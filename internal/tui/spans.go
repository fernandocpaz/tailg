package tui

import (
 "context"
 "fmt"
 "sort"
 "strings"
 "time"
 "unicode"

 tea "github.com/charmbracelet/bubbletea"
 "github.com/charmbracelet/lipgloss"
)

// Span carries observed provider telemetry; timestamps define its waterfall.
type Span struct{
 ID,ParentID,Name,Service,Kind,Status,StatusMessage string
 HTTPStatus,Method,Path,DBSystem,MessagingSystem string
 Start,End time.Time
}
type SpanResult struct{
 Spans []Span
 Warning string
}
type spanViewState struct{
 result SpanResult
 open,loading,manual,detail bool
 index,offset,generation int
 notice,logSpan string
 cancel *searchController
 collapsed map[string]bool
 anchor time.Time
}
type spanResultMsg struct{generation int;id string;result SpanResult;err error}
type spanRow struct{span Span;depth int;children bool}

func(m *model)closeSpans(){
 if m.spanState.cancel!=nil{m.spanState.cancel.stop()}
 generation:=m.spanState.generation+1
 m.spanState=spanViewState{generation:generation}
}
func(m *model)loadSpans()tea.Cmd{
 if m.config.Spans==nil{return nil}
 s:=&m.spanState
 if s.cancel==nil{s.cancel=&searchController{}}
 parent:=m.ctx
 if parent==nil{parent=context.Background()}
 ctx:=s.cancel.start(parent)
 s.generation++
 generation,id:=s.generation,m.traceID
 if s.anchor.IsZero(){
  rows:=m.traceRows()
  if len(rows)>0{s.anchor=rows[0].Event.ObservedAt}
  if s.anchor.IsZero(){s.anchor=time.Now()}
 }
 at,query:=s.anchor,m.config.Spans
 s.loading=true;s.notice=""
 return func()tea.Msg{
  limited,cancel:=context.WithTimeout(ctx,45*time.Second);defer cancel()
  result,err:=query(limited,id,at)
  if ctx.Err()!=nil{return nil}
  return spanResultMsg{generation:generation,id:id,result:result,err:err}
 }
}
func(m model)receiveSpans(msg spanResultMsg)model{
 s:=&m.spanState
 if !m.traceOpen||msg.id!=m.traceID||msg.generation!=s.generation{return m}
 s.loading=false;s.result=msg.result;s.notice=""
 if msg.err!=nil{s.notice="Spans unavailable: "+msg.err.Error()}
 if len(s.result.Spans)==0{
  if s.notice==""{s.notice="No spans retained or accessible in this time window"}
  s.open=false;s.detail=false
 }else if !s.manual{s.open=true}
 s.index=min(s.index,max(0,len(m.spanRows())-1))
 return m
}
func(m model)spanSummary()string{
 s:=m.spanState
 if s.loading{return "Loading spans…"}
 if s.notice!=""{return s.notice}
 return fmt.Sprintf("%d spans (Tab)",len(s.result.Spans))
}
func cleanSpanText(v string)string{
 return strings.Map(func(r rune)rune{if unicode.IsControl(r){return ' '};return r},v)
}
// orderSpanRows retains missing-parent components and protects against malformed
// cyclic relationships. Children are ordered by their observed start times.
func orderSpanRows(spans []Span,collapsed map[string]bool)[]spanRow{
 items:=append([]Span(nil),spans...)
 sort.SliceStable(items,func(i,j int)bool{if items[i].Start.Equal(items[j].Start){return items[i].ID<items[j].ID};return items[i].Start.Before(items[j].Start)})
 byID:=map[string]Span{};children:=map[string][]Span{}
 for _,s:=range items{if s.ID!=""{byID[s.ID]=s}}
 for _,s:=range items{if s.ParentID!=""&&s.ParentID!=s.ID{children[s.ParentID]=append(children[s.ParentID],s)}}
 visited:=map[string]bool{};var out []spanRow
 var visit func(Span,int,bool)
 visit=func(s Span,depth int,hidden bool){
  if visited[s.ID]||s.ID==""{return};visited[s.ID]=true
  if !hidden{out=append(out,spanRow{s,depth,len(children[s.ID])>0})}
  for _,child:=range children[s.ID]{visit(child,depth+1,hidden||collapsed[s.ID])}
 }
 for _,s:=range items{if _,ok:=byID[s.ParentID];!ok||s.ParentID==s.ID{visit(s,0,false)}}
 for _,s:=range items{visit(s,0,false)}
 return out
}
func(m model)spanRows()[]spanRow{return orderSpanRows(m.spanState.result.Spans,m.spanState.collapsed)}
func spanBounds(items []Span)(time.Time,time.Time,time.Duration,int,Span){
 var start,end time.Time;var slow Span;errors:=0
 for _,s:=range items{
  if start.IsZero()||s.Start.Before(start){start=s.Start}
  if end.IsZero()||s.End.After(end){end=s.End}
  if strings.EqualFold(s.Status,"error"){errors++}
  if slow.ID==""||s.End.Sub(s.Start)>slow.End.Sub(slow.Start){slow=s}
 }
 return start,end,max(0,end.Sub(start)),errors,slow
}
func waterfallBar(s Span,start time.Time,elapsed time.Duration,width int)string{
 if width<=0{return ""}
 left,right:=0,1
 if elapsed>0{
  left=int(float64(s.Start.Sub(start))/float64(elapsed)*float64(width))
  right=int(float64(s.End.Sub(start))/float64(elapsed)*float64(width)+0.999999)
 }
 left=max(0,min(width-1,left));right=max(left+1,min(width,right))
 return strings.Repeat(" ",left)+strings.Repeat("█",right-left)+strings.Repeat(" ",width-right)
}
func(m model)renderSpans()string{
 s:=m.spanState;rows:=m.spanRows()
 start,_,elapsed,errors,slow:=spanBounds(s.result.Spans)
 width:=max(1,m.width)
 fit:=func(v string)string{return truncate(v,width)}
 title:="Spans | "+m.traceID
 if s.detail&&len(rows)>0{
  span:=rows[min(max(0,s.index),len(rows)-1)].span
  values:=[]string{"Operation: "+span.Name,"Service: "+span.Service,"Kind: "+span.Kind,"Status: "+span.Status,
   "Status message: "+span.StatusMessage,"Span ID: "+span.ID,"Parent ID: "+span.ParentID,
   "Start: "+span.Start.Format(time.RFC3339Nano),"End: "+span.End.Format(time.RFC3339Nano),"Duration: "+span.End.Sub(span.Start).String(),
   "HTTP: "+span.Method+" "+span.Path+" "+span.HTTPStatus,"Database: "+span.DBSystem,"Messaging: "+span.MessagingSystem}
  var lines []string
  for _,value:=range values{lines=append(lines,fit(cleanSpanText(value)))}
  offset:=min(s.offset,max(0,len(lines)-max(1,m.height-3)))
  return m.panel(title,strings.Join(lines[offset:],"\n"),"↑↓ scroll | Enter/Esc back | L span logs | Tab trace logs")
 }
 summary:=fmt.Sprintf("%d spans | observed envelope %s | %d errors | longest %s",len(s.result.Spans),elapsed.Round(time.Microsecond),errors,cleanSpanText(slow.Name))
 lines:=[]string{fit(summary)}
 if s.loading||s.notice!=""||s.result.Warning!=""{
  note:=s.notice;if s.loading{note="Loading spans…"}
  if s.result.Warning!=""{note+=" "+s.result.Warning}
  lines=append(lines,fit(cleanSpanText(note)))
 }
 if len(rows)==0{lines=append(lines,fit("No spans available. Tab returns to correlated logs."))}
 height:=max(1,m.height-3-len(lines))
 selected:=max(0,min(s.index,len(rows)-1))
 first:=max(0,selected-height+1)
 for i:=first;i<min(len(rows),first+height);i++{
  row:=rows[i];span:=row.span
  marker:="  ";if i==selected{marker="> "}
  expand:="  ";if row.children{expand="- ";if s.collapsed[span.ID]{expand="+ "}}
  label:=marker+strings.Repeat(" ",min(row.depth*2,12))+expand+cleanSpanText(span.Name)
  if span.Service!=""{label+=" ["+cleanSpanText(span.Service)+"]"}
  duration:=span.End.Sub(span.Start).Round(time.Microsecond).String()
  if strings.EqualFold(span.Status,"error"){duration="ERR "+duration}
  if width<45{lines=append(lines,fit(label+" "+duration));continue}
  labelWidth:=max(12,width/2-8);barWidth:=max(1,width-labelWidth-17)
  label=truncate(label,labelWidth)
  label+=strings.Repeat(" ",max(0,labelWidth-lipgloss.Width(label)))
  bar:=waterfallBar(span,start,elapsed,barWidth)
  if strings.EqualFold(span.Status,"error"){bar=renderWithColor(errorLineStyle,bar,m.config.Formatter.Color)}else if span.ID==slow.ID{bar=renderWithColor(warnStyle,bar,m.config.Formatter.Color)}else{bar=renderWithColor(okStyle,bar,m.config.Formatter.Color)}
  lines=append(lines,fit(label+" "+bar+" "+duration))
 }
 return m.panel(title,strings.Join(lines,"\n"),"Tab logs | ↑↓ select | Enter details | Space expand/collapse | L span logs | R reload | Esc close")
}
func(m model)updateSpanKey(key string)(tea.Model,tea.Cmd){
 s:=&m.spanState;rows:=m.spanRows()
 if key=="f6"{m.closeTrace();return m,nil}
 if s.detail {
  switch key{case "enter","backspace":s.detail=false
  case "up":s.offset=max(0,s.offset-1)
  case "down":s.offset++
  case "pgup":s.offset=max(0,s.offset-max(1,m.height-3))
  case "pgdown":s.offset+=max(1,m.height-3)
  case "l":if len(rows)>0{s.logSpan=rows[min(s.index,len(rows)-1)].span.ID;s.detail=false;s.open=false;s.manual=true;m.traceIndex=0}}
  return m,nil
 }
 switch key{
 case "up":s.index--
 case "down":s.index++
 case "pgup":s.index-=max(1,m.height-5)
 case "pgdown":s.index+=max(1,m.height-5)
 case "enter":if len(rows)>0{s.detail=true;s.offset=0}
 case " ":if len(rows)>0&&rows[min(s.index,len(rows)-1)].children{
   if s.collapsed==nil{s.collapsed=map[string]bool{}}
   id:=rows[min(s.index,len(rows)-1)].span.ID;s.collapsed[id]=!s.collapsed[id]
  }
 case "l":if len(rows)>0{s.logSpan=rows[min(s.index,len(rows)-1)].span.ID;s.open=false;s.manual=true;m.traceIndex=0}
 }
 s.index=max(0,min(s.index,len(m.spanRows())-1))
 return m,nil
}
