package tui

import (
 "fmt"
 "strings"
 "time"
)

// Each component is edited with arrows rather than parsing user-entered text.
// The picker always holds a complete, ordered, past interval of 1s to 31d.
type customTimePicker struct {
 selection TimeRange
 field int // 0..5: From; 6..11: To
 note string
}

func newCustomTimePicker(from,to,now time.Time) customTimePicker {
 now=now.Truncate(time.Second)
 from=from.Local().Truncate(time.Second)
 to=to.Local().Truncate(time.Second)
 if to.After(now){to=now.Local()}
 if !from.Before(to){from=to.Add(-time.Second)}
 if to.Sub(from)>maxTimeRange{from=to.Add(-maxTimeRange)}
 return customTimePicker{selection:TimeRange{Start:from,End:to},field:3}
}

// Calendar month/year edits clamp to the last existing day (e.g. Jan 31 to Feb 28).
func pickerCalendarStep(at time.Time,field,delta int)time.Time{
 switch field{
 case 0,1:
  years,months:=0,0
  if field==0{years=delta}else{months=delta}
  first:=time.Date(at.Year()+years,at.Month()+time.Month(months),1,at.Hour(),at.Minute(),at.Second(),0,at.Location())
  last:=time.Date(first.Year(),first.Month()+1,0,12,0,0,0,at.Location()).Day()
  return time.Date(first.Year(),first.Month(),min(at.Day(),last),at.Hour(),at.Minute(),at.Second(),0,at.Location())
 case 2:return at.AddDate(0,0,delta)
 case 3:return at.Add(time.Duration(delta)*time.Hour)
 case 4:return at.Add(time.Duration(delta)*time.Minute)
 default:return at.Add(time.Duration(delta)*time.Second)
 }
}

func(p *customTimePicker)setEndpoint(to bool,at,now time.Time){
 now=now.Truncate(time.Second)
 at=at.Truncate(time.Second)
 p.note=""
 old:=p.selection
 width:=old.End.Sub(old.Start)
 if at.After(now){at=now;p.note="Cannot select a future time"}
 if to{
  p.selection.End=at
  if !old.Start.Before(at)||at.Sub(old.Start)>maxTimeRange{
   p.selection.Start=at.Add(-width)
   p.note="From moved to keep a valid interval"
  }
 }else{
  if !at.Before(now){at=now.Add(-time.Second);p.note="From must be before now"}
  p.selection.Start=at
  if !old.End.After(at)||old.End.Sub(at)>maxTimeRange{
   p.selection.End=at.Add(width)
   if p.selection.End.After(now){p.selection.End=now}
   p.note="To moved to keep a valid interval"
  }
 }
}

func(p *customTimePicker)key(key string,now time.Time){
 switch key{
 case "tab","shift+tab":p.field=(p.field+6)%12
 case "left":p.field=(p.field+11)%12
 case "right":p.field=(p.field+1)%12
 case "up","down","+","=","-":
  delta:=1
  if key=="down"||key=="-"{delta=-1}
  if key=="+"||key=="="||key=="-"{delta*=5}
  at:=p.selection.Start
  if p.field>=6{at=p.selection.End}
  p.setEndpoint(p.field>=6,pickerCalendarStep(at,p.field%6,delta),now)
 case "n":
  p.setEndpoint(true,now,now)
  p.field=9
 case "pgup","pgdown":
  direction:=-1
  if key=="pgdown"{direction=1}
  r:=p.selection.Shift(now.Truncate(time.Second),direction)
  // Page navigation moves the full interval by one calendar-sized day,
  // independent of its current duration.
  width:=r.End.Sub(r.Start)
  from:=p.selection.Start.Add(time.Duration(direction)*24*time.Hour)
  to:=from.Add(width)
  if to.After(now.Truncate(time.Second)){to=now.Truncate(time.Second);from=to.Add(-width)}
  p.selection=TimeRange{Start:from,End:to}
  p.note=""
 }
}

func(p customTimePicker)view(width int,color bool)string{
 format:=func(at time.Time,offset int)string{
  at=at.Local()
  parts:=[]string{at.Format("2006"),at.Format("Jan"),at.Format("02"),at.Format("15"),at.Format("04"),at.Format("05")}
  for i,v:=range parts{
   if p.field==offset+i{parts[i]=renderWithColor(selectedStyle,"["+v+"]",color)}else{parts[i]=" "+v+" "}
  }
  return strings.Join(parts[:3],"")+" "+strings.Join(parts[3:],":")+" "+at.Format("MST -0700")
 }
 from,to:=p.selection.Start,p.selection.End
 lines:=[]string{
  "Choose date and time (no typing required)",
  "From "+format(from,0),
  "To   "+format(to,6),
  "Duration: "+compactDuration(to.Sub(from)),
  "Left/Right field | Up/Down change | Tab From/To",
  "+/- change by 5 | N end now | PgUp/PgDn shift day",
 }
 if p.note!=""{lines=append(lines,p.note)}else{lines=append(lines,"Ready: Enter applies to every window")}
 // On narrow panes show the active endpoint and zone on separate lines
 // rather than clipping the currently selected segment.
 if width<51{
  formatSmall:=func(at time.Time,offset int)string{
   at=at.Local()
   parts:=[]string{at.Format("2006"),at.Format("01"),at.Format("02"),at.Format("15"),at.Format("04"),at.Format("05")}
   for i,v:=range parts{if p.field==offset+i{parts[i]="["+v+"]"}}
   return strings.Join(parts[:3],"-")+" "+strings.Join(parts[3:],":")
  }
  lines[1]="From "+formatSmall(from,0)
  lines[2]="To   "+formatSmall(to,6)
  lines[3]=fmt.Sprintf("%s | %s → %s",compactDuration(to.Sub(from)),from.Local().Format("-0700"),to.Local().Format("-0700"))
  lines[4]="←/→ field  ↑/↓ edit  Tab switch"
  lines[5]="+/- ×5  N now  PgUp/Dn day"
 }
 for i:=range lines{lines[i]=truncate(lines[i],width)}
 return strings.Join(lines,"\n")
}
