package tui

import (
 "context"
 "strings"
 "testing"

 tea "github.com/charmbracelet/bubbletea"
)
func TestProviderAndTailgShareUpdateDialog(t *testing.T){
 for _,app:=range []string{"tailg","DynaR"}{
  view:=renderUpdateDialog(app,"v0.4.0","v0.4.1",false,false,"",100,30)
  for _,piece:=range []string{app+" update available","v0.4.0","v0.4.1","Later","Update & restart"}{
    if !strings.Contains(view,piece){t.Fatalf("%s: missing %q",app,piece)}
  }
 }
}
func TestExternalUpdateKeys(t *testing.T){
 calls:=0
 offer:=ExternalUpdateOffer{Application:"DynaR",Installed:"v0.4.0",Latest:"v0.4.1",Install:func(context.Context)error{calls++;return nil}}
 m:=externalUpdateModel{ctx:context.Background(),offer:offer,width:100,height:30}
 next,cmd:=m.Update(tea.KeyMsg{Type:tea.KeyTab})
 m=next.(externalUpdateModel)
 if !m.focused||cmd!=nil{t.Fatal("tab should focus Update")}
 next,cmd=m.Update(tea.KeyMsg{Type:tea.KeyEnter})
 m=next.(externalUpdateModel)
 if !m.busy||cmd==nil{t.Fatal("enter should begin installation")}
 result:=cmd()
 next,_=m.Update(result)
 m=next.(externalUpdateModel)
 if calls!=1||!m.installed||m.busy{t.Fatalf("install result not reflected, calls=%d",calls)}
 later:=externalUpdateModel{ctx:context.Background(),offer:offer}
 next,_=later.Update(tea.KeyMsg{Type:tea.KeyEsc})
 if next.(externalUpdateModel).installed||calls!=1{t.Fatal("Esc must never install")}
}
