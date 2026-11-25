package tui

import (
 "context"
 "time"
 tea "github.com/charmbracelet/bubbletea"
 "github.com/fernandocpaz/tailg/internal/core"
)

type ExternalInventory func(context.Context)([]core.AppChoice,string,error)
type externalInventoryTick struct{}
type externalInventoryResult struct{ apps []core.AppChoice; note string; err error }

func(m *pickerModel) startExternalInventory() tea.Cmd {
 if m.externalInventory==nil||m.externalInventoryBusy{return nil}
 m.externalInventoryBusy=true
 load,ctx:=m.externalInventory,m.refreshContext
 if ctx==nil{ctx=context.Background()}
 return func()tea.Msg{
  ctx,cancel:=context.WithTimeout(ctx,25*time.Second);defer cancel()
  apps,note,err:=load(ctx)
  return externalInventoryResult{apps,note,err}
 }
}
func(m pickerModel) externalInventoryStatus()string{
 if m.externalInventoryBusy{return "Refreshing pod telemetry…"}
 if m.inventoryNote!=""{return m.inventoryNote}
 return "Pod telemetry: refresh every 30s"
}
func(m *pickerModel) updateExternalInventory(msg tea.Msg)(bool,tea.Cmd){
 if m.externalInventory==nil{return false,nil}
 switch v:=msg.(type){
 case externalInventoryTick:
  return true,m.startExternalInventory()
 case externalInventoryResult:
  m.externalInventoryBusy=false
  m.inventoryNote=v.note
  if v.err!=nil{m.inventoryNote="Pod telemetry unavailable: "+v.err.Error()}
  // On failures replace provider fields with unknown, but keep the existing
  // selectable log inventory. Preserve checked pod identities and cursor.
  if v.apps!=nil{
   current:=""
   if m.index>=0&&m.index<len(m.apps){current=m.apps[m.index].Name}
   m.apps=v.apps;m.allApps=v.apps;m.totalApps=len(v.apps)
   m.index=min(m.index,max(0,len(m.apps)-1))
   for i,a:=range m.apps{if a.Name==current{m.index=i;break}}
   if m.selection!=nil&&m.selection.pods!=nil{
    p:=m.selection.pods
    current:=""
    if p.index>=0&&p.index<len(p.rows){current=p.rows[p.index].pod.Name}
    p.rows=preparePodPickerRows(v.apps);p.index=min(p.index,max(0,len(p.rows)-1))
    for i,r:=range p.rows{if r.pod.Name==current{p.index=i;break}}
   }
  }
  return true,tea.Tick(30*time.Second,func(time.Time)tea.Msg{return externalInventoryTick{}})
 }
 return false,nil
}
