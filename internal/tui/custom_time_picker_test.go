package tui

import (
 "strings"
 "testing"
 "time"
)

func TestCustomPickerAlwaysValid(t *testing.T){
 now:=time.Date(2026,11,24,15,0,0,0,time.UTC)
 p:=newCustomTimePicker(now.Add(-time.Minute),now,now)
 keys:=[]string{"left","right","tab","up","down","+","-","n","pgup","pgdown","shift+tab"}
 for i:=0;i<1000;i++{
  p.key(keys[(i*7+i/11)%len(keys)],now)
  if !p.selection.Valid()||p.selection.End.After(now){t.Fatalf("invalid interval after key: %+v",p.selection)}
 }
 if !strings.Contains(p.view(100,false),"Duration:"){t.Fatal("duration preview missing")}
}
func TestCustomPickerClampsCalendarAndCrossedEndpoints(t *testing.T){
 jan:=time.Date(2025,1,31,14,0,0,0,time.UTC)
 feb:=pickerCalendarStep(jan,1,1)
 if feb.Month()!=time.February||feb.Day()!=28{t.Fatalf("invalid month edit: %v",feb)}
 leap:=time.Date(2024,2,29,14,0,0,0,time.UTC)
 next:=pickerCalendarStep(leap,0,1)
 if next.Day()!=28||next.Month()!=time.February{t.Fatalf("invalid year edit: %v",next)}
 now:=jan.Add(90*24*time.Hour)
 p:=newCustomTimePicker(jan,jan.Add(time.Hour),now)
 p.setEndpoint(true,jan.Add(-time.Hour),now)
 if !p.selection.Valid()||p.selection.End.Sub(p.selection.Start)!=time.Hour||p.note==""{t.Fatal("crossed end not corrected")}
 p.setEndpoint(false,now.Add(time.Hour),now)
 if !p.selection.Valid()||p.selection.End.After(now){t.Fatal("future start not corrected")}
 p.key("n",now)
 if !p.selection.End.Equal(now){t.Fatal("now shortcut failed")}
}
func TestCustomPickerTabAndDayShift(t *testing.T){
 now:=time.Date(2026,11,24,15,0,0,0,time.UTC)
 p:=newCustomTimePicker(now.Add(-time.Hour),now,now)
 before:=p.selection
 p.key("tab",now)
 if p.field!=9{t.Fatal("tab did not switch endpoint")}
 p.key("pgup",now)
 if !p.selection.Start.Equal(before.Start.Add(-24*time.Hour))||!p.selection.End.Equal(before.End.Add(-24*time.Hour)){t.Fatal("day shift failed")}
 p.key("pgdown",now)
 if !p.selection.equal(before){t.Fatal("day shift did not return")}
}
