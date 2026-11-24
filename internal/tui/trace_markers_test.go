package tui

import (
 "strings"
 "testing"
 "time"
 "github.com/fernandocpaz/tailg/internal/core"
 "github.com/charmbracelet/lipgloss"
)
func TestTraceMarkersUseLocalMetadataAndKeepNarrowRows(t *testing.T){
 now:=time.Now()
 for _,tc:=range []struct{trace,span,want string}{
  {"","",""},{"11111111111111111111111111111111","","[T]"},
  {"11111111111111111111111111111111","2222222222222222","[TS]"},
  {"","2222222222222222","[S]"},
  {"not-a-trace","0000000000000000",""},
 }{
  event:=core.LogEvent{TraceID:tc.trace,SpanID:tc.span,ObservedAt:now,Message:"[09:11:32 INF] patient saved"}
  record:=core.LogRecord{Event:event,Fields:core.ParseLogFields(event),Text:event.Message}
  if got:=traceMarkerFor(record);got!=tc.want{t.Fatalf("marker=%q want %q",got,tc.want)}
  for _,width:=range []int{24,60,100}{
   line:=renderRecordRowAt(record,"",false,width,false,false,now,true)
   if lipgloss.Width(line)>width{t.Fatalf("row too wide: %q",line)}
   if tc.want!=""&&!strings.Contains(line,tc.want){t.Fatalf("marker missing: %q",line)}
  }
  if tc.want!=""&&strings.Contains(renderRecordRowAt(record,"",false,100,false,false,now),tc.want){t.Fatal("non-opted-in viewer changed")}
 }
}
