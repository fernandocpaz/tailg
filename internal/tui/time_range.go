package tui

import (
 "context"
 "encoding/json"
 "fmt"
 "os"
 "sort"
 "strings"
 "time"

 "github.com/charmbracelet/bubbles/textinput"
 tea "github.com/charmbracelet/bubbletea"
 "github.com/fernandocpaz/tailg/internal/core"
)

// TimeRange is a relative rolling interval or an exact absolute interval.
// The same value is shared between processes, independently of text filters.
type TimeRange struct{
 Lookback time.Duration
 Start,End time.Time
}
const maxTimeRange=31*24*time.Hour
func defaultLookback(d time.Duration)time.Duration{
 if d<=0{return 30*time.Minute}
 return min(d,maxTimeRange)
}
func(r TimeRange)Valid()bool{
 if r.Lookback>0{return r.Start.IsZero()&&r.End.IsZero()&&r.Lookback<=maxTimeRange}
 return r.Lookback==0&&!r.Start.IsZero()&&r.End.After(r.Start)&&r.End.Sub(r.Start)<=maxTimeRange
}
func(r TimeRange)Bounds(now time.Time)(time.Time,time.Time){
 if r.Lookback>0{return now.Add(-r.Lookback),now}
 return r.Start,r.End
}
func(r TimeRange)Shift(now time.Time,direction int)TimeRange{
 from,to:=r.Bounds(now)
 width:=to.Sub(from)
 delta:=time.Duration(direction)*width
 from,to=from.Add(delta),to.Add(delta)
 if to.After(now){to=now;from=to.Add(-width)}
 return TimeRange{Start:from,End:to}
}
func(r TimeRange)Zoom(now time.Time,in bool)TimeRange{
 from,to:=r.Bounds(now)
 width:=to.Sub(from)
 if in{width=max(time.Second,width/2)}else{width=min(maxTimeRange,width*2)}
 center:=from.Add(to.Sub(from)/2)
 from=center.Add(-width/2);to=from.Add(width)
 if to.After(now){to=now;from=to.Add(-width)}
 return TimeRange{Start:from,End:to}
}
func(r TimeRange)equal(other TimeRange)bool{return r.Lookback==other.Lookback&&r.Start.Equal(other.Start)&&r.End.Equal(other.End)}
func(r TimeRange)label()string{
 if r.Lookback>0{return "Last "+compactDuration(r.Lookback)+" LIVE"}
 return r.Start.Local().Format("Jan 02 15:04:05")+" – "+r.End.Local().Format("Jan 02 15:04:05")+" FIXED"
}
func compactDuration(d time.Duration)string{
 if d%(24*time.Hour)==0{return fmt.Sprintf("%dd",d/(24*time.Hour))}
 if d%time.Hour==0{return fmt.Sprintf("%dh",d/time.Hour)}
 if d%time.Minute==0{return fmt.Sprintf("%dm",d/time.Minute)}
 return d.String()
}

type TimeResult struct{Records []core.LogRecord;Warning string}
type timeRangeMsg struct{generation int;from,to time.Time;result TimeResult;err error}
type timeViewState struct{
 selection TimeRange
 revision sharedRevision
 live *core.FilterState
 active,loading,complete bool
 generation int
 initial tea.Cmd
 searches *searchController
 lastTrim time.Time
 loadedFrom,loadedTo time.Time
 warning string
 dialog timeDialog
}
type timeDialog struct{
 open,custom bool
 index,field int
 from,to textinput.Model
 errorText string
 anchor time.Time
}

const timeRangePrefix="logui-time-v1:"
func readTimeRange(path string)(TimeRange,sharedRevision,bool){
 if path==""{return TimeRange{},sharedRevision{},false}
 data,err:=os.ReadFile(path)
 if err!=nil||len(data)>4096{return TimeRange{},sharedRevision{},false}
 payload,revision,ok:=decodeSharedValue(timeRangePrefix,data)
 var r TimeRange
 if !ok||json.Unmarshal([]byte(payload),&r)!=nil||!r.Valid(){return TimeRange{},sharedRevision{},false}
 return r,revision,true
}
// ReadSharedTimeRange exposes the synchronized selection to provider diagnostics.
func ReadSharedTimeRange(path string)(TimeRange,bool){r,_,ok:=readTimeRange(path);return r,ok}
func writeTimeRange(path string,r TimeRange,last sharedRevision)(sharedRevision,error){
 if !r.Valid(){return last,fmt.Errorf("choose a valid time interval (up to 31 days)")}
 if path==""{return nextSharedRevision(last),nil}
 payload,err:=json.Marshal(r)
 if err!=nil{return last,err}
 var revision sharedRevision
 err=withSharedFileLock(path,func()error{
  if _,current,ok:=readTimeRange(path);ok&&current.newerThan(last){last=current}
  revision=nextSharedRevision(last)
  return os.WriteFile(path,encodeSharedValue(timeRangePrefix,string(payload),revision),0600)
 })
 return revision,err
}

