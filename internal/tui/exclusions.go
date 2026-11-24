package tui

import (
 "encoding/json"
 "fmt"
 "os"
 "path/filepath"
 "strings"
 "unicode"

 "github.com/charmbracelet/bubbles/textinput"
 tea "github.com/charmbracelet/bubbletea"
 "github.com/charmbracelet/lipgloss"
 "github.com/charmbracelet/x/ansi"
 "github.com/fernandocpaz/tailg/internal/core"
)

const maxExclusions=256
const maxExclusionBytes=4096
type exclusionFrame struct{rows []string;top,width int}
type exclusionSelection struct{rows []string;row,anchor,end,top,width int;dragging bool}
type exclusionState struct{
 patterns []string
 open bool
 index int
 input textinput.Model
 notice string
 frame *exclusionFrame
 selection *exclusionSelection
}
func newExclusionState(path string)exclusionState{
 s:=exclusionState{frame:&exclusionFrame{},input:textinput.New()}
 s.input.Prompt="Add: "
 s.input.Placeholder="literal text to exclude"
 s.input.CharLimit=maxExclusionBytes
 if path==""{return s}
 data,err:=os.ReadFile(path)
 if os.IsNotExist(err){return s}
 if err==nil{
  if len(data)>maxExclusions*(maxExclusionBytes*6+8){err=fmt.Errorf("exclusion file too large")}else{err=json.Unmarshal(data,&s.patterns)}
 }
 if err!=nil{s.notice="Cannot load exclusions: "+err.Error();s.patterns=nil;return s}
 var valid []string
 for _,p:=range s.patterns{if validExclusion(p)&&len(valid)<maxExclusions&&!containsExclusion(valid,p){valid=append(valid,p)}}
 s.patterns=valid
 return s
}
func validExclusion(p string)bool{
 return strings.TrimSpace(p)!=""&&len(p)<=maxExclusionBytes&&strings.IndexFunc(p,unicode.IsControl)<0
}
func containsExclusion(patterns []string,p string)bool{
 for _,value:=range patterns{if value==p{return true}}
 return false
}
func saveExclusions(path string,patterns []string)error{
 if path==""{return fmt.Errorf("exclusion storage unavailable")}
 if err:=os.MkdirAll(filepath.Dir(path),0700);err!=nil{return err}
 data,err:=json.MarshalIndent(patterns,"","  ");if err!=nil{return err}
 f,err:=os.CreateTemp(filepath.Dir(path),".exclusions-*");if err!=nil{return err}
 defer os.Remove(f.Name())
 _,err=f.Write(data)
 if err==nil{err=f.Sync()}
 closeErr:=f.Close();if err==nil{err=closeErr}
 if err!=nil{return err}
 return os.Rename(f.Name(),path)
}
func(m *model)applyExclusions(patterns []string)bool{
 if err:=saveExclusions(m.config.ExclusionFile,patterns);err!=nil{m.exclusions.notice="Save failed: "+err.Error();return false}
 m.exclusions.patterns=patterns
 m.state.SetExclusions(patterns)
 if m.timeState.live!=nil&&m.timeState.live!=m.state{m.timeState.live.SetExclusions(patterns)}
 m.selected=m.state.VisibleCount()-1
 m.scroll=0
 return true
}
func(m *model)addExclusion(value string)bool{
 value=strings.TrimSpace(value)
 if !validExclusion(value){m.exclusions.notice="Enter nonempty literal text (single line, max 4096 bytes).";return false}
 if containsExclusion(m.exclusions.patterns,value){m.exclusions.notice="Already excluded.";return false}
 if len(m.exclusions.patterns)>=maxExclusions{m.exclusions.notice="Maximum 256 exclusions.";return false}
 next:=append(append([]string(nil),m.exclusions.patterns...),value)
 if !m.applyExclusions(next){return false}
 m.exclusions.index=len(next)-1;m.exclusions.notice="Excluded and saved."
 return true
}
func(m model)exclusionTimeline()bool{
 return m.config.ExclusionFile!=""&&m.width>0&&m.height>0&&!m.exclusions.open&&
 !m.timeState.dialog.open&&m.aiSetup==nil&&m.serviceRepoSetup==nil&&!m.showUpdate()&&
 !m.errorLogs.open&&!m.replicas.open&&!m.traceOpen&&!m.freshnessOpen&&!m.heartbeatOpen&&!m.issueOpen&&
 !m.analyticsOpen&&!m.securityRadarOpen&&!m.resourceOpen&&m.detail==""
}
func(m model)exclusionPill()string{
 label:=fmt.Sprintf("E [%d]",len(m.exclusions.patterns))
 if m.config.Formatter.Color{return modeStyle.Render(label)}
 return label
}
// Capture the actual last rendered viewport, not an independently re-rendered
// stream. Keep that viewport stable during selection even as live logs arrive.
func(m model)exclusionRows(rows []string,top int)[]string{
 if !m.exclusionTimeline(){return rows}
 s:=m.exclusions.selection
 if s!=nil&&s.top==top&&s.width==m.width&&len(s.rows)==len(rows){
  rows=append([]string(nil),s.rows...)
  a,b:=min(s.anchor,s.end),max(s.anchor,s.end)
  if b>a&&s.row>=0&&s.row<len(rows){
   plain:=core.StripANSI(rows[s.row])
   rows[s.row]=ansi.Cut(plain,0,a)+matchStyle.Reverse(true).Render(ansi.Cut(plain,a,b))+ansi.Cut(plain,b,lipgloss.Width(plain))
  }
 }
 if m.exclusions.frame!=nil{
  m.exclusions.frame.rows=append(m.exclusions.frame.rows[:0],rows...)
  m.exclusions.frame.top=top;m.exclusions.frame.width=m.width
 }
 return rows
}
func(m *model)selectExclusionText(msg tea.MouseMsg){
 s:=&m.exclusions
 switch msg.Action{
 case tea.MouseActionPress:
  if msg.Button!=tea.MouseButtonLeft{return}
  s.selection=nil
  frame:=s.frame
  if frame==nil||frame.width!=m.width{return}
  row:=msg.Y-frame.top
  if row<0||row>=len(frame.rows){return}
  plain:=core.StripANSI(frame.rows[row])
  if strings.TrimSpace(plain)==""||strings.HasPrefix(plain,"── "){return}
  x:=min(max(0,msg.X),lipgloss.Width(plain))
  s.selection=&exclusionSelection{rows:append([]string(nil),frame.rows...),row:row,anchor:x,end:x,top:frame.top,width:frame.width,dragging:true}
 case tea.MouseActionMotion,tea.MouseActionRelease:
  sel:=s.selection
  if sel==nil||!sel.dragging{return}
  if sel.top!=s.frame.top||sel.width!=m.width{ s.selection=nil;return }
  width:=lipgloss.Width(core.StripANSI(sel.rows[sel.row]))
  sel.end=min(max(0,msg.X),width)
  if msg.Action==tea.MouseActionRelease{
   sel.dragging=false
   if sel.end==sel.anchor{s.selection=nil}
  }
 }
}
func selectedExclusion(s *exclusionSelection)string{
 if s==nil||s.row<0||s.row>=len(s.rows){return ""}
 a,b:=min(s.anchor,s.end),max(s.anchor,s.end)
 return strings.TrimSpace(ansi.Cut(core.StripANSI(s.rows[s.row]),a,b))
}
func(m model)openExclusions()(tea.Model,tea.Cmd){
 value:=selectedExclusion(m.exclusions.selection)
 m.exclusions.selection=nil
 m.exclusions.open=true
 m.exclusions.notice=""
 m.exclusions.input.SetValue("")
 m.exclusions.index=max(0,min(m.exclusions.index,len(m.exclusions.patterns)-1))
 if value!=""&&!m.addExclusion(value){m.exclusions.input.SetValue(value)}
 cmd:=m.exclusions.input.Focus()
 return m,cmd
}
func(m model)updateExclusions(key tea.KeyMsg)(tea.Model,tea.Cmd){
 s:=&m.exclusions
 switch key.String(){
 case "ctrl+c","ctrl+q":
  if m.cancel!=nil{m.cancel()};return m,tea.Quit
 case "esc":
  s.open=false;s.input.Blur();return m,nil
 case "up":s.index=max(0,s.index-1)
 case "down":s.index=min(max(0,len(s.patterns)-1),s.index+1)
 case "pgup":s.index=max(0,s.index-8)
 case "pgdown":s.index=min(max(0,len(s.patterns)-1),s.index+8)
 case "delete":
  if len(s.patterns)>0{
   next:=append([]string(nil),s.patterns[:s.index]...)
   next=append(next,s.patterns[s.index+1:]...)
   if m.applyExclusions(next){s.index=min(s.index,max(0,len(next)-1));s.notice="Removed and saved."}
  }
 case "enter":
  if m.addExclusion(s.input.Value()){s.input.SetValue("")}
 case "ctrl+u":s.input.SetValue("")
 default:
  var cmd tea.Cmd
  s.input,cmd=s.input.Update(key)
  return m,cmd
 }
 return m,nil
}
func(m model)exclusionDialog()string{
 s:=m.exclusions
 if m.width<30||m.height<10{return "Resize terminal for exclusions. Esc closes."}
 inner:=min(94,m.width-6)
 s.input.Width=max(1,inner-6)
 lines:=[]string{headerStyle.Render(fmt.Sprintf("EXCLUSIONS · %d",len(s.patterns))),"This window · literal, case-sensitive · saved automatically",s.input.View()}
 if s.notice!=""{lines=append(lines,truncatePickerCell(s.notice,inner))}
 if len(s.patterns)==0{lines=append(lines,"No exclusions. Drag log text then press E, or type above.")}
 capacity:=max(1,m.height-9-len(lines))
 start:=max(0,s.index-capacity+1)
 for i:=start;i<min(len(s.patterns),start+capacity);i++{lines=append(lines,selectionChoiceRow(s.patterns[i],inner,i==s.index))}
 lines=append(lines,"Enter Add · ↑↓ Select · Delete Remove","Esc Close · Ctrl+U Clear input")
 for i:=range lines{lines[i]=truncatePickerCell(lines[i],inner)}
 return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(accentColor).Foreground(modalForeground).Background(modalBackground).Padding(0,1).Width(inner+2).Render(strings.Join(lines,"\n"))
}
