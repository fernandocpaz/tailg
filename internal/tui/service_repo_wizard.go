package tui

import (
 "context"
 "fmt"
 "regexp"
 "strings"

 "github.com/charmbracelet/bubbles/textinput"
 tea "github.com/charmbracelet/bubbletea"
 "github.com/charmbracelet/lipgloss"

 "github.com/fernandocpaz/tailg/internal/ai"
)

var replicaSuffix = regexp.MustCompile(`-[a-z0-9]{8,12}-[a-z0-9]{4,6}$`)
var jobSuffix = regexp.MustCompile(`-[0-9]{8,14}-[a-z0-9]{4,6}$`)

// Workload identity is preferred because pod suffixes vary after deployments.
// Fall back to a pod-derived name for older streams lacking controller metadata.
func (m model) selectedAIService() string {
 var workload,pod string
 if m.detailRecord!=nil {
  workload=strings.TrimSpace(m.detailRecord.Event.Workload)
  pod=strings.TrimSpace(m.detailRecord.Event.Pod)
 }
 if workload=="" {workload=replicaSuffix.ReplaceAllString(pod,"")}
 if workload==pod {workload=jobSuffix.ReplaceAllString(workload,"")}
 return ai.ServiceKey(m.config.Namespace,workload)
}

type serviceRepoWizard struct {
 service string
 candidates []string
 index int
 mode string // "" = picker, "path", "url", "confirm"
 input textinput.Model
 url string
 note string
 busy bool
 generation int
 cancel context.CancelFunc
}
type serviceCloneMsg struct{
 service string
 generation int
 repo ai.ServiceRepository
 err error
}
func newServiceRepoWizard(service,reason string)*serviceRepoWizard {
 w:=&serviceRepoWizard{service:service,candidates:ai.DiscoverRepos(),note:reason}
 w.input=textinput.New()
 w.input.Prompt="> "
 w.input.CharLimit=2048
 w.input.Width=64
 return w
}
func (w *serviceRepoWizard) stop(){
 if w.cancel!=nil{w.cancel();w.cancel=nil}
 w.generation++
 w.busy=false
}
func (m model) openServiceRepoWizard(service,reason string)(tea.Model,tea.Cmd){
 if service=="" {return m,nil}
 if m.serviceRepoSetup!=nil{m.serviceRepoSetup.stop()}
 m.cancelAI()
 m.aiLoading=false
 m.aiView=false
 m.serviceRepoSetup=newServiceRepoWizard(service,reason)
 return m,nil
}
func (m model) saveServiceRepo(service string,repo ai.ServiceRepository)(tea.Model,tea.Cmd) {
 config:=m.config.AISettings
 if config.APIKey=="" || config.Model==""{
  m.serviceRepoSetup.note="Configure the AI provider before saving source locations."
  return m,nil
 }
 if config.ServiceRepos==nil{config.ServiceRepos=map[string]ai.ServiceRepository{}}
 // Copy map to avoid mutating the live configuration if persistence fails.
 copied:=make(map[string]ai.ServiceRepository,len(config.ServiceRepos)+1)
 for key,value:=range config.ServiceRepos{copied[key]=value}
 copied[service]=repo
 config.ServiceRepos=copied
 if err:=ai.SaveSettings(config);err!=nil{
  m.serviceRepoSetup.note="Unable to save this mapping: "+err.Error()
  return m,nil
 }
 m.config.AISettings=config
 m.serviceRepoSetup.stop()
 m.serviceRepoSetup=nil
 m.aiText=""
 m.notice="Source mapping saved for "+service
 return m.toggleAIExplanation()
}
func (m model) updateServiceRepoWizardKey(msg tea.KeyMsg)(tea.Model,tea.Cmd){
 w:=m.serviceRepoSetup
 if w==nil{return m,nil}
 key:=msg.String()
 if key=="esc" || key=="ctrl+c"{
  w.stop();m.serviceRepoSetup=nil
  return m,nil
 }
 if w.busy{return m,nil}
 if w.mode=="path" || w.mode=="url"{
  if key=="enter" {
   raw:=strings.TrimSpace(w.input.Value())
   if w.mode=="path"{
    repo,err:=ai.LocalServiceRepo(raw)
    if err!=nil{w.note=err.Error();return m,nil}
    return m.saveServiceRepo(w.service,repo)
   }
   url,err:=ai.ValidateCloneURL(raw)
   if err!=nil{w.note=err.Error();return m,nil}
   w.url=url;w.mode="confirm";w.note="Cloning retrieves code to a new tailg-managed directory. It will not touch working repos."
   return m,nil
  }
  var cmd tea.Cmd
  w.input,cmd=w.input.Update(msg)
  return m,cmd
 }
 if w.mode=="confirm" {
  switch key{
  case "n","N","backspace":
   w.mode="url";w.input.SetValue(w.url);w.input.Focus();return m,nil
  case "y","Y","enter":
   parent:=m.ctx
   if parent==nil{parent=context.Background()}
   ctx,cancel:=context.WithCancel(parent)
   w.stop();w.generation++
   generation:=w.generation
   w.cancel=cancel;w.busy=true
   w.note="Querying remote HEAD and cloning the default branch (read-only source analysis)..."
   service,url:=w.service,w.url
   return m,func()tea.Msg{
    defer cancel()
    repo,err:=ai.CloneService(ctx,service,url)
    return serviceCloneMsg{service:service,generation:generation,repo:repo,err:err}
   }
  }
  return m,nil
 }
 switch key{
 case "up","k":
  if w.index>0{w.index--}
 case "down","j":
  if w.index+1<len(w.candidates){w.index++}
 case "enter":
  if len(w.candidates)>0 {
   repo,err:=ai.LocalServiceRepo(w.candidates[w.index])
   if err!=nil{w.note=err.Error();return m,nil}
   return m.saveServiceRepo(w.service,repo)
  }
 case "a","A":
  w.mode="path";w.note="Enter the path of an existing repository (it will not be changed)."
  w.input.SetValue("");w.input.Focus()
 case "c","C":
  if !ai.GitAvailable(){w.note="Git is not installed; select or add a local source folder.";return m,nil}
  w.mode="url";w.note="Enter HTTPS or SSH Git URL. Credentials must not be embedded in the URL."
  w.input.SetValue("");w.input.Focus()
 case "l","L":
  w.stop();m.serviceRepoSetup=nil
  // Explicit request to use logs alone, no cross-service source context.
  return m.startAIExplanation(nil)
 }
 return m,nil
}
func (m model) updateServiceClone(msg serviceCloneMsg)(tea.Model,tea.Cmd){
 w:=m.serviceRepoSetup
 if w==nil||msg.generation!=w.generation||msg.service!=w.service{return m,nil}
 w.stop()
 if msg.err!=nil{
  w.mode="url";w.note="Clone failed: "+msg.err.Error()+". Check Git access or choose a local folder."
  w.input.SetValue(w.url);w.input.Focus()
  return m,nil
 }
 return m.saveServiceRepo(w.service,msg.repo)
}

