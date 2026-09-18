package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/fernandocpaz/tailg/internal/core"
)

func TestRenderLogSourceUsesSelectedRecord(t *testing.T) {
	m := model{width: 80, selected: 0, state: core.NewFilterState(10), items: []core.InventoryItem{{Pod: "inventory-pod", Container: "api"}}}
	m.state.AppendRecords(core.LogRecord{Event: core.LogEvent{Pod: "selected-pod-abcde", Container: "worker"}, Text: "line"})
	got := m.renderLogSource()
	if !strings.Contains(got, "Pod: selected-pod-abcde") || !strings.Contains(got, "Container: worker") {
		t.Fatalf("source %q does not identify selected record", got)
	}
}

func TestRenderLogSourceWaitingAndAmbiguousInventory(t *testing.T) {
	one := model{width: 80, selected: -1, items: []core.InventoryItem{{Pod: "only-pod", Container: "api"}}}
	if got := one.renderLogSource(); got != "Pod: only-pod  Container: api" {
		t.Fatalf("single inventory source = %q", got)
	}
	many := model{width: 80, selected: -1, items: []core.InventoryItem{{Pod: "pod-a", Container: "api"}, {Pod: "pod-b", Container: "api"}}}
	if got := many.renderLogSource(); got != "Select a log to see source" {
		t.Fatalf("ambiguous inventory source = %q", got)
	}
}

func TestRenderLogSourceWrapsWithoutTruncating(t *testing.T) {
	m := model{width: 20, selected: 0, state: core.NewFilterState(10)}
	m.state.AppendRecords(core.LogRecord{Event: core.LogEvent{Pod: "workload-123456789", Container: "container"}, Text: "line"})
	got := m.renderLogSource()
	joined := strings.Join(strings.Fields(got), "")
	if strings.Contains(got, "…") || !strings.Contains(joined, "Pod:workload-123456789") || !strings.Contains(joined, "Container:container") {
		t.Fatalf("wrapped source lost content: %q", got)
	}
}

func TestCompactPodLabelPreservesPrefixAndReplica(t *testing.T) {
	got := compactPodLabel("orders-api-7d9-abcde", 12)
	if !strings.Contains(got, "order") || !strings.Contains(got, "abcde") {
		t.Fatalf("compact label %q lost identity", got)
	}
	unicode := compactPodLabel("注文サービス-7d9-abcde", 12)
	if gotWidth := lipgloss.Width(unicode); gotWidth > 12 {
		t.Fatalf("unicode compact label width %d: %q", gotWidth, unicode)
	}
}
