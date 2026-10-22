package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/fernandocpaz/tailg/internal/core"
	"github.com/fernandocpaz/tailg/internal/kube"
)

func TestNamespaceResourceMetricsRenderAndAggregate(t *testing.T) {
	app := core.AppChoice{Name: "vitas-emr-api-patient", Pods: []string{"api-a", "api-b", "api-b"}}
	m := newPickerModel([]core.AppChoice{app}, 1, "tkgs-qa", "apollo", nil)
	m.width, m.height = 160, 28
	m.monitorEnabled = true
	m.metricsAt = time.Now()
	m.metricsPods = map[string]kube.PodResourceUsage{
		"api-a": {CPUMilli: 125, MemoryBytes: 256*1024*1024, CPUValid: true, MemoryValid: true,
			CPULimitMilli: 250, MemoryLimitBytes: 512*1024*1024,
			CPULimitKnown: true, MemoryLimitKnown: true},
		"api-b": {CPUMilli: 75, MemoryBytes: 128*1024*1024, CPUValid: true, MemoryValid: true,
			CPULimitMilli: 250, MemoryLimitBytes: 512*1024*1024,
			CPULimitKnown: true, MemoryLimitKnown: true},
	}
	total, ok := m.appResources(app)
	if !ok || total.CPUMilli != 200 || total.MemoryBytes != 384*1024*1024 {
		t.Fatalf("replica aggregation counted duplicate or dropped a pod: %+v", total)
	}
	view := ansi.Strip(m.View())
	for _, expected := range []string{"CPU", "MEM", "200m", "384Mi", "CPU 200m / limit 500m (40%)",
		"memory 384Mi / limit 1.0Gi (38%)"} {
		if !strings.Contains(view, expected) {
			t.Fatalf("missing metric field %q: %s", expected, view)
		}
	}
	if got := lipgloss.Width(strings.Split(view, "\n")[0]); got != 160 {
		t.Fatalf("monitoring changed viewport width: %d", got)
	}
}

func TestNamespaceMetricsHandlesMissingReplicasAndPermissionDenial(t *testing.T) {
	app := core.AppChoice{Name:"api",Pods:[]string{"one", "two"}}
	m := newPickerModel([]core.AppChoice{app}, 1,"qa","apollo",nil)
	m.width,m.height = 160,28
	m.monitorEnabled,m.monitorGeneration = true,7
	m.metricsBusy = true
	scope := activityScope(m.kubeContext,m.namespace)
	m.metricsPods = map[string]kube.PodResourceUsage{"one":{CPUMilli: 50,CPUValid:true}}
	if _,ok := m.appResources(app); ok {
		t.Fatal("missing replica must not be reported as zero")
	}
	updated,cmd := m.Update(namespaceMetricsResultMsg{generation:7,scope:scope,at:time.Now(),err:errors.New("Forbidden")})
	m = updated.(pickerModel)
	if cmd != nil || m.metricsBusy || m.metricsPods != nil ||
		!strings.Contains(m.metricsSummary(),"unavailable") || strings.Contains(ansi.Strip(m.View()),"ISSUE") {
		t.Fatalf("metrics RBAC denial must be a visibility gap, not an application issue: %s",m.View())
	}
}

func TestNamespaceMetricsCancellationAndStaleResults(t *testing.T) {
	m := newPickerModel(nil,0,"qa","apollo",nil)
	m.monitorEnabled,m.monitorGeneration = true,3
	m.metricsBusy = true
	m.metricsPods=map[string]kube.PodResourceUsage{"old":{CPUMilli:400,CPUValid:true}}
	previous := m.metricsPods
	updated,cmd := m.Update(namespaceMetricsResultMsg{generation:2,scope:activityScope("qa","apollo"),
		metrics:kube.NamespaceResourceMetrics{Pods:map[string]kube.PodResourceUsage{"new":{CPUMilli:1}}}})
	m = updated.(pickerModel)
	if cmd != nil || m.metricsPods["old"].CPUMilli != previous["old"].CPUMilli {
		t.Fatal("stale metrics from previous monitoring generation accepted")
	}
	updated,_ = m.Update(namespaceMetricsResultMsg{generation:3,scope:activityScope("qa","other"),
		metrics:kube.NamespaceResourceMetrics{Pods:map[string]kube.PodResourceUsage{"new":{CPUMilli:1}}}})
	if updated.(pickerModel).metricsPods["new"].CPUMilli != 0 {
		t.Fatal("metrics from another namespace accepted")
	}
	m.podMonitorState.stop()
	if m.metricsPods != nil || m.metricsBusy || m.monitorEnabled {
		t.Fatal("stop must clear metrics and cancel polling")
	}
}

func TestMetricsPollIsIndependentOfLogScan(t *testing.T) {
	m := newPickerModel(nil,0,"qa","apollo",nil)
	m.monitorEnabled,m.monitorGeneration = true,4
	m.monitorContext = context.Background()
	m.monitorBusy = true // a log scan can overlap the independent metrics poll
	m.metricsBusy = false
	updated,cmd := m.Update(namespaceMetricsPollMsg{generation:4})
	m=updated.(pickerModel)
	if cmd == nil || !m.metricsBusy || !m.monitorBusy {
		t.Fatal("CPU/memory polling must not interfere with live log scan")
	}
	if len(cmd().(tea.BatchMsg)) != 2 {
		t.Fatal("idle metrics poll must schedule next poll and a concurrent read")
	}
}
