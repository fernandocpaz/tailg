package tui
import(
 "context"
 "testing"
)
func TestResourceBrowserResult(t *testing.T){
 m:=pickerModel{externalResource:func(context.Context,string,string)(string,error){return "opened",nil}}
 if m.openExternalResource("demo","demo-pod")==nil||!m.resourceOpening{t.Fatal("not started")}
 m.externalResourceMessage(externalResourceResult{request:m.resourceRequest-1,message:"stale"})
 if !m.resourceOpening{t.Fatal("stale result accepted")}
 m.externalResourceMessage(externalResourceResult{request:m.resourceRequest,message:"opened"})
 if m.resourceOpening||m.note!="opened"{t.Fatal("result not applied")}
}
