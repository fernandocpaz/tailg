package tui

import (
 "context"
 "errors"
 "strings"
 "testing"
 "time"

 tea "github.com/charmbracelet/bubbletea"
 "github.com/charmbracelet/lipgloss"
)
func TestExternalSpansOpenAndFallback(t *testing.T){
 m:=workflowModel()
 m.config.Spans=func(context.Context,string,time.Time)(SpanResult,error){return SpanResult{},nil}
 m.openTrace(workflowTrace)
 now:=time.Now()
 result:=SpanResult{Spans:[]Span{
  {ID:"child",ParentID:"root",Name:"SQL",Start:now.Add(20*time.Millisecond),End:now.Add(80*time.Millisecond),Status:"error"},
  {ID:"root",Name:"HTTP",Start:now,End:now.Add(100*time.Millisecond)},
 }}
 m=m.receiveSpans(spanResultMsg{generation:m.spanState.generation,id:workflowTrace,result:result})
 if !m.spanState.open||len(m.spanRows())!=2||m.spanRows()[1].depth!=1{t.Fatal("span hierarchy did not open")}
 if !strings.Contains(m.renderSpans(),"1 errors"){t.Fatal("error count absent")}
 for _,width:=range []int{40,70,120}{
  m.width=width
  for _,line:=range strings.Split(m.renderSpans(),"\n"){if lipgloss.Width(line)>width{t.Fatalf("line too wide: %q",line)}}
 }
 next,_:=m.Update(tea.KeyMsg{Type:tea.KeyTab});m=next.(model)
 if m.spanState.open{t.Fatal("Tab must return to correlated logs")}
 m=m.receiveSpans(spanResultMsg{generation:m.spanState.generation,id:workflowTrace,err:errors.New("permission denied")})
 if m.spanState.open||!strings.Contains(m.spanSummary(),"permission denied"){t.Fatal("missing permission must fall back to logs")}
 m.closeTrace()
 m=m.receiveSpans(spanResultMsg{generation:m.spanState.generation-1,id:workflowTrace,result:result})
 if m.spanState.open{t.Fatal("late response reopened trace")}
}
func TestSpanHierarchyHandlesMissingParentsCyclesAndCollapse(t *testing.T){
 now:=time.Now()
 items:=[]Span{{ID:"r",Start:now},{ID:"c",ParentID:"r",Start:now},{ID:"orphan",ParentID:"missing",Start:now},{ID:"a",ParentID:"b",Start:now},{ID:"b",ParentID:"a",Start:now}}
 if len(orderSpanRows(items,nil))!=5{t.Fatal("missing/cyclic parents lost spans")}
 if len(orderSpanRows(items,map[string]bool{"r":true}))!=4{t.Fatal("collapse should hide descendants only")}
 if b:=waterfallBar(Span{Start:now.Add(50*time.Millisecond),End:now.Add(100*time.Millisecond)},now,100*time.Millisecond,10);b!="     █████"{t.Fatalf("wrong waterfall: %q",b)}
}
