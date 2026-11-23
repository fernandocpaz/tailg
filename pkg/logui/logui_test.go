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

func TestMatchesTailgStructuredPredicates(t *testing.T){
    trace:="0123456789abcdef0123456789abcdef"
    data:=[]Event{
        {Workload:"encounter",Pod:"encounter-abcd",Container:"app",Level:"ERROR",TraceID:trace,Message:"SQL timeout"},
        {Workload:"patient",Pod:"patient-bcde",Container:"app",Level:"INFO",Message:"Patient loaded"},
    }
    found,err:=MatchEvents("level:ERR service:encounter",data)
    if err!=nil{t.Fatal(err)}
    if len(found)!=1||found[0].Workload!="encounter"{t.Fatalf("got %v",found)}
    found,err=MatchEvents("trace:"+trace,data)
    if err!=nil{t.Fatal(err)}
    if len(found)!=1{t.Fatalf("trace metadata not searchable: %v",found)}
    found,err=MatchEvents("SQL timeout",data)
    if err!=nil||len(found)!=1{t.Fatalf("plain search: %v %v",found,err)}
}
