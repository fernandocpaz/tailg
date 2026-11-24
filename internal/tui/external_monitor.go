package tui

import (
 "context"
 "fmt"
 "strings"
 "time"

 tea "github.com/charmbracelet/bubbletea"
)

// ExternalWorkloadSample contains only metrics actually observed for one
// workload. CPU is % of its configured limit, not total node CPU.
type ExternalWorkloadSample struct {
 Name string
 CPUPercent float64
 CPUKnown bool
 MemoryBytes float64
 MemoryKnown bool
 MemoryLimitBytes float64
 MemoryLimitKnown bool
 Lines,Warnings,Errors int
 LogsComplete bool
}
type ExternalMonitorSnapshot struct {
 At time.Time
 Workloads []ExternalWorkloadSample
 Warning string
}
type ExternalMonitor func(context.Context)(ExternalMonitorSnapshot,error)
type externalStartMonitorMsg struct{}
type externalMonitorPollMsg struct {generation int}
type externalMonitorResultMsg struct {
 generation int
 snapshot ExternalMonitorSnapshot
 err error
}
const externalMonitorInterval=30*time.Second

func(m pickerModel) readExternalMonitor()tea.Cmd{
 callback:=m.externalMonitor
 generation:=m.monitorGeneration
 ctx:=m.refreshContext
 return func()tea.Msg{
  if callback==nil{return externalMonitorResultMsg{generation:generation,err:fmt.Errorf("Dynatrace monitoring not configured")}}
  ctx,cancel:=context.WithTimeout(ctx,25*time.Second)
  defer cancel()
  snapshot,err:=callback(ctx)
  return externalMonitorResultMsg{generation:generation,snapshot:snapshot,err:err}
 }
}
func(m pickerModel) pollExternalMonitor()tea.Cmd{
 generation:=m.monitorGeneration
 return tea.Tick(externalMonitorInterval,func(time.Time)tea.Msg{
  return externalMonitorPollMsg{generation:generation}
 })
}
func(m *pickerModel) toggleExternalMonitor()tea.Cmd{
 if m.externalMonitor==nil {
  m.note="Dynatrace metrics source is not configured (no Kubernetes calls were made)"
  return nil
 }
 if m.monitorEnabled {
  m.podMonitorState.stop()
  m.externalSamples=nil
  m.charts.resources=nil
  m.charts.logs=nil
  m.monitorApps=nil
  return nil
 }
 m.monitorGeneration++
 m.monitorEnabled=true
 m.monitorBusy=true
 m.monitorFlashOn=true
 m.monitorError=""
 m.metricsWarning=""
 m.monitorLastScan=time.Time{}
 m.externalSamples=make(map[string]ExternalWorkloadSample)
 m.monitorApps=make(map[string]podHealthDisplay)
 m.charts.resources=make(map[string][]pickerResourcePoint)
 m.charts.logs=make(map[string][]pickerLogPoint)
 return tea.Batch(m.readExternalMonitor(),m.pollExternalMonitor(),podMonitorFlashCmd(m.monitorGeneration))
}
func(m *pickerModel) updateExternalMonitor(message tea.Msg)(bool,tea.Cmd){
 if !m.external{return false,nil}
 switch msg:=message.(type){
 case externalStartMonitorMsg:
  if m.monitorEnabled{return true,nil}
  return true,m.toggleExternalMonitor()
 case externalMonitorPollMsg:
  if !m.monitorEnabled||msg.generation!=m.monitorGeneration{return true,nil}
  cmds:=[]tea.Cmd{m.pollExternalMonitor()}
  if !m.monitorBusy{
   m.monitorBusy=true
   cmds=append(cmds,m.readExternalMonitor())
  }
  return true,tea.Batch(cmds...)
 case externalMonitorResultMsg:
  if !m.monitorEnabled||msg.generation!=m.monitorGeneration{return true,nil}
  m.monitorBusy=false
  if msg.err!=nil{
   m.monitorError="Dynatrace metrics unavailable: "+msg.err.Error()
   m.metricsWarning=m.monitorError
   m.externalSamples=nil
   if m.monitorApps==nil{m.monitorApps=make(map[string]podHealthDisplay)}
   for _,app:=range m.apps{
    m.monitorApps[app.Name]=podHealthDisplay{kind:podHealthUnknown,reason:"Last monitoring query failed; previous values are stale"}
   }
   return true,nil
  }
  now:=msg.snapshot.At
  if now.IsZero(){now=time.Now()}
  m.monitorLastScan=now
  m.metricsAt=now
  m.metricsWarning=msg.snapshot.Warning
  m.monitorError=msg.snapshot.Warning
  m.externalSamples=make(map[string]ExternalWorkloadSample,len(msg.snapshot.Workloads))
  for _,sample:=range msg.snapshot.Workloads{
   if sample.Name==""{continue}
   m.externalSamples[sample.Name]=sample
  }
  for _,app:=range m.apps{
   sample,ok:=m.externalSamples[app.Name]
   previous:=m.monitorApps[app.Name].sample
   state:=podHealthDisplay{kind:podHealthUnknown,reason:"No complete log sample for workload"}
   if ok && sample.LogsComplete {
    current:=podHealthSample{lines:sample.Lines,warnings:sample.Warnings,errors:sample.Errors}
    state=classifyPodHealth(current,previous)
    if state.kind==podHealthWarning{
     state.warningsIncreasing=sample.Warnings>previous.warnings
    }
   }
   m.monitorApps[app.Name]=state
   resource:=pickerResourcePoint{at:now}
   if ok {
    resource.cpu=sample.CPUPercent
    resource.cpuKnown=sample.CPUKnown
    if sample.MemoryKnown {
     resource.memory=sample.MemoryBytes/(1024*1024)
     resource.memoryKnown=true
    }
   }
   m.charts.resources[app.Name]=appendBoundedResource(m.charts.resources[app.Name],resource)
   if ok && sample.LogsComplete {
    m.charts.logs[app.Name]=append(m.charts.logs[app.Name],pickerLogPoint{
     at:now,errors:float64(sample.Errors),warnings:float64(sample.Warnings),
    })
    if len(m.charts.logs[app.Name])>pickerChartHistoryLimit{
     m.charts.logs[app.Name]=m.charts.logs[app.Name][len(m.charts.logs[app.Name])-pickerChartHistoryLimit:]
    }
   }
  }
  return true,nil
 }
 return false,nil
}
func(m pickerModel) externalResourceCells(name string)(string,string){
 sample,ok:=m.externalSamples[name]
 if !ok{return "—","—"}
 cpu,mem:="—","—"
 if sample.CPUKnown{cpu=fmt.Sprintf("%.0f%%",sample.CPUPercent)}
 if sample.MemoryKnown{mem=metricMemory(sample.MemoryBytes,true)}
 return cpu,mem
}
func(m pickerModel) externalMetricDetail(name string)string{
 sample,ok:=m.externalSamples[name]
 if !ok{return "Dynatrace resource metrics: unavailable (metrics permission, ingestion, or query failure)"}
 cpu,mem:=m.externalResourceCells(name)
 items:=[]string{"CPU "+cpu+" of configured limit","memory "+mem}
 if sample.MemoryKnown&&sample.MemoryLimitKnown&&sample.MemoryLimitBytes>0{
  items[1]+=fmt.Sprintf(" / limit %s (%.0f%%)",metricMemory(sample.MemoryLimitBytes,true),100*sample.MemoryBytes/sample.MemoryLimitBytes)
 }
 return strings.Join(items," · ")
}
func(m pickerModel) externalMetricsSummary()string{
 if !m.monitorEnabled{return "Monitor: OFF · M to start"}
 if m.monitorBusy{return loadingLabel("Dynatrace monitor: loading logs and CPU/memory")}
 if m.monitorLastScan.IsZero(){return "Dynatrace monitor: "+m.monitorError}
 description:=fmt.Sprintf("Dynatrace monitor: ON · 30s · %s · %d workloads",m.monitorLastScan.Format("15:04:05"),len(m.externalSamples))
 if m.metricsWarning!=""{description+=" · "+m.metricsWarning}
 return description
}
