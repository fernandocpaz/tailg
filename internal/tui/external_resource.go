package tui
import(
 "context"
 "time"
 tea "github.com/charmbracelet/bubbletea"
)
type ExternalResourceOpen func(context.Context,string,string)(string,error)
type externalResourceResult struct{request int;message string;err error}
type externalResourceTick struct{request int}
func resourceBrowserTick(request int)tea.Cmd{return tea.Tick(250*time.Millisecond,func(time.Time)tea.Msg{return externalResourceTick{request}})}
func(m *pickerModel)openExternalResource(workload,pod string)tea.Cmd{
 if m.externalResource==nil||m.resourceOpening{return nil}
 m.resourceOpening=true
 m.resourceRequest++
 request:=m.resourceRequest
 parent:=m.refreshContext;if parent==nil{parent=context.Background()}
 open:=m.externalResource
 m.note="Opening Dynatrace resources…"
 if m.selection!=nil{m.selection.err=""}
 return tea.Batch(resourceBrowserTick(request),func()tea.Msg{
  ctx,cancel:=context.WithTimeout(parent,25*time.Second);defer cancel()
  message,err:=open(ctx,workload,pod)
  return externalResourceResult{request,message,err}
 })
}
func(m *pickerModel)externalResourceMessage(message tea.Msg)(bool,tea.Cmd){
 switch msg:=message.(type){
 case externalResourceTick:
  if m.resourceOpening&&m.resourceRequest==msg.request{m.note=loadingLabel("Opening Dynatrace resources…");return true,resourceBrowserTick(msg.request)}
  return true,nil
 case externalResourceResult:
  if m.resourceRequest!=msg.request{return true,nil}
  m.resourceOpening=false
  m.note=msg.message
  if msg.err!=nil{m.note="Dynatrace resources: "+pickerErrorSummary(msg.err)}
  if m.selection!=nil{m.selection.err=m.note}
  return true,nil
 }
 return false,nil
}
