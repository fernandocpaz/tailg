package tui

import (
  "context"
  "fmt"
  "strings"

  tea "github.com/charmbracelet/bubbletea"
  "github.com/charmbracelet/lipgloss"
  "github.com/charmbracelet/x/ansi"
)

// renderUpdateDialog powers both the original tailg startup update view and
// external-provider apps. The layout and keys are intentionally shared.
func renderUpdateDialog(application, installed, latest string, busy, focused bool, failure string, width,height int) string {
  if width < 40 || height < 12 {
    if busy {
      return ansi.Wrap("Updating…\nCtrl+C quits.", max(1,width),"")
    }
    return ansi.Wrap("Update available. Resize to 40 × 12.\nEsc continues without updating.",max(1,width),"")
  }
  inner:=min(76,width-6)
  header:=strings.Split(ansi.Wrap(headerStyle.Render(application+" update available")+"\nInstalled: "+installed+" · Latest: "+latest,inner,""),"\n")
  var body,footer []string
  if busy {
    body=strings.Split(ansi.Wrap("Downloading and verifying the release…\n"+application+" will restart after installation.",inner,""),"\n")
  } else {
    later, install := "> [ Later ]", "  [ Update & restart ]"
    if focused {later,install="  [ Later ]","> [ Update & restart ]"}
    footer=strings.Split(ansi.Wrap(later+"  "+install+"\nU Update · Enter Select · Esc Later",inner,""),"\n")
    if failure!="" {
      body=pickerLimitedLines("Update failed: "+failure,inner,max(1,height-5-len(header)-len(footer)))
      if width>=80 && height>=18 {
        body=append(body,fmt.Sprintf("Manual: go install github.com/fernandocpaz/%s/cmd/%s@%s",strings.ToLower(application),strings.ToLower(application),latest))
      }
    }
  }
  lines:=append(header,body...)
  if len(footer)>0{lines=append(lines,"");lines=append(lines,footer...)}
  content:=ansi.Wrap(strings.Join(lines,"\n"),inner,"")
  return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(accentColor).
    Foreground(modalForeground).Background(modalBackground).Padding(0,1).Width(inner+2).Render(content)
}

// ExternalUpdateOffer displays the actual tailg update dialog while allowing
// a separate executable to install its *own* verified release asset.
type ExternalUpdateOffer struct {
  Application string
  Installed string
  Latest string
  Install func(context.Context) error
}
type externalUpdateInstalledMsg struct{ err error }

type externalUpdateModel struct {
  ctx context.Context
  offer ExternalUpdateOffer
  width,height int
  focused,busy,installed,quit bool
  failure string
}

func (m externalUpdateModel) Init() tea.Cmd {
  return tea.SetWindowTitle(m.offer.Application+" update")
}
func (m externalUpdateModel) Update(message tea.Msg)(tea.Model,tea.Cmd){
  switch msg:=message.(type){
  case tea.WindowSizeMsg:
    m.width,m.height=msg.Width,msg.Height
    return m,nil
  case externalUpdateInstalledMsg:
    m.busy=false
    if msg.err!=nil{
      m.failure=msg.err.Error()
      return m,nil
    }
    m.installed=true
    return m,tea.Quit
  case tea.KeyMsg:
    if msg.String()=="ctrl+c" {
      m.quit=true
      return m,tea.Quit
    }
    if m.busy{return m,nil}
    switch msg.String(){
    case "esc","q","Q","l","L","n","N":
      return m,tea.Quit
    case "tab","shift+tab","left","right":
      m.focused=!m.focused
      return m,nil
    case "enter":
      if !m.focused{return m,tea.Quit}
    case "u","U":
    default:
      return m,nil
    }
    m.busy=true
    m.failure=""
    install,ctx:=m.offer.Install,m.ctx
    return m,func()tea.Msg{return externalUpdateInstalledMsg{err:install(ctx)}}
  }
  return m,nil
}
func (m externalUpdateModel) View()string{
  width,height:=m.width,m.height
  if width<1{width=90}
  if height<1{height=22}
  dialog:=renderUpdateDialog(m.offer.Application,m.offer.Installed,m.offer.Latest,m.busy,m.focused,m.failure,width,height)
  if width<40||height<12{return dialog}
  background:=strings.Repeat(strings.Repeat(" ",width)+"\n",max(0,height-1))+strings.Repeat(" ",width)
  return overlayModal(background,dialog,width,height)
}

// PromptExternalUpdate reports installed and Ctrl+C cancel separately.
func PromptExternalUpdate(ctx context.Context,offer ExternalUpdateOffer)(installed,quit bool,err error){
  if offer.Install==nil{return false,false,fmt.Errorf("update install callback is required")}
  if offer.Application==""||offer.Installed==""||offer.Latest==""{
    return false,false,fmt.Errorf("update offer application and versions are required")
  }
  state:=externalUpdateModel{ctx:ctx,offer:offer}
  finished,err:=tea.NewProgram(state,tea.WithAltScreen()).Run()
  if err!=nil{return false,false,err}
  result,ok:=finished.(externalUpdateModel)
  if !ok{return false,false,fmt.Errorf("update dialog returned unexpected result")}
  return result.installed,result.quit,nil
}
