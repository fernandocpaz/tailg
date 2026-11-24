// Package logui exposes tailg's real log viewer to alternative log providers.
// It is intentionally small: providers own discovery, transport and authentication;
// tailg owns filtering, rendering, charts, trace navigation and keyboard behavior.
package logui

import (
    "context"
    "errors"
    "time"

    "github.com/fernandocpaz/tailg/internal/core"
    "github.com/fernandocpaz/tailg/internal/ai"
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
    SpanID string
    RequestID string
    HTTPMethod string
    HTTPPath string
    HTTPStatusCode int
    HTTPDuration time.Duration
    HasHTTPDuration bool
    Timestamp time.Time
}
type Config struct {
    Title string
    InitialFilter string
    RegexFilter string // optional raw include-regex, like tailg --include
    UseConfiguredAI bool // opt-in to tailg AI settings; analyses run only by explicit user action
    RecordingPath string
    Capture func([]Event) error // each source event once, never each formatted line
    Namespace string
    Provider string
    Target string
    Sources []Source
    RefreshInterval time.Duration
    BufferLines int
    Color bool
    ShowPod bool
    TraceMarkers bool
    CleanLogs bool
    TimeFile string
    DefaultLookback time.Duration
    TimeRecords func(context.Context,time.Time,time.Time,bool)(TimeResult,error)
    FilterFile string
    // Stream is a blocking source-specific subscription and must exit on
    // context cancellation. It may emit events from a shared polling hub.
    Stream func(context.Context,Source,chan<- Event)error
    Inventory func(context.Context)([]Source,error)
    Search func(context.Context,string)([]Event,error)
    Trace func(context.Context,string)([]Event,error)
    Spans func(context.Context,string,time.Time)(SpanResult,error)
    ErrorLogs func(context.Context,string)([][]Event,error)
    LogDetails func(context.Context,Event)([]Event,error)
}
func ToLogEvent(e Event)core.LogEvent{
    at:=e.Timestamp
    if at.IsZero(){at=time.Now().UTC()}
    return core.LogEvent{
        Workload:e.Workload,Pod:e.Pod,Container:e.Container,
        Message:e.Message,ObservedAt:at,ReceivedAt:time.Now().UTC(),
        Level:e.Level,TraceID:e.TraceID,SpanID:e.SpanID,RequestID:e.RequestID,
        HTTPMethod:e.HTTPMethod,HTTPPath:e.HTTPPath,
        HTTPStatusCode:e.HTTPStatusCode,HTTPDuration:e.HTTPDuration,
        HasHTTPDuration:e.HasHTTPDuration,
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
    if c.RegexFilter!=""{
        patterns,err:=core.CompilePatterns([]string{c.RegexFilter})
        if err!=nil{return err}
        f.Include=patterns
    }
    run:=tui.Config{
        AppName:"DynaR", InitialFilter:c.InitialFilter,RecordingPath:c.RecordingPath,
        TraceMarkers:c.TraceMarkers,CleanLogs:c.CleanLogs,
        TimeFile:c.TimeFile,DefaultLookback:c.DefaultLookback,
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
    if c.UseConfiguredAI {
        aiConfig,err:=ai.EnvConfig(nil)
        if err!=nil{run.AIProblem=err.Error()}
        run.AISettings=aiConfig
        if err==nil && aiConfig.APIKey!="" && aiConfig.Model!="" {
            run.AI,err=ai.New(aiConfig)
            if err!=nil{run.AIProblem=err.Error()}
        }
    }
    if c.Capture!=nil{
        run.CaptureLogs=func(blocks [][]core.LogRecord)error{
            events:=make([]Event,0,len(blocks))
            for _,block:=range blocks{
                if len(block)==0{continue}
                e:=block[0].Event
                events=append(events,Event{
                    Workload:e.Workload,Pod:e.Pod,Container:e.Container,
                    Message:e.Message,Level:e.Level,TraceID:e.TraceID,SpanID:e.SpanID,
                    RequestID:e.RequestID,Timestamp:e.ObservedAt,
                    HTTPMethod:e.HTTPMethod,HTTPPath:e.HTTPPath,
                    HTTPStatusCode:e.HTTPStatusCode,HTTPDuration:e.HTTPDuration,
                    HasHTTPDuration:e.HasHTTPDuration,
                })
            }
            if len(events)==0{return nil}
            return c.Capture(events)
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
    if c.TimeRecords!=nil{run.TimeRecords=func(ctx context.Context,from,to time.Time,force bool)(tui.TimeResult,error){
        result,err:=c.TimeRecords(ctx,from,to,force)
        return tui.TimeResult{Records:records(c,result.Events),Warning:result.Warning},err
    }}
    if c.Spans!=nil{run.Spans=c.Spans}
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
            found,err:=c.LogDetails(ctx,Event{Workload:e.Workload,Pod:e.Pod,Container:e.Container,Message:e.Message,Level:e.Level,TraceID:e.TraceID,SpanID:e.SpanID,RequestID:e.RequestID,Timestamp:e.ObservedAt,
                HTTPMethod:e.HTTPMethod,HTTPPath:e.HTTPPath,
                HTTPStatusCode:e.HTTPStatusCode,HTTPDuration:e.HTTPDuration,
                HasHTTPDuration:e.HasHTTPDuration})
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
    Pods []string
    ScopeKind, ScopeValue string
    MonitorEnabled bool
    Quit bool
    Record bool
    RecordFrom time.Time
}
func PickApps(apps []App,source,namespace string)(Selection,error){
 return PickAppsWithMonitor(apps,source,namespace,nil,false)
}
func PickAppsWithMonitor(apps []App,source,namespace string,monitor Monitor,start bool)(Selection,error){
    return PickAppsWithScope(context.Background(),apps,source,namespace,monitor,start,nil,"")
}
// ScopeDiscovery returns provider-discovered names in display order, with an optional warning.
type ScopeDiscovery func(context.Context,string)([]string,string,error)
func PickAppsWithScope(ctx context.Context,apps []App,source,namespace string,monitor Monitor,start bool,discover ScopeDiscovery,notice string)(Selection,error){
    choices:=make([]core.AppChoice,0,len(apps))
    for _,app:=range apps{
        choices=append(choices,core.AppChoice{Name:app.Name,Namespace:namespace,Pods:append([]string(nil),app.Pods...),LastLogAt:app.LastSeen,Ready:"-",Phases:"Logs"})
    }
    result,err:=tui.PickExternalAppsWithScope(ctx,choices,source,namespace,convertMonitor(monitor),start,tui.ExternalScopeDiscovery(discover),notice)
    if err!=nil{return Selection{},err}
    if result.Action==tui.PickerQuit{return Selection{Quit:true},nil}
    selected:=Selection{ScopeKind:result.ScopeKind,ScopeValue:result.ScopeValue,MonitorEnabled:result.MonitorEnabled,Record:result.LogToFile,RecordFrom:result.CaptureFrom,Pods:append([]string(nil),result.Pods...)}
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

// ErrorBlocks preserves multi-record exception continuations using the same
// rule as tailg's selected-log detail and F3 exception grouping.
func ErrorBlocks(events []Event)[][]Event{
    var blocks [][]Event
    var current []Event
    for _,event:=range events{
        ev:=ToLogEvent(event)
        level:=core.ParseLogFields(ev).Level
        severe:=level=="ERR"||level=="FTL"
        continuation:=core.IsLogContinuation(event.Message)
        if len(current)>0&&continuation&&event.Pod==current[0].Pod&&event.Container==current[0].Container{
            current=append(current,event)
            continue
        }
        if len(current)>0 {blocks=append(blocks,current);current=nil}
        if severe {current=[]Event{event}}
    }
    if len(current)>0{blocks=append(blocks,current)}
    return blocks
}
