package tui

import (
	"context"
	"regexp"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/fernandocpaz/tailg/internal/core"
)

func TestFreshnessDistinguishesRecentReceiptFromOldLogsAndQuiet(t *testing.T) {
	now := time.Date(2026, 9, 9, 20, 0, 0, 0, time.UTC)
	item := core.InventoryItem{Pod: "pod-1", Container: "api"}
	m := model{items: []core.InventoryItem{item}}
	if m.freshnessStatus(item, now) != "WAITING" {
		t.Fatal("unconfirmed stream should wait for data")
	}
	m.recordFreshness(core.LogEvent{Pod: item.Pod, Container: item.Container, ReceivedAt: now, ObservedAt: now.Add(-time.Hour)})
	if got := m.freshnessSummary(now); !strings.Contains(got, "recv 0s") || !strings.Contains(got, "log 1h0m0s") {
		t.Fatalf("old logs appear fresh: %q", got)
	}
	if got := m.freshnessStatus(item, now.Add(time.Minute)); got != "QUIET" {
		t.Fatalf("idle stream state = %q", got)
	}
	if m.isReconnecting() {
		t.Fatal("quiet stream was marked disconnected")
	}
	m.recordFreshness(core.LogEvent{Pod: item.Pod, Container: item.Container, Started: true})
	if m.freshnessStatus(item, now) != "WAITING" {
		t.Fatal("starting kubectl should not claim remote connectivity")
	}
	if got := freshnessAge(now.Add(time.Minute), now); !strings.HasPrefix(got, "clock+") {
		t.Fatalf("future clock timestamp = %q", got)
	}
}

func TestFreshnessObservesFilteredLogsAndDoesNotCountReplays(t *testing.T) {
	now := time.Now()
	m := model{state: core.NewFilterState(10), heartbeat: &core.HeartbeatAnalyzer{}, followsLive: true,
		config: Config{Formatter: core.Formatter{Exclude: []*regexp.Regexp{regexp.MustCompile("health")}}}}
	update := func(event core.LogEvent) {
		updated, _ := m.Update(logMsg(event))
		m = updated.(model)
	}
	update(core.LogEvent{Pod: "p", Container: "c", Message: "health ok", ReceivedAt: now, ObservedAt: now})
	if used, _ := m.state.BufferUsage(); used != 0 || m.freshness[streamKey("p", "c")].lastReceived.IsZero() {
		t.Fatal("filtered activity was ignored or shown as a log")
	}
	errorEvent := core.LogEvent{Pod: "p", Container: "c", Message: "[20:00:00 ERR] request failed", ReceivedAt: now, ObservedAt: now}
	update(errorEvent)
	errorEvent.Replayed = true
	update(errorEvent)
	if used, _ := m.state.BufferUsage(); used != 1 || m.issues.Stats(now, time.Minute).Events != 1 {
		t.Fatal("replayed log inflated the buffer or issue count")
	}
	if m.freshness[streamKey("p", "c")].replayed != 1 {
		t.Fatal("replay count missing from stream status")
	}
}

func TestFreshnessPanelKeepsStreamsSeparateAndPrunesRemovedPods(t *testing.T) {
	now := time.Now()
	a := core.InventoryItem{Pod: "pod-a", Container: "api"}
	b := core.InventoryItem{Pod: "pod-b", Container: "worker"}
	m := model{items: []core.InventoryItem{a, b}, state: core.NewFilterState(10), width: 100, height: 12}
	m.recordFreshness(core.LogEvent{Pod: a.Pod, Container: a.Container, ReceivedAt: now.Add(-time.Minute), ObservedAt: now.Add(-time.Minute)})
	m.recordFreshness(core.LogEvent{Pod: b.Pod, Container: b.Container, ReceivedAt: now, ObservedAt: now})
	if m.freshnessStatus(a, now) != "QUIET" || m.freshnessStatus(b, now) != "RECEIVING" {
		t.Fatal("busy pod hid the quiet stream")
	}
	for _, width := range []int{60, 100, 160} {
		m.width = width
		view := m.renderFreshness(now)
		for _, line := range strings.Split(view, "\n") {
			if lipgloss.Width(line) > width {
				t.Fatalf("freshness panel exceeds width %d: %q", width, line)
			}
		}
		if !strings.Contains(view, "pod-a") || !strings.Contains(view, "pod-b") {
			t.Fatalf("panel omitted stream identities: %q", view)
		}
	}
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyF4})
	m = updated.(model)
	if !m.freshnessOpen {
		t.Fatal("F4 did not open stream details")
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(model)
	if m.freshnessOpen {
		t.Fatal("Escape did not close stream details")
	}
	m.markReconnecting(a.Pod, a.Container)
	updated, _ = m.Update(inventoryMsg{items: []core.InventoryItem{b}})
	m = updated.(model)
	if len(m.freshnessItems()) != 1 || m.isReconnecting() || strings.Contains(m.renderFreshness(now), "pod-a") {
		t.Fatal("removed stream remained in the freshness panel")
	}
}

func TestManageStreamsRetainsIndependentCursorsOnReconnect(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	items := []core.InventoryItem{{Pod: "a", Container: "api"}, {Pod: "b", Container: "api"}}
	base := time.Date(2026, 9, 9, 20, 0, 0, 0, time.UTC)
	resumed := make(chan string, 2)
	config := Config{Items: items, Stream: func(ctx context.Context, item core.InventoryItem, cursor *core.LogCursor, _ chan<- core.LogEvent) error {
		at := base
		if item.Pod == "b" {
			at = base.Add(time.Minute)
		}
		if cursor.Resume().Since.IsZero() {
			cursor.Observe(core.LogEvent{ObservedAt: at, Message: item.Pod})
			return nil
		}
		if !cursor.Resume().Since.Equal(at.Add(-time.Second)) {
			resumed <- "wrong cursor"
		} else {
			resumed <- item.Pod
		}
		<-ctx.Done()
		return ctx.Err()
	}}
	done := make(chan struct{})
	go func() {
		manageStreams(ctx, config, make(chan core.LogEvent), nil)
		close(done)
	}()
	seen := map[string]bool{}
	for len(seen) < 2 {
		select {
		case key := <-resumed:
			if key != "a" && key != "b" {
				t.Fatal(key)
			}
			seen[key] = true
		case <-time.After(2 * time.Second):
			t.Fatal("streams did not reconnect")
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("stream manager did not stop")
	}
}
