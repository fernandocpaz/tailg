package tui

import (
 "context"
 "strings"
 "time"
 "unicode"
 "unicode/utf8"

 tea "github.com/charmbracelet/bubbletea"
 "github.com/charmbracelet/lipgloss"
)

type ExternalScopeDiscovery func(context.Context,string)([]string,string,error)
type externalScopeDialog struct{
 kind,query,warning string
 names []string
 index int
 loading bool
 cancel context.CancelFunc
}
type externalScopeResult struct{request int;names []string;warning string;err error}
type externalScopeTick struct{request int}
func scopeTick(request int)tea.Cmd{return tea.Tick(250*time.Millisecond,func(time.Time)tea.Msg{return externalScopeTick{request}})}
func(m *pickerModel)closeExternalScope(){
 if m.scopeDialog!=nil&&m.scopeDialog.cancel!=nil{m.scopeDialog.cancel()}
 m.scopeDialog=nil
}
func(m *pickerModel)openExternalScope(kind string)tea.Cmd{
 m.closeExternalScope()
 m.scopeRequest++
 request:=m.scopeRequest
 parent:=m.refreshContext;if parent==nil{parent=context.Background()}
 ctx,cancel:=context.WithTimeout(parent,30*time.Second)
 current:=m.namespace;if kind=="cluster"{current=m.providerName;if current=="all clusters"{current=""}}
 m.scopeDialog=&externalScopeDialog{kind:kind,names:[]string{current},loading:true,cancel:cancel}
 discover:=m.externalScope
 return tea.Batch(scopeTick(request),func()tea.Msg{
  defer cancel()
  names,warning,err:=discover(ctx,kind)
  return externalScopeResult{request,names,warning,err}
 })
}
func(m *pickerModel)externalScopeMessage(message tea.Msg)(bool,tea.Cmd){
 switch msg:=message.(type){
 case externalScopeTick:
  if m.scopeDialog!=nil&&m.scopeRequest==msg.request&&m.scopeDialog.loading{return true,scopeTick(msg.request)}
  return true,nil
 case externalScopeResult:
  d:=m.scopeDialog
  if d==nil||m.scopeRequest!=msg.request{return true,nil}
  d.loading=false;d.warning=msg.warning
  if msg.err!=nil{d.warning="Discovery unavailable: "+pickerErrorSummary(msg.err)}
  seen:=map[string]bool{}
  var names []string
  for _,name:=range append(msg.names,d.names...){
   if !seen[name]&&(name!=""||d.kind=="cluster"){seen[name]=true;names=append(names,name)}
  }
  if d.kind=="cluster"&&!seen[""]{names=append(names,"")}
  d.names=names;d.index=0
  return true,nil
 }
 return false,nil
}
type scopeChoice struct{value,label string}
func(d externalScopeDialog)choices()[]scopeChoice{
 var rows []scopeChoice
 exact:=false
 query:=strings.TrimSpace(d.query)
 for _,name:=range d.names{
  label:=name;if name==""{label="all clusters"}
  if strings.EqualFold(name,query){exact=true}
  if strings.Contains(strings.ToLower(label),strings.ToLower(query)){rows=append(rows,scopeChoice{name,label})}
 }
 if query!=""&&!exact&&!strings.EqualFold(query,"all clusters"){rows=append(rows,scopeChoice{query,"Use exact name: "+query})}
 return rows
}
func(m pickerModel)externalScopeKey(key tea.KeyMsg)(tea.Model,tea.Cmd){
 d:=m.scopeDialog
 switch key.String(){
 case "ctrl+c":
  m.closeExternalScope();m.result.Action=PickerQuit;return m,tea.Quit
 case "esc":m.closeExternalScope();return m,nil
 case "ctrl+r":return m,m.openExternalScope(d.kind)
 case "up":d.index=max(0,d.index-1)
 case "down":d.index=min(max(0,len(d.choices())-1),d.index+1)
 case "pgup":d.index=max(0,d.index-8)
 case "pgdown":d.index=min(max(0,len(d.choices())-1),d.index+8)
 case "backspace":
  if d.query!=""{_,size:=utf8.DecodeLastRuneInString(d.query);d.query=d.query[:len(d.query)-size]};d.index=0
 case "ctrl+u":d.query="";d.index=0
 case "enter":
  rows:=d.choices();if len(rows)==0{return m,nil}
  m.result=PickerResult{Action:PickerSwitchScope,ScopeKind:d.kind,ScopeValue:rows[d.index].value}
  m.closeExternalScope()
  return m,tea.Quit
 default:
  if key.Type==tea.KeyRunes||key.Type==tea.KeySpace{
   for _,r:=range key.Runes{if !unicode.IsControl(r)&&len(d.query)<253{d.query+=string(r)}}
   d.index=0
  }
 }
 return m,nil
}
func(m pickerModel)externalScopeView(width,height int)string{
 d:=m.scopeDialog
 inner:=max(1,min(84,width-6))
 if width<30||height<10{return "Resize terminal to choose scope. Esc cancels."}
 lines:=[]string{headerStyle.Render("Switch Kubernetes "+d.kind),"Current: "+m.providerName+" / "+m.namespace,"Search: "+d.query+"▏"}
 if d.loading{lines=append(lines,loadingLabel("Discovering from Dynatrace…"))}
 if d.warning!=""{lines=append(lines,pickerLimitedLines(d.warning,inner,2)...)}
 rows:=d.choices()
 capacity:=max(1,height-9-len(lines))
 start:=max(0,d.index-capacity+1)
 for i:=start;i<min(len(rows),start+capacity);i++{lines=append(lines,selectionChoiceRow(rows[i].label,inner,i==d.index))}
 if len(rows)==0{lines=append(lines,"Type a name to select it directly.")}
 lines=append(lines,"↑↓ Move · Enter Select · Esc Cancel","Type to search · Ctrl+U Clear · Ctrl+R Retry")
 for i:=range lines{lines[i]=truncatePickerCell(lines[i],inner)}
 return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(accentColor).Foreground(modalForeground).Background(modalBackground).Padding(0,1).Width(inner+2).Render(strings.Join(lines,"\n"))
}