func filterTimeRecords(records []core.LogRecord,r TimeRange,now time.Time)[]core.LogRecord{
 from,to:=r.Bounds(now)
 out:=make([]core.LogRecord,0,len(records))
 for _,record:=range records{
  at:=record.Event.ObservedAt
  if !at.IsZero()&&!at.Before(from)&&at.Before(to){out=append(out,record)}
 }
 return out
}
type timeOccurrence struct{traceOccurrence;trace,span string}
func timeKey(record core.LogRecord)timeOccurrence{return timeOccurrence{traceKey(record),record.Fields.TraceID,record.Fields.SpanID}}
// Merge snapshots by occurrence count, preserving repeated identical messages.
func mergeTimeRecords(primary,extra []core.LogRecord)[]core.LogRecord{
 result:=append([]core.LogRecord(nil),primary...)
 counts:=make(map[timeOccurrence]int,len(primary))
 for _,row:=range primary{counts[timeKey(row)]++}
 for _,row:=range extra{
  key:=timeKey(row)
  if counts[key]>0{counts[key]--;continue}
  result=append(result,row)
 }
 sort.SliceStable(result,func(i,j int)bool{return result[i].Event.ObservedAt.Before(result[j].Event.ObservedAt)})
 return result
}
func(m *model)timeSnapshot(records []core.LogRecord)*core.FilterState{
 limit:=m.config.BufferLines
 if limit<=0{limit=core.DefaultBufferLines}
 next:=core.NewFilterState(limit)
 next.SetHideRoutineNoise(m.state.HideRoutineNoise())
 next.SetMatchesOnly(m.state.MatchesOnly())
 next.SetFilter(m.input.Value())
 from,to:=m.timeState.selection.Bounds(time.Now())
 if m.timeState.selection.Lookback>0{to=time.Time{}}
 next.SetTimeWindow(from,to,true)
 if len(records)>limit{
  records=records[len(records)-limit:]
  m.timeState.complete=false
  note:=fmt.Sprintf("Display limited to newest %d rows",limit)
  if m.timeState.warning!=""{m.timeState.warning+="; "}
  m.timeState.warning+=note
 }
 next.AppendRecords(records...)
 return next
}
func(m *model)applyTimeRange(r TimeRange,publish bool)tea.Cmd{
 s:=&m.timeState
 if m.config.TimeRecords==nil||!r.Valid(){m.notice="Invalid time range (maximum 31 days)";return nil}
 if publish{
  revision,err:=writeTimeRange(m.config.TimeFile,r,s.revision)
  if err!=nil{m.notice="Time synchronization failed: "+err.Error();return nil}
  s.revision=revision
 }
 sameRange:=s.active&&s.selection.equal(r)
 if s.live==nil{s.live=m.state}
 if s.searches==nil{s.searches=&searchController{}}
 parent:=m.ctx;if parent==nil{parent=context.Background()}
 ctx:=s.searches.start(parent)
 s.generation++
 generation:=s.generation
 now:=time.Now()
 anchor:=now
 committed:=time.Unix(0,s.revision.timestamp)
 if !committed.After(now)&&now.Sub(committed)<5*time.Second{anchor=committed}
 from,to:=r.Bounds(anchor)
 // A contained fixed interval can be served directly from a complete
 // previously fetched snapshot. Otherwise fetch only on committed changes.
 reuse:=s.active&&!sameRange&&s.complete&&s.selection.Lookback==0&&r.Lookback==0&&!from.Before(s.loadedFrom)&&!to.After(s.loadedTo)
 var previous []core.LogRecord
 if s.active{previous=filterTimeRecords(m.state.AllRecords(),r,now)}
 seed:=mergeTimeRecords(previous,filterTimeRecords(s.live.AllRecords(),r,now))
 s.selection=r;s.active=true;s.loading=!reuse;s.complete=reuse;s.warning=""
 s.loadedFrom,s.loadedTo=from,to
 s.lastTrim=now
 m.generation++;m.cancelSearch();m.searching=false
 m.closeDetail()
 if m.traceOpen{m.closeTrace()}
 if m.errorLogs.open{m.closeErrorLogs()}
 m.issueOpen=false;m.analyticsOpen=false;m.securityRadarOpen=false;m.freshnessOpen=false;m.heartbeatOpen=false;m.resourceOpen=false;m.replicas.open=false
 m.state=m.timeSnapshot(seed)
 m.followsLive=r.Lookback>0;m.selected=m.state.VisibleCount()-1;m.scroll=0;m.notice=""
 if reuse{return nil}
 load:=m.config.TimeRecords
 return func()tea.Msg{
  limited,cancel:=context.WithTimeout(ctx,90*time.Second);defer cancel()
  result,err:=load(limited,from,to,sameRange)
  if ctx.Err()!=nil{return nil}
  return timeRangeMsg{generation:generation,from:from,to:to,result:result,err:err}
 }
}
func(m model)receiveTimeRange(msg timeRangeMsg)model{
 s:=&m.timeState
 if !s.active||msg.generation!=s.generation{return m}
 s.loading=false
 s.complete=msg.err==nil&&msg.result.Warning==""
 s.warning=msg.result.Warning
 if msg.err!=nil{s.warning="Incomplete: "+msg.err.Error()}
 now:=time.Now()
 merged:=mergeTimeRecords(filterTimeRecords(msg.result.Records,s.selection,now),filterTimeRecords(m.state.AllRecords(),s.selection,now))
 m.state=m.timeSnapshot(merged)
 // Do not disturb text filters edited while the request was running.
 m.selected=m.state.VisibleCount()-1;m.scroll=0
 if strings.TrimSpace(m.input.Value())!=""&&m.state.MatchCount()>0{m.selected=m.state.MatchIndex();m.followsLive=false}
 return m
}
func(m *model)syncTimeRange(now time.Time)tea.Cmd{
 if m.config.TimeRecords==nil{return nil}
 s:=&m.timeState
 if r,revision,ok:=readTimeRange(m.config.TimeFile);ok&&revision.newerThan(s.revision){
  s.revision=revision
  return m.applyTimeRange(r,false)
 }
 // Age out rolling windows at a coarse cadence, not on every log or redraw.
 if s.selection.Lookback>0&&now.Sub(s.lastTrim)>=5*time.Second{
  from,_:=s.selection.Bounds(now)
  m.state.SetTimeWindow(from,time.Time{},s.active)
  s.lastTrim=now
  if m.followsLive{m.selected=m.state.VisibleCount()-1}else{m.selected=min(m.selected,m.state.VisibleCount()-1)}
 }
 return nil
}
func(m model)renderTimeBar()string{
 if m.config.TimeRecords==nil{return ""}
 s:=m.timeState
 status:=s.selection.label()
 if s.loading{status+=" | loading…"}else if s.warning!=""{status+=" | "+s.warning}
 if m.config.TimeFile!=""{status+=" | synced"}
 return "\n"+truncate(renderWithColor(headerStyle,"F12 TIME ",m.config.Formatter.Color)+status,m.width)
}

