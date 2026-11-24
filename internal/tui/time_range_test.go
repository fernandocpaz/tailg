package tui

import (
 "context"
 "path/filepath"
 "testing"
 "time"
 "github.com/fernandocpaz/tailg/internal/core"
)
func TestSharedTimeRangeAndStaleResults(t *testing.T){
 path:=filepath.Join(t.TempDir(),"time")
 now:=time.Now().UTC()
 absolute:=TimeRange{Start:now.Add(-time.Hour),End:now.Add(-30*time.Minute)}
 rev,err:=writeTimeRange(path,absolute,sharedRevision{});if err!=nil{t.Fatal(err)}
 got,gotRev,ok:=readTimeRange(path)
 if !ok||!got.equal(absolute)||gotRev!=rev{t.Fatal("shared interval did not round trip")}
 rolling:=TimeRange{Lookback:15*time.Minute}
 newer,err:=writeTimeRange(path,rolling,sharedRevision{});if err!=nil||!newer.newerThan(rev){t.Fatal("shared revision did not advance")}
 m:=workflowModel()
 m.config.TimeFile=path;m.config.DefaultLookback=30*time.Minute
 m.config.TimeRecords=func(context.Context,time.Time,time.Time)(TimeResult,error){return TimeResult{},nil}
 m.timeState.selection=TimeRange{Lookback:30*time.Minute}
 if cmd:=m.syncTimeRange(now);cmd==nil||!m.timeState.selection.equal(rolling){t.Fatal("other pane did not apply interval")}
 oldGen:=m.timeState.generation
 m.applyTimeRange(absolute,false)
 m=m.receiveTimeRange(timeRangeMsg{generation:oldGen})
 if !m.timeState.loading||!m.timeState.selection.equal(absolute){t.Fatal("stale result replaced current interval")}
}
func TestTimeSnapshotDoesNotLoseLiveBuffer(t *testing.T){
 m:=workflowModel()
 now:=time.Now()
 m.config.TimeRecords=func(context.Context,time.Time,time.Time)(TimeResult,error){return TimeResult{},nil}
 m.timeState.selection=TimeRange{Lookback:30*time.Minute}
 live:=m.state
 old:=core.LogEvent{Message:"[12:00:00 INF] old",ObservedAt:now.Add(-2*time.Hour)}
 current:=core.LogEvent{Message:"[12:00:00 INF] current",ObservedAt:now.Add(-time.Second)}
 live.AppendRecords(core.RecordsForEvent(m.config.Formatter,old,true)...)
 live.AppendRecords(core.RecordsForEvent(m.config.Formatter,current,true)...)
 r:=TimeRange{Start:now.Add(-3*time.Hour),End:now.Add(-time.Hour)}
 m.applyTimeRange(r,false)
 if m.state.VisibleCount()!=1||m.state.Selected(0)==""||m.state==live{t.Fatal("fixed interval not isolated")}
 m.processLogBatch([]core.LogEvent{{Message:"[12:00:00 INF] later",ObservedAt:now}})
 if m.state.VisibleCount()!=1||len(live.AllRecords())!=3{t.Fatal("live arrivals polluted fixed view or were lost")}
 m=m.receiveTimeRange(timeRangeMsg{generation:m.timeState.generation,from:r.Start,to:r.End,result:TimeResult{Records:core.RecordsForEvent(m.config.Formatter,old,true)}})
 if m.state.VisibleCount()!=1{t.Fatal("overlapping snapshots duplicated a log")}
 m.state.SetFilter("old")
 if m.state.MatchCount()!=1{t.Fatal("local historical search failed")}
}
func TestTimeRangeParsingAndNavigation(t *testing.T){
 at,err:=parseTimeInput("2026-11-24T08:00:00-05:00",time.UTC)
 if err!=nil||at.UTC().Hour()!=13{t.Fatal("offset lost")}
 r:=TimeRange{Lookback:15*time.Minute}
 prev:=r.Shift(at,-1)
 if prev.End.Sub(prev.Start)!=15*time.Minute||!prev.End.Equal(at.Add(-15*time.Minute)){t.Fatal("incorrect previous interval")}
 zoom:=prev.Zoom(at,true)
 if zoom.End.Sub(zoom.Start)!=7500*time.Millisecond*60{t.Fatal("incorrect zoom")}
}
