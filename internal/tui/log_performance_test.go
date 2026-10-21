package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/fernandocpaz/tailg/internal/core"
)

// High-volume streams must be delivered in bounded batches, in original order.
func TestWaitForLogBatchBoundedAndOrdered(t *testing.T) {
	events := make(chan core.LogEvent, 256)
	for index := 0; index < 200; index++ {
		events <- core.LogEvent{Message: fmt.Sprintf("line-%d", index)}
	}
	first, ok := waitForLogBatch(events)().(logBatchMsg)
	if !ok || len(first) != logBatchMaxEvents {
		t.Fatalf("first batch length = %d, want %d", len(first), logBatchMaxEvents)
	}
	for index, event := range first {
		if event.Message != fmt.Sprintf("line-%d", index) {
			t.Fatalf("batch event %d = %q", index, event.Message)
		}
	}
	close(events)
	second, ok := waitForLogBatch(events)().(logBatchMsg)
	if !ok || len(second) != 200-logBatchMaxEvents {
		t.Fatalf("second batch length = %d, want %d", len(second), 200-logBatchMaxEvents)
	}
	for index, event := range second {
		if event.Message != fmt.Sprintf("line-%d", index+logBatchMaxEvents) {
			t.Fatalf("second batch event %d = %q", index, event.Message)
		}
	}
}

// A batch must retain reconnect/issue semantics and discard replayed records.
func TestProcessLogBatchPreservesStreamLifecycle(t *testing.T) {
	m := model{
		state: core.NewFilterState(100),
		heartbeat: &core.HeartbeatAnalyzer{},
		config: Config{Formatter: core.Formatter{}},
		followsLive: true,
	}
	m.processLogBatch([]core.LogEvent{
		{Pod: "pod-a", Container: "app", Started: true},
		{Pod: "pod-a", Container: "app", Message: "first"},
		{Pod: "pod-a", Container: "app", Message: "duplicate", Replayed: true},
		{Pod: "pod-a", Container: "app", Closed: true},
		{Pod: "pod-a", Container: "app", Message: "second"},
	})
	if m.isReconnecting() {
		t.Fatal("a later log in the same batch should clear reconnect state")
	}
	got := strings.Join(m.state.Lines(), "|")
	if !strings.Contains(got, "first") || !strings.Contains(got, "second") || strings.Contains(got, "duplicate") {
		t.Fatalf("unexpected batched logs: %q", got)
	}
	if m.selected != m.state.VisibleCount()-1 {
		t.Fatalf("selected=%d, want newest=%d", m.selected, m.state.VisibleCount()-1)
	}
}

func TestRenderLargeTimelineKeepsViewportAndSelectedRow(t *testing.T) {
	const count = 50_000
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	records := make([]core.LogRecord, count)
	for index := range records {
		records[index] = timelineTestRecord(fmt.Sprintf("message-%d", index), now.Add(-time.Duration(count-index)*time.Second))
	}
	latest := strings.Join(renderLogTimelineRows(records, "", count-1, 32, 120, false, false, true, now), "\n")
	if !strings.Contains(latest, "message-49999") || strings.Contains(latest, "message-100") {
		t.Fatalf("live viewport not bounded to newest rows: %q", latest)
	}
	selected := strings.Join(renderLogTimelineRows(records, "", 50, 32, 120, false, false, false, now), "\n")
	if !strings.Contains(selected, "message-50") || strings.Contains(selected, "message-49999") {
		t.Fatalf("paused viewport lost selected entry: %q", selected)
	}
}

func BenchmarkRenderTimeline50000(b *testing.B) {
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	records := make([]core.LogRecord, 50_000)
	for index := range records {
		records[index] = timelineTestRecord("regular INFO request completed successfully", now.Add(-time.Duration(index)*time.Second))
	}
	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		_ = renderLogTimelineRows(records, "", 49_999, 40, 120, false, false, true, now)
	}
}