var timePresets=[]time.Duration{5*time.Minute,15*time.Minute,30*time.Minute,time.Hour,2*time.Hour,6*time.Hour,12*time.Hour,24*time.Hour,7*24*time.Hour}
func(m *model)openTimeDialog(){
 d:=timeDialog{open:true}
 for i,p:=range timePresets{if p==m.timeState.selection.Lookback{d.index=i}}
 if record,ok:=m.state.SelectedRecord(m.selected);ok{d.anchor=record.Event.ObservedAt}
 m.timeState.dialog=d
}
const timeInputLayout="2006-01-02 15:04:05"
func parseTimeInput(value string,loc *time.Location)(time.Time,error){
 value=strings.TrimSpace(value)
 if t,err:=time.Parse(time.RFC3339Nano,value);err==nil{return t,nil}
 for _,layout:=range []string{timeInputLayout,"2006-01-02 15:04","2006-01-02"}{
  if t,err:=time.ParseInLocation(layout,value,loc);err==nil{return t,nil}
 }
 return time.Time{},fmt.Errorf("use YYYY-MM-DD HH:MM[:SS] or RFC3339")
}
func(m *model)customTimeDialog(){
 d:=&m.timeState.dialog
 from,to:=m.timeState.selection.Bounds(time.Now())
 d.custom=true;d.field=0;d.errorText=""
 d.from=textinput.New();d.from.Prompt="";d.from.CharLimit=64;d.from.SetValue(from.Local().Format(timeInputLayout));d.from.Focus();d.from.CursorEnd()
 d.to=textinput.New();d.to.Prompt="";d.to.CharLimit=64;d.to.SetValue(to.Local().Format(timeInputLayout));d.to.Blur()
}
func(m model)renderTimeDialog()string{
 d:=m.timeState.dialog
 title:="Time range • all DynaR windows"
 if d.custom{
  arrow:=[]string{"  ","  "};arrow[d.field]="> "
  lines:=[]string{"Exact interval (local time; offsets accepted)",arrow[0]+"From: "+d.from.View(),arrow[1]+"To:   "+d.to.View(),"YYYY-MM-DD HH:MM[:SS] or RFC3339"}
  if d.errorText!=""{lines=append(lines,d.errorText)}
  for i:=range lines{lines[i]=truncate(lines[i],m.width)}
  return m.panel(title,strings.Join(lines,"\n"),"Tab field | Enter apply | Esc back")
 }
 lines:=[]string{truncate("Current: "+m.timeState.selection.label(),m.width),truncate("Rolling ranges (choose 1–9 or Enter)",m.width)}
 height:=max(1,m.height-8)
 start:=max(0,d.index-height+1)
 for i:=start;i<min(len(timePresets),start+height);i++{
  marker:="  ";if i==d.index{marker="> "}
  lines=append(lines,truncate(fmt.Sprintf("%s%d  Last %s",marker,i+1,compactDuration(timePresets[i])),m.width))
 }
 lines=append(lines,truncate("C Custom | L Live default | F Freeze | A ±5m at log",m.width),truncate("← Previous | → Next | +/- Zoom | R Reload",m.width),truncate("T Today | Y Yesterday",m.width))
 if d.errorText!=""{lines[0]=truncate(d.errorText,m.width)}
 return m.panel(title,strings.Join(lines,"\n"),"↑↓ choose | Enter apply | Esc cancel")
}
func(m model)updateTimeDialog(msg tea.KeyMsg)(tea.Model,tea.Cmd){
 d:=&m.timeState.dialog
 key:=msg.String()
 if d.custom{
  switch key{
  case "esc":d.custom=false;return m,nil
  case "tab","shift+tab","up","down":
   d.field=1-d.field
   if d.field==0{d.from.Focus();d.to.Blur()}else{d.from.Blur();d.to.Focus()}
   return m,nil
  case "enter":
   from,err:=parseTimeInput(d.from.Value(),time.Local);if err!=nil{d.errorText="From: "+err.Error();return m,nil}
   to,err:=parseTimeInput(d.to.Value(),time.Local);if err!=nil{d.errorText="To: "+err.Error();return m,nil}
   r:=TimeRange{Start:from,End:to}
   if !r.Valid()||to.After(time.Now().Add(time.Minute)){d.errorText="End must follow start, not be in the future; max 31 days";return m,nil}
   d.open=false;cmd:=m.applyTimeRange(r,true);return m,cmd
  }
  var cmd tea.Cmd
  if d.field==0{d.from,cmd=d.from.Update(msg)}else{d.to,cmd=d.to.Update(msg)}
  return m,cmd
 }
 now:=time.Now()
 current:=m.timeState.selection
 var r TimeRange
 switch key{
 case "esc","f12":d.open=false;return m,nil
 case "up":d.index=max(0,d.index-1);return m,nil
 case "down":d.index=min(len(timePresets)-1,d.index+1);return m,nil
 case "c":m.customTimeDialog();return m,nil
 case "enter":r=TimeRange{Lookback:timePresets[d.index]}
 case "l":r=TimeRange{Lookback:defaultLookback(m.config.DefaultLookback)}
 case "f":from,to:=current.Bounds(now);r=TimeRange{Start:from,End:to}
 case "t","y":
  local:=now.Local();midnight:=time.Date(local.Year(),local.Month(),local.Day(),0,0,0,0,local.Location())
  if key=="t"{r=TimeRange{Start:midnight,End:now}}else{r=TimeRange{Start:midnight.AddDate(0,0,-1),End:midnight}}
 case "left":r=current.Shift(now,-1)
 case "right":r=current.Shift(now,1)
 case "+","=":r=current.Zoom(now,true)
 case "-":r=current.Zoom(now,false)
 case "a":
  if d.anchor.IsZero(){d.errorText="Select a timestamped log first";return m,nil}
  end:=minTimeRangeEnd(now,d.anchor.Add(5*time.Minute));r=TimeRange{Start:d.anchor.Add(-5*time.Minute),End:end}
 case "r":r=current;m.timeState.complete=false
 default:
  if len(key)!=1||key[0]<'1'||key[0]>'9'{return m,nil}
  r=TimeRange{Lookback:timePresets[int(key[0]-'1')]}
 }
 d.open=false
 cmd:=m.applyTimeRange(r,true)
 return m,cmd
}
func minTimeRangeEnd(a,b time.Time)time.Time{if a.Before(b){return a};return b}
