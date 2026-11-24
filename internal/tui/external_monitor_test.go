package tui

import (
 "context"
 "errors"
 "strings"
 "testing"
 "time"

 tea "github.com/charmbracelet/bubbletea"
 "github.com/fernandocpaz/tailg/internal/core"
)
func TestExternalMonitorNeverUsesKubectlForMOrF9(t *testing.T){
 calls:=0
 m:=newPickerModel([]core.AppChoice{{Name:"encounter",Pods:[]string{"encounter-a"}}},1,"qa","apollo",nil)
 m.external=true
 m.refreshContext=context.Background()
 m.externalMonitor=func(context.Context)(ExternalMonitorSnapshot,error){
  calls++
  return ExternalMonitorSnapshot{At:time.Now(),Workloads:[]ExternalWorkloadSample{{
   Name:"encounter",CPUPercent:83,CPUKnown:true,MemoryBytes:256*1024*1024,MemoryKnown:true,
   Lines:12,Warnings:2,Errors:0,LogsComplete:true,
  }}},nil
 }
 next,cmd:=m.Update(tea.KeyMsg{Type:tea.KeyRunes,Runes:[]rune("m")})
 m=next.(pickerModel)
 if cmd==nil||!m.monitorEnabled{t.Fatal("M should enable provider monitoring")}
 handled,_:=m.updateExternalMonitor(externalMonitorResultMsg{generation:m.monitorGeneration,
  snapshot:ExternalMonitorSnapshot{At:time.Now(),Workloads:[]ExternalWorkloadSample{{
   Name:"encounter",CPUPercent:83,CPUKnown:true,MemoryBytes:256*1024*1024,MemoryKnown:true,
   Lines:12,Warnings:2,LogsComplete:true,
  }}}})
 if !handled||m.monitorApps["encounter"].kind!=podHealthWarning{t.Fatalf("expected warning state: %+v",m.monitorApps)}
 cpu,mem:=m.externalResourceCells("encounter")
 if cpu!="83%"||mem!="256Mi"{t.Fatalf("resource cells %q %q",cpu,mem)}
 if len(m.charts.resources["encounter"])!=1{t.Fatal("F9 resources missing")}
 if calls!=0{t.Fatal("callback must be deferred to tea command")}
}
func TestExternalMonitorQueryFailuresAreUnknown(t *testing.T){
 m:=newPickerModel([]core.AppChoice{{Name:"patient"}},1,"qa","apollo",nil)
 m.external=true;m.refreshContext=context.Background()
 m.externalMonitor=func(context.Context)(ExternalMonitorSnapshot,error){return ExternalMonitorSnapshot{},errors.New("access denied")}
 m.monitorEnabled=true;m.monitorGeneration=2
 handled,_:=m.updateExternalMonitor(externalMonitorResultMsg{generation:2,err:errors.New("access denied")})
 if !handled||m.monitorApps["patient"].kind!=podHealthUnknown||!strings.Contains(m.monitorError,"access denied"){
  t.Fatal("failed metrics read must not show healthy")
 }
}

func TestExternalAutoStartUpdatesRealPickerState(t *testing.T){
 m:=newPickerModel([]core.AppChoice{{Name:"encounter",Pods:[]string{"encounter-a"}}},1,"qa","apollo",nil)
 m.external=true
 m.externalMonitorOnStart=true
 m.refreshContext=context.Background()
 m.externalMonitor=func(context.Context)(ExternalMonitorSnapshot,error){
  return ExternalMonitorSnapshot{At:time.Now()},nil
 }
 cmd:=m.Init()
 if cmd==nil{t.Fatal("--monitor must produce a startup event")}
 initial:=cmd()
 if _,ok:=initial.(externalStartMonitorMsg);!ok{t.Fatalf("wrong startup event %T",initial)}
 updated,follow:=m.Update(initial)
 next:=updated.(pickerModel)
 if !next.monitorEnabled||follow==nil{t.Fatal("--monitor did not enable the actual live picker")}
}
