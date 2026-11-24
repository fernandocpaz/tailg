package logui

import (
 "context"
 "time"
 "github.com/fernandocpaz/tailg/internal/tui"
)

// CPUPercent is % of configured workload CPU limit; unknown is not zero.
type WorkloadSample struct {
 Workload string
 CPUPercent float64
 CPUKnown bool
 MemoryBytes float64
 MemoryKnown bool
 MemoryLimitBytes float64
 MemoryLimitKnown bool
 Lines,Warnings,Errors int
 LogsComplete bool
}
type MonitorSnapshot struct {
 At time.Time
 Workloads []WorkloadSample
 Warning string
}
type Monitor func(context.Context)(MonitorSnapshot,error)

func convertMonitor(callback Monitor)tui.ExternalMonitor{
 if callback==nil{return nil}
 return func(ctx context.Context)(tui.ExternalMonitorSnapshot,error){
  sample,err:=callback(ctx)
  if err!=nil{return tui.ExternalMonitorSnapshot{},err}
  result:=tui.ExternalMonitorSnapshot{At:sample.At,Warning:sample.Warning}
  for _,item:=range sample.Workloads{
   result.Workloads=append(result.Workloads,tui.ExternalWorkloadSample{
    Name:item.Workload,CPUPercent:item.CPUPercent,CPUKnown:item.CPUKnown,
    MemoryBytes:item.MemoryBytes,MemoryKnown:item.MemoryKnown,
    MemoryLimitBytes:item.MemoryLimitBytes,MemoryLimitKnown:item.MemoryLimitKnown,
    Lines:item.Lines,Warnings:item.Warnings,Errors:item.Errors,LogsComplete:item.LogsComplete,
   })
  }
  return result,nil
 }
}
