// Package logui exposes tailg's real log viewer to alternative log providers.
// It is intentionally small: providers own discovery, transport and authentication;
// tailg owns filtering, rendering, charts, trace navigation and keyboard behavior.
package logui

import (
    "context"
    "errors"
    "time"

    "github.com/fernandocpaz/tailg/internal/core"
    "github.com/fernandocpaz/tailg/internal/tui"
)

type Source struct {
    Workload string
    Pod string
    Container string
}
func(s Source) Key()string{return s.Pod+"\x00"+s.Container}

type Event struct{
    Workload string
    Pod string
    Container string
    Message string
    Level string
    TraceID string
    RequestID string
    Timestamp time.Time
}
type Config struct {
    Title string
    InitialFilter string
    Namespace string
    Provider string
    Target string
    Sources []Source
    RefreshInterval time.Duration
    BufferLines int
    Color bool
    ShowPod bool
    FilterFile string
    // Stream is a blocking source-specific subscription and must exit on
    // context cancellation. It may emit events from a shared polling hub.
    Stream func(context.Context,Source,chan<- Event)error
    Inventory func(context.Context)([]Source,error)
    Search func(context.Context,string)([]Event,error)
    Trace func(context.Context,string)([]Event,error)
    ErrorLogs func(context.Context,string)([][]Event,error)
    LogDetails func(context.Context,Event)([]Event,error)
}
func ToLogEvent(e Event)core.LogEvent{
    at:=e.Timestamp
    if at.IsZero(){at=time.Now().UTC()}
    return core.LogEvent{
        Workload:e.Workload,Pod:e.Pod,Container:e.Container,
        Message:e.Message,ObservedAt:at,ReceivedAt:time.Now().UTC(),
        Level:e.Level,TraceID:e.TraceID,RequestID:e.RequestID,
    }
}
func toSource(item core.InventoryItem)Source{return Source{Pod:item.Pod,Container:item.Container}}
func inventory(items []Source)[]core.InventoryItem{
    out:=make([]core.InventoryItem,0,len(items))
    seen:=make(map[string]bool,len(items))
    for _,s:=range items{
        if s.Pod==""||s.Container==""||seen[s.Key()]{continue}
        out=append(out,core.InventoryItem{Workload:s.Workload,Pod:s.Pod,Container:s.Container})
        seen[s.Key()]=true
    }
    return out
}
func formatter(c Config) core.Formatter{return core.Formatter{Color:c.Color,ShowPod:c.ShowPod}}
func records(c Config,items []Event)[]core.LogRecord{
    f:=formatter(c)
    var out []core.LogRecord
    for _,item:=range items{
        ev:=ToLogEvent(item)
        out=append(out,core.RecordsForEvent(f,ev,true)...)
    }
    return out
}
func Run(ctx context.Context,c Config)error{
    if c.Stream==nil{return errors.New("log provider Stream is required")}
    f:=formatter(c)
    run:=tui.Config{
        AppName:"DynaR", InitialFilter:c.InitialFilter,
        Title:c.Title,Namespace:c.Namespace,KubeContext:c.Provider,Target:c.Target,
        Items:inventory(c.Sources),Formatter:f,
        RefreshInterval:c.RefreshInterval,BufferLines:c.BufferLines,
        FilterFile:c.FilterFile,
    }
    run.Stream=func(ctx context.Context,item core.InventoryItem,cursor *core.LogCursor,out chan<- core.LogEvent)error{
        input:=make(chan Event,256)
        done:=make(chan error,1)
        go func(){
            defer close(input)
            done<-c.Stream(ctx,toSource(item),input)
        }()
        replay:=cursor.Resume()
        for {
            select {
            case <-ctx.Done():
                return ctx.Err()
            case item,ok:=<-input:
                if !ok{return <-done}
                ev:=ToLogEvent(item)
                if replay.Duplicate(ev){ev.Replayed=true}
                select{
                case out<-ev:
                    if !ev.Replayed{cursor.Observe(ev)}
                case <-ctx.Done():return ctx.Err()
                }
            }
        }
    }
    if c.Inventory!=nil{
        run.Inventory=func(ctx context.Context)([]core.InventoryItem,error){
            items,err:=c.Inventory(ctx)
            return inventory(items),err
        }
    }
    if c.Search!=nil{
        run.SearchRecords=func(ctx context.Context,text string)([]core.LogRecord,error){
            found,err:=c.Search(ctx,text)
            if err!=nil{return nil,err}
            return records(c,found),nil
        }
    }
    if c.Trace!=nil{
        run.Trace=func(ctx context.Context,id string)([]core.LogRecord,error){
            found,err:=c.Trace(ctx,id)
            if err!=nil{return nil,err}
            return records(c,found),nil
        }
    }
    if c.ErrorLogs!=nil{
        run.ErrorLogs=func(ctx context.Context,pod string)([][]core.LogEvent,error){
            blocks,err:=c.ErrorLogs(ctx,pod)
            if err!=nil{return nil,err}
            out:=make([][]core.LogEvent,0,len(blocks))
            for _,block:=range blocks{
                events:=make([]core.LogEvent,0,len(block))
                for _,entry:=range block{events=append(events,ToLogEvent(entry))}
                out=append(out,events)
            }
            return out,nil
        }
    }
    if c.LogDetails!=nil{
        run.LogDetails=func(ctx context.Context,e core.LogEvent)([]core.LogEvent,error){
            found,err:=c.LogDetails(ctx,Event{Workload:e.Workload,Pod:e.Pod,Container:e.Container,Message:e.Message,Level:e.Level,TraceID:e.TraceID,RequestID:e.RequestID,Timestamp:e.ObservedAt})
            if err!=nil{return nil,err}
            out:=make([]core.LogEvent,0,len(found))
            for _,entry:=range found{out=append(out,ToLogEvent(entry))}
            return out,nil
        }
    }
    if run.RefreshInterval<=0{run.RefreshInterval=3*time.Second}
    if run.BufferLines<=0{run.BufferLines=50000}
    if run.Title==""{run.Title="DynaR | "+c.Namespace}
    return tui.Run(ctx,run)
}

// App represents a discovered workload in the shared tailg picker.
type App struct {
    Name string
    Pods []string
    LastSeen time.Time
}
type Selection struct {
    Workloads []string
    Quit bool
}
func PickApps(apps []App,source,namespace string)(Selection,error){
    choices:=make([]core.AppChoice,0,len(apps))
    for _,app:=range apps{
        choices=append(choices,core.AppChoice{Name:app.Name,Namespace:namespace,Pods:append([]string(nil),app.Pods...),LastLogAt:app.LastSeen,Ready:"-",Phases:"Logs"})
    }
    result,err:=tui.PickExternalApps(choices,source,namespace)
    if err!=nil{return Selection{},err}
    if result.Action==tui.PickerQuit{return Selection{Quit:true},nil}
    selected:=Selection{}
    for _,choice:=range result.Apps{selected.Workloads=append(selected.Workloads,choice.Name)}
    return selected,nil
}

// MatchEvents applies the exact same structured query syntax used by tailg's
// local and full-history search (level:, service:, pod:, trace:, duration:, ...).
func MatchEvents(query string,events []Event)([]Event,error){
    compiled,err:=core.CompileLogQuery(query)
    if err!=nil{return nil,err}
    f:=core.Formatter{Color:false,ShowPod:false}
    var matching []Event
    for _,e:=range events{
        records:=core.RecordsForEvent(f,ToLogEvent(e),false)
        for _,r:=range records{
            if compiled.Matches(r){matching=append(matching,e);break}
        }
    }
    return matching,nil
}
