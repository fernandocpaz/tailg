package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/fernandocpaz/tailg/internal/kube"
)

func testNodeDiagnosticsReport() kube.WorkloadNodeDiagnostics {
	return kube.WorkloadNodeDiagnostics{
		Context:"tkgs-qa", Namespace:"apollo", Application:"worker",
		CapturedAt:time.Now(),
		Pods:[]kube.PodNodeDiagnostic{{
			Name:"worker-1",Node:"node-1",Phase:"Running",
			Restarts:2,OOMKilled:true,Ready:false,
			Waiting:[]string{"worker: CrashLoopBackOff"},
		}},
		Nodes:[]kube.NodeDiagnostic{{
			Name:"node-1",Ready:"True",CPUAllocatable:2000,CPUCapacityKnown:true,
			MemoryAllocatable:4*1024*1024*1024,MemoryCapacityKnown:true,
			MemoryUsed:3.8*1024*1024*1024,MemoryUsedKnown:true,
			Conditions:[]kube.NodePressureCondition{
				{Type:"Ready",Status:"True"},
				{Type:"MemoryPressure",Status:"True",Reason:"KubeletHasInsufficientMemory"},
			},
			MetricsWarning:"Node usage partially unavailable",
		}},
	}
}

func TestPickerDOpensNodeDiagnosticsAndPreservesSelection(t *testing.T) {
	m:=namespaceActivityPicker()
	updated,cmd:=m.Update(tea.KeyMsg{Type:tea.KeyRunes,Runes:[]rune{'d'}})
	m=updated.(pickerModel)
	if !m.nodeDiag.open || !m.nodeDiag.busy || cmd==nil ||
		m.nodeDiag.app!="worker" || len(m.nodeDiag.pods)!=1 || m.nodeDiag.pods[0]!="worker-1" {
		t.Fatalf("D did not start focused diagnostics: %+v",m.nodeDiag)
	}
	// R while pending does not launch an overlapping node read.
	updated,cmd=m.Update(tea.KeyMsg{Type:tea.KeyRunes,Runes:[]rune{'r'}})
	m=updated.(pickerModel)
	if cmd!=nil || m.nodeDiag.request!=1 {t.Fatal("overlapping diagnostics request launched")}
	// Keys meant for the picker must not affect selection while the modal is open.
	for _,key:=range []string{"n","c","x","space"} {m=activityKey(m,key)}
	if m.index!=1 || !m.checked["api"] || m.result.Action!=PickerQuit {
		t.Fatal("node modal changed picker state")
	}
	scope:=activityScope(m.kubeContext,m.namespace)
	updated,_=m.Update(nodeDiagnosticResultMsg{
		request:m.nodeDiag.request,scope:scope,app:"worker",report:testNodeDiagnosticsReport(),
	})
	m=updated.(pickerModel)
	if m.nodeDiag.busy || len(m.nodeDiag.report.Nodes)!=1 {
		t.Fatal("node diagnostics reply not applied")
	}
	view:=ansi.Strip(m.View())
	for _,term:=range []string{"Node diagnostics","worker-1","node-1","OOMKilled",
		"CrashLoopBackOff","MemoryPressure=True","KubeletHasInsufficientMemory","R refresh"} {
		if !strings.Contains(view,term) {
			t.Fatalf("missing expected modal evidence %q in\n%s",term,view)
		}
	}
	m=activityKey(m,"esc")
	if m.nodeDiag.open || m.index!=1 || !m.checked["api"] {
		t.Fatal("Esc failed to restore picker")
	}
}

func TestNodeDiagnosticsStaleResultsRejectedAndRefreshGuarded(t *testing.T) {
	m:=namespaceActivityPicker()
	m=activityKey(m,"d")
	oldRequest:=m.nodeDiag.request
	m=activityKey(m,"esc")
	before:=m.nodeDiag.report
	updated,_:=m.Update(nodeDiagnosticResultMsg{request:oldRequest,
		scope:activityScope(m.kubeContext,m.namespace),app:"worker",report:testNodeDiagnosticsReport()})
	m=updated.(pickerModel)
	if m.nodeDiag.open || !m.nodeDiag.report.CapturedAt.Equal(before.CapturedAt) {
		t.Fatal("closed modal accepted stale results")
	}
	m=activityKey(m,"d")
	updated,_=m.Update(nodeDiagnosticResultMsg{
		request:m.nodeDiag.request,scope:activityScope("other","apollo"),app:"worker",
		report:testNodeDiagnosticsReport(),
	})
	m=updated.(pickerModel)
	if !m.nodeDiag.busy {t.Fatal("cross-context result accepted")}
	updated,_=m.Update(nodeDiagnosticResultMsg{
		request:m.nodeDiag.request,scope:activityScope(m.kubeContext,m.namespace),app:"worker",
		report:testNodeDiagnosticsReport(),
	})
	m=updated.(pickerModel)
	if m.nodeDiag.busy {t.Fatal("correct result not accepted")}
	m=activityKey(m,"r")
	if !m.nodeDiag.busy || m.nodeDiag.request<=oldRequest {
		t.Fatal("manual refresh did not restart diagnostics")
	}
}

func TestNodeDiagnosticsNoPodsShowsActionableNotice(t *testing.T) {
	m:=namespaceActivityPicker()
	m.apps[m.index].Pods=nil
	m=activityKey(m,"d")
	if m.nodeDiag.open || !strings.Contains(m.note,"no current pods") {
		t.Fatalf("unexpected empty selection handling: %+v, notice %q",m.nodeDiag,m.note)
	}
}

func TestNodeDiagnosticsModalFitsViewportAndScroll(t *testing.T) {
	for _,size:=range []tea.WindowSizeMsg{
		{Width:40,Height:12},{Width:80,Height:24},
		{Width:140,Height:40},{Width:24,Height:8},
	} {
		m:=namespaceActivityPicker()
		m.nodeDiag.open=true
		m.nodeDiag.app="worker"
		m.nodeDiag.report=testNodeDiagnosticsReport()
		for i:=0;i<60;i++ {
			m.nodeDiag.report.Warnings=append(m.nodeDiag.report.Warnings,"additional node diagnostic detail for scrolling")
		}
		updated,_:=m.Update(size)
		m=updated.(pickerModel)
		m=activityKey(m,"end")
		if size.Width>=40 && m.nodeDiag.scroll==0 {
			t.Fatal("end should scroll a long modal")
		}
		rows:=strings.Split(ansi.Strip(m.View()),"\n")
		if len(rows)!=size.Height {
			t.Fatalf("height %d != %d for %+v",len(rows),size.Height,size)
		}
		for _,row:=range rows {
			if lipgloss.Width(row)!=size.Width {
				t.Fatalf("width %d != %d for %+v: %q",lipgloss.Width(row),size.Width,size,row)
			}
		}
		if !strings.Contains(strings.Join(rows,"\n"),"Esc") {t.Fatal("close control hidden")}
	}
}
