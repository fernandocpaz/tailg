package tui

import (
 "testing"
 "context"
 tea "github.com/charmbracelet/bubbletea"
 "github.com/fernandocpaz/tailg/internal/core"
)

func TestExternalPodPickerSelectsTwoExactPods(t *testing.T) {
 apps:=[]core.AppChoice{
  {Name:"encounter", Pods:[]string{"encounter-a","encounter-b"}},
  {Name:"patient", Pods:[]string{"patient-a"}},
 }
 m:=newPickerModel(apps,len(apps),"tkgs-prod","apollo",nil)
 m.external=true
 m.width,m.height=110,32
 m.refreshContext=context.Background()
 next,cmd:=m.Update(tea.KeyMsg{Type:tea.KeyRunes,Runes:[]rune{'p'}})
 m=next.(pickerModel)
 if cmd!=nil||m.selection==nil||m.selection.pods==nil {
  t.Fatal("P must open the local Grail pod picker without kubectl")
 }
 if len(m.selection.pods.rows)!=3 {t.Fatalf("expected 3 available pods, got %d",len(m.selection.pods.rows))}
 next,_=m.Update(tea.KeyMsg{Type:tea.KeySpace})
 m=next.(pickerModel)
 next,_=m.Update(tea.KeyMsg{Type:tea.KeyDown})
 m=next.(pickerModel)
 next,_=m.Update(tea.KeyMsg{Type:tea.KeySpace})
 m=next.(pickerModel)
 next,_=m.Update(tea.KeyMsg{Type:tea.KeyEnter})
 m=next.(pickerModel)
 if len(m.result.Pods)!=2||m.result.Pods[0]!="encounter-a"||m.result.Pods[1]!="encounter-b"{
  t.Fatalf("exact pod selection not returned: %+v",m.result)
 }
 if len(m.result.Apps)!=0 {t.Fatal("exact pods must not be coerced into applications")}
}

func TestExternalPodPickerRNeverStartsKubernetesRefresh(t *testing.T){
 apps:=[]core.AppChoice{{Name:"worker",Pods:[]string{"worker-1"}}}
 m:=newPickerModel(apps,1,"tkgs-prod","apollo",nil)
 m.external=true;m.width,m.height=110,32;m.refreshContext=context.Background()
 next,_:=m.Update(tea.KeyMsg{Type:tea.KeyRunes,Runes:[]rune{'p'}})
 m=next.(pickerModel)
 next,cmd:=m.Update(tea.KeyMsg{Type:tea.KeyRunes,Runes:[]rune{'r'}})
 m=next.(pickerModel)
 if cmd!=nil||m.inventoryBusy||m.selection==nil||len(m.selection.pods.rows)!=1{
  t.Fatal("external P/R must only refresh cached Grail pods")
 }
}
