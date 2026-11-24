package core
import (
 "testing"
 "time"
)
func TestExternalHTTPMetadataFeedsTheRealF9Analytics(t *testing.T) {
 now:=time.Now().UTC().Truncate(time.Second)
 event:=LogEvent{ObservedAt:now.Add(-time.Second),Container:"api",Workload:"encounter",
  Message:"request done",HTTPMethod:"POST",HTTPPath:"/api/encounter/123?token=hidden",
  HTTPStatusCode:503,HTTPDuration:450*time.Millisecond,HasHTTPDuration:true}
 fields:=ParseLogFields(event)
 if fields.Method!="POST"||fields.Path!="/api/encounter/123"||fields.StatusCode!=503 ||
  !fields.HasDuration||fields.Duration!=450*time.Millisecond {
  t.Fatalf("HTTP fields missing: %+v",fields)
 }
 analytics:=NewRollingAnalytics()
 if !analytics.ObserveAt(event,now){t.Fatal("external HTTP log not accepted by analytics")}
 snapshot:=analytics.SnapshotWindow(now,15)
 if snapshot.Total.Requests!=1||snapshot.Total.ServerErrors!=1||snapshot.Total.Timed!=1||snapshot.Total.Slow!=1{
  t.Fatalf("F9 counts wrong: %+v",snapshot.Total)
 }
 if len(snapshot.Endpoints)!=1||snapshot.Endpoints[0].Path!="/api/encounter/{id}"{
  t.Fatalf("unexpected fingerprinted endpoint: %+v",snapshot.Endpoints)
 }
}
func TestExternalHTTPFieldsDoNotInventMissingMetrics(t *testing.T){
 event:=LogEvent{HTTPStatusCode:200,HTTPDuration:0,HasHTTPDuration:false}
 fields:=ParseLogFields(event)
 if fields.HasDuration||fields.Method!=""||fields.Path!=""{t.Fatalf("invented missing HTTP measurements: %+v",fields)}
}
