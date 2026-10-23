package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func resourceKey(m model, key tea.KeyType) model {
	result, _ := m.Update(tea.KeyMsg{Type:key})
	return result.(model)
}

func longMappedResource() string {
	var b strings.Builder
	b.WriteString("ConfigMap: sample-appsettings\nNamespace: qa\nappsettings.k8s.json:\n")
	for i:=0;i<170;i++ {
		fmt.Fprintf(&b, "  \"AppSetting%03d\": \"Some long configuration value for checking the terminal viewport behavior\",\n",i)
	}
	b.WriteString("END_OF_COMPLETE_RESOURCE_DOCUMENT")
	return b.String()
}

func TestMappedResourceViewerScrollsToEndWithoutTruncatingDocument(t *testing.T) {
	m:=workflowModel()
	m.resourceOpen=true
	m.width,m.height=76,15
	full:=longMappedResource()
	result,_:=m.Update(resourceDetailMsg{text:full})
	m=result.(model)
	if m.resourceDetail!=full || m.resourceOffset!=0 {t.Fatal("resource text was modified or offset not reset")}
	first:=m.View()
	if strings.Contains(first,"END_OF_COMPLETE_RESOURCE_DOCUMENT") {t.Fatal("document end should initially be below the viewport")}
	if !strings.Contains(first,"█") || !strings.Contains(first,"│") {t.Fatal("expected a visible vertical scrollbar")}
	if !strings.Contains(first,"PgUp/PgDn") {t.Fatal("missing scroll help")}
	if got:=len(strings.Split(first,"\n"));got!=m.height {t.Fatalf("expected %d terminal rows, got %d",m.height,got)}
	for _,row:=range strings.Split(first,"\n") {
		if lipgloss.Width(row)>m.width {t.Fatalf("row overflows width %d: %d",m.width,lipgloss.Width(row))}
	}
	m=resourceKey(m,tea.KeyDown)
	if m.resourceOffset!=1{t.Fatalf("Down did not scroll: %d",m.resourceOffset)}
	m=resourceKey(m,tea.KeyPgDown)
	if m.resourceOffset<=1{t.Fatal("PageDown did not advance")}
	m=resourceKey(m,tea.KeyEnd)
	if !strings.Contains(m.View(),"END_OF_COMPLETE_RESOURCE_DOCUMENT"){t.Fatal("End cannot access last line of ConfigMap")}
	end:=m.resourceOffset
	if end!=max(0,len(m.mappedResourceLines())-m.mappedResourceHeight()){t.Fatal("End did not reach last viewport")}
	m=resourceKey(m,tea.KeyDown)
	if m.resourceOffset!=end {t.Fatal("scrolled past end")}
	m=resourceKey(m,tea.KeyPgUp)
	if m.resourceOffset>=end{t.Fatal("PageUp did not move")}
	m=resourceKey(m,tea.KeyHome)
	if m.resourceOffset!=0 || !strings.Contains(m.View(),"ConfigMap: sample-appsettings") {t.Fatal("Home did not restore beginning")}
	if m.resourceDetail!=full {t.Fatal("scrolling changed copyable resource content")}
}

