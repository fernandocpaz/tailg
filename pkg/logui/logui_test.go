package logui
import (
 "context"
 "testing"
 "time"
)
func TestConvertSourcesAndMetadata(t *testing.T){
 v:=inventory([]Source{{Pod:"pod1",Container:"app"},{Pod:"pod1",Container:"app"},{Pod:"pod2",Container:"worker"}})
 if len(v)!=2{t.Fatal("dedup inventory",v)}
 e:=ToLogEvent(Event{Pod:"pod1",Container:"app",Workload:"api",Level:"ERROR",TraceID:"123",RequestID:"req",Timestamp:time.Unix(1700000000,0),Message:"failure"})
 if e.Level!="ERROR"||e.TraceID!="123"||e.Workload!="api"{t.Fatalf("metadata lost: %+v",e)}
}
func TestMissingStream(t *testing.T){
 if err:=Run(context.Background(),Config{});err==nil{t.Fatal("missing provider should fail")}
}
