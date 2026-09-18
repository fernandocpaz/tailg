package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/fernandocpaz/tailg/internal/core"
)

func TestReplicaExplanationOpensForSelectedPodAndWraps(t *testing.T) {
	m := workflowModel()
	m.width, m.height = 48, 12
	event := core.LogEvent{Pod: "orders-api-7d9-abcde", Container: "api", Message: "[12:00:00 INF] ready"}
	updated, _ := m.Update(logMsg(event))
	m = updated.(model)
	m.config.ExplainReplicas = func(context.Context, string) (core.ReplicaExplanation, error) {
		return core.ReplicaExplanation{
			Pod: event.Pod, Workload: "Deployment/orders-api",
			Summary: "HPA/orders-scale controls Deployment/orders-api and currently requests 5 replicas within its 2-10 range.",
			Details: []string{"desired 5, current 4, ready 4.", "HPA condition: ScalingActive=True (ValidMetricFound)."},
		}, nil
	}
	updated, command := m.Update(tea.KeyMsg{Type: tea.KeyF7})
	m = updated.(model)
	if !m.replicas.open || command == nil || m.replicas.pod != event.Pod {
		t.Fatalf("replica panel was not opened for selected pod: %+v", m.replicas)
	}
	updated, _ = m.Update(command())
	m = updated.(model)
	view := m.View()
	for _, expected := range []string{"Why replicas?", event.Pod, "Deployment/orders-api", "requests 5"} {
		if !strings.Contains(view, expected) {
			t.Fatalf("replica view missing %q: %s", expected, view)
		}
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	m = updated.(model)
	if !strings.Contains(m.View(), "ScalingActive=True") {
		t.Fatalf("replica detail could not be reached by paging: %s", m.View())
	}
	if lines := strings.Split(view, "\n"); len(lines) != m.height {
		t.Fatalf("replica view height = %d, want %d", len(lines), m.height)
	}
	for _, line := range strings.Split(view, "\n") {
		if lipgloss.Width(line) > m.width {
			t.Fatalf("replica line exceeded width: %q", line)
		}
	}
}

func TestReplicaSummaryRefreshAndStaleResult(t *testing.T) {
	m := workflowModel()
	m.items = []core.InventoryItem{{Pod: "only-pod", Container: "api"}}
	m.config.ExplainReplicas = func(context.Context, string) (core.ReplicaExplanation, error) {
		return core.ReplicaExplanation{Summary: "Deployment/api requests 3 replicas."}, nil
	}
	command := m.ensureReplicaLookup(time.Now())
	if command == nil || !strings.Contains(m.renderLogSource(), "checking workload") {
		t.Fatal("single-pod replica lookup did not start")
	}
	updated, _ := m.Update(replicaMsg{pod: "old-pod", generation: m.replicas.generation, info: core.ReplicaExplanation{Summary: "wrong"}})
	m = updated.(model)
	if m.replicas.info.Summary != "" {
		t.Fatal("stale replica result was accepted")
	}
	updated, _ = m.Update(command())
	m = updated.(model)
	if source := m.renderLogSource(); !strings.Contains(source, "requests 3 replicas") || !strings.Contains(source, "F7 details") {
		t.Fatalf("source lacks replica explanation: %s", source)
	}
}

func TestReplicaShortcutRequiresKnownPodInMultiPodView(t *testing.T) {
	m := workflowModel()
	m.items = []core.InventoryItem{{Pod: "a", Container: "api"}, {Pod: "b", Container: "api"}}
	m.config.ExplainReplicas = func(context.Context, string) (core.ReplicaExplanation, error) { return core.ReplicaExplanation{}, nil }
	if command := m.openReplicaExplanation(); command != nil || m.replicas.open || !strings.Contains(m.notice, "Select a log") {
		t.Fatalf("ambiguous replica lookup was started: %+v", m.replicas)
	}
}