func TestMappedResourceMouseWheelScrollbarClickAndResize(t *testing.T) {
	m:=workflowModel()
	m.resourceOpen=true
	m.width,m.height=54,13
	result,_:=m.Update(resourceDetailMsg{text:longMappedResource()})
	m=result.(model)
	if m.resourceMouseCapture {t.Fatal("mouse capture must be disabled by default for terminal text selection")}
	if !strings.Contains(m.View(),"M:mouse OFF") {t.Fatal("missing native text selection hint")}
	result,_=m.Update(tea.MouseMsg{Button:tea.MouseButtonWheelDown,Action:tea.MouseActionPress})
	m=result.(model)
	if m.resourceOffset!=0{t.Fatal("mouse should not be captured before explicit opt-in")}
	result,mouseEnable:=m.Update(tea.KeyMsg{Type:tea.KeyRunes,Runes:[]rune{'m'}})
	m=result.(model)
	if mouseEnable==nil || !m.resourceMouseCapture || !strings.Contains(m.View(),"M:mouse ON") {
		t.Fatal("M must enable mouse capture with a visible text-selection warning")
	}
	result,_=m.Update(tea.MouseMsg{Button:tea.MouseButtonWheelDown,Action:tea.MouseActionPress})
	m=result.(model)
	if m.resourceOffset!=3{t.Fatalf("mouse wheel did not scroll down: %d",m.resourceOffset)}
	result,_=m.Update(tea.MouseMsg{Button:tea.MouseButtonWheelUp,Action:tea.MouseActionPress})
	m=result.(model)
	if m.resourceOffset!=0{t.Fatal("mouse wheel failed to scroll up")}
	result,_=m.Update(tea.MouseMsg{X:m.width-1,Y:m.height-2,Button:tea.MouseButtonLeft,Action:tea.MouseActionPress})
	m=result.(model)
	if !strings.Contains(m.View(),"END_OF_COMPLETE_RESOURCE_DOCUMENT"){t.Fatal("scrollbar click could not reach end")}
	result,_=m.Update(tea.WindowSizeMsg{Width:35,Height:9})
	m=result.(model)
	if !strings.Contains(m.View(),"END_OF_COMPLETE_RESOURCE_DOCUMENT") {t.Fatal("resize hid last line")}
	for _,line:=range strings.Split(m.View(),"\n") {
		if lipgloss.Width(line)>m.width{t.Fatalf("resize overflow: width=%d rendered=%d",m.width,lipgloss.Width(line))}
	}
	result,mouseDisable:=m.Update(tea.KeyMsg{Type:tea.KeyEsc})
	m=result.(model)
	if mouseDisable==nil || m.resourceMouseCapture || m.resourceDetail!="" || m.resourceLayout!=nil || m.resourceOffset!=0 || !m.resourceOpen {
		t.Fatal("Esc should disable mouse capture, return to mapped resource list, and reset viewport")
	}
	result,_=m.Update(resourceDetailMsg{text:"one\ntwo"})
	m=result.(model)
	if m.resourceOffset!=0 || strings.Contains(m.View(),"END_OF_COMPLETE_RESOURCE_DOCUMENT"){
		t.Fatal("new mapped resource inherited stale viewport")
	}
}

func TestMappedResourceEnterCopiesFullUntruncatedContent(t *testing.T) {
	if runtime.GOOS=="windows"{t.Skip("fake clipboard test uses POSIX shell")}
	dir:=t.TempDir()
	clipboard:=filepath.Join(dir,"clipboard")
	t.Setenv("TAILG_TEST_CLIPBOARD",clipboard)
	t.Setenv("PATH",dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	name:="wl-copy"
	if runtime.GOOS=="darwin"{name="pbcopy"}
	if err:=os.WriteFile(filepath.Join(dir,name),[]byte("#!/bin/sh\ncat > \"$TAILG_TEST_CLIPBOARD\"\n"),0700);err!=nil{t.Fatal(err)}
	m:=workflowModel()
	m.resourceOpen=true
	m.width,m.height=65,12
	full:=longMappedResource()
	result,_:=m.Update(resourceDetailMsg{text:full})
	m=result.(model)
	m=resourceKey(m,tea.KeyEnd)
	m=resourceKey(m,tea.KeyEnter)
	b,err:=os.ReadFile(clipboard)
	if err!=nil || string(b)!=full{t.Fatalf("copy did not preserve entire document: size=%d, want=%d, err=%v",len(b),len(full),err)}
	if m.resourceDetail!="" || !m.resourceOpen{t.Fatal("Enter should close detail after copy")}
}

func TestMappedResourceMouseModeReturnsToSelectionWithMEnterAndF2(t *testing.T) {
	m:=workflowModel()
	m.resourceOpen=true
	m.width,m.height=90,20
	updated,_:=m.Update(resourceDetailMsg{text:longMappedResource()})
	m=updated.(model)
	keyM:=tea.KeyMsg{Type:tea.KeyRunes,Runes:[]rune{'m'}}
	updated,cmd:=m.Update(keyM)
	m=updated.(model)
	if cmd==nil || !m.resourceMouseCapture{t.Fatal("mouse opt-in not enabled")}
	updated,cmd=m.Update(keyM)
	m=updated.(model)
	if cmd==nil || m.resourceMouseCapture{t.Fatal("M must disable mouse capture and restore native selection")}
	updated,_=m.Update(keyM)
	m=updated.(model)
	updated,cmd=m.Update(tea.KeyMsg{Type:tea.KeyF2})
	m=updated.(model)
	if cmd==nil || m.resourceMouseCapture || m.resourceOpen{t.Fatal("F2 must restore native selection")}
	m.resourceOpen=true
	updated,_=m.Update(resourceDetailMsg{text:longMappedResource()})
	m=updated.(model)
	updated,_=m.Update(keyM)
	m=updated.(model)
	updated,cmd=m.Update(tea.KeyMsg{Type:tea.KeyEnter})
	m=updated.(model)
	if cmd==nil || m.resourceMouseCapture || m.resourceDetail!="" {
		t.Fatal("copy/close must restore native mouse selection")
	}
}