func (m model) renderServiceRepoWizard()string {
 w:=m.serviceRepoSetup
 if w==nil{return ""}
 width:=max(36,min(m.width-4,92))
 var content strings.Builder
 fmt.Fprintf(&content,"  SOURCE REPOSITORY FOR AI\n\nService: %s\n\n",w.service)
 switch w.mode{
 case "path":
  content.WriteString("Local checkout path (no files will be modified):\n")
  content.WriteString(w.input.View()+"\n")
 case "url":
  content.WriteString("Clone URL (https:// or git@host:group/repo.git):\n")
  content.WriteString(w.input.View()+"\n")
 case "confirm":
  fmt.Fprintf(&content,"Clone into tailg's PRIVATE cache directory?\n%s\n\n",w.url)
  content.WriteString("The remote's symbolic HEAD selects the branch; its commit is recorded.\n")
  content.WriteString("No checkout, reset, pull, or cleanup of your work repos.\n")
  content.WriteString("Press Y/Enter to clone, N to edit.\n")
 default:
  content.WriteString("Choose one local checkout for this service:\n")
  if len(w.candidates)==0{content.WriteString("No local repositories detected. Add a path or clone a URL.\n")}
  start:=max(0,min(w.index-3,len(w.candidates)-7))
  end:=min(len(w.candidates),start+7)
  for i:=start;i<end;i++{
   pointer:="  ";if i==w.index{pointer="> "}
   fmt.Fprintf(&content,"%s%s\n",pointer,w.candidates[i])
  }
  content.WriteString("\nUp/Down + Enter: select | A: local path | C: clone Git URL\n")
  content.WriteString("L: continue with logs only (no source code)\n")
 }
 if w.busy{content.WriteString("\nWorking on the isolated clone...\n")}
 if w.note!=""{content.WriteString("\n"+w.note+"\n")}
 content.WriteString("\nEsc: cancel; no provider request is made until mapping or log-only selection.")
 dialog:=lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(accentColor).
  Background(modalBackground).Foreground(modalForeground).Padding(0,1).
  Width(width-2).Render(content.String())
 background:=m
 background.serviceRepoSetup=nil
 return overlayModal(background.View(),dialog,m.width,m.height)
}
