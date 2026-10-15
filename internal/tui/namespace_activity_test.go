package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/fernandocpaz/tailg/internal/core"
)

func namespaceActivityPicker() pickerModel {
	m := newPickerModel([]core.AppChoice{{Name: "api", Pods: []string{"api-1"}}, {Name: "worker", Pods: []string{"worker-1"}}}, 2, "tkgs-qa", "apollo", []string{"api"})
	m.width, m.height, m.index = 120, 30, 1
	now := time.Now()
	m.activity.tracker.Apply(core.NamespaceActivitySnapshot{At: now, Complete: map[string]bool{"events": true}, Events: []core.NamespaceEvent{{ID: "event-warning", Type: "Warning", Reason: "FailedScheduling", Object: "Pod/api", Message: strings.Repeat("long full message ", 100) + "END-OF-EVENT", Count: 7, FirstAt: now.Add(-time.Hour), LastAt: now}, {ID: "event-normal", Type: "Normal", Reason: "Started", Object: "Pod/worker", Message: "container started", Count: 1, LastAt: now.Add(-time.Minute)}}})
	m.activity.tracker.Record(core.NamespaceChange{ID: "image", At: now, Object: "Deployment/api", Action: "Image changed", Detail: "api:v1 → api:v2", Observed: true})
	return m
}
func activityKey(m pickerModel, key string) pickerModel {
	var msg tea.KeyMsg
	switch key {
	case "esc":
		msg.Type = tea.KeyEsc
	case "enter":
		msg.Type = tea.KeyEnter
	case "down":
		msg.Type = tea.KeyDown
	case "end":
		msg.Type = tea.KeyEnd
	case "home":
		msg.Type = tea.KeyHome
	case "space":
		msg.Type = tea.KeySpace
	default:
		msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)}
	}
	updated, _ := m.Update(msg)
	return updated.(pickerModel)
}
func TestNamespaceActivityModalsCaptureKeysAndPreservePicker(t *testing.T) {
	for _, key := range []string{"h", "e"} {
		m := activityKey(namespaceActivityPicker(), key)
		if m.activity.view == "" {
			t.Fatal("modal did not open")
		}
		for _, key := range []string{"down", "space", "c", "n", "x"} {
			m = activityKey(m, key)
		}
		if m.index != 1 || !m.checked["api"] || len(m.checked) != 1 || m.confirmRun != nil || m.result.Action != PickerQuit {
			t.Fatal("modal keys affected picker")
		}
		m = activityKey(m, "esc")
		if m.activity.view != "" || m.index != 1 || !m.checked["api"] {
			t.Fatal("close lost selection")
		}
	}
	m := activityKey(namespaceActivityPicker(), "h")
	m = activityKey(m, "e")
	if m.activity.view != "events" {
		t.Fatal("switch failed")
	}
	m = activityKey(m, "w")
	if len(m.namespaceActivityEntries()) != 1 {
		t.Fatal("warning filter did not hide Normal")
	}
	m = activityKey(m, "/")
	m = activityKey(m, "nonesuch")
	m = activityKey(m, "enter")
	if len(m.namespaceActivityEntries()) != 0 {
		t.Fatal("text filter failed")
	}
}
func TestNamespaceActivityDetailIsCompleteAndScrolls(t *testing.T) {
	m := activityKey(namespaceActivityPicker(), "e")
	m = activityKey(m, "enter")
	if m.activity.detail == nil || !strings.Contains(m.activity.detail.detail, "END-OF-EVENT") || !strings.Contains(m.activity.detail.detail, "Occurrences: 7") || !strings.Contains(m.activity.detail.detail, "tkgs-qa / apollo") {
		t.Fatal("detail lost fields or truncated message")
	}
	m = activityKey(m, "end")
	if m.activity.detailScroll == 0 || !strings.Contains(ansi.Strip(m.View()), "END-OF-EVENT") {
		t.Fatal("end of full message not reachable")
	}
	m = activityKey(m, "esc")
	if m.activity.detail != nil || m.activity.view != "events" {
		t.Fatal("Esc should first return to event list")
	}
	m = activityKey(m, "esc")
	if m.activity.view != "" {
		t.Fatal("Esc should return to picker")
	}
}
func TestNamespaceActivityModalFitsAndResizes(t *testing.T) {
	for _, size := range []tea.WindowSizeMsg{{Width: 40, Height: 12}, {Width: 80, Height: 24}, {Width: 140, Height: 40}, {Width: 24, Height: 8}} {
		for _, mode := range []string{"h", "e"} {
			m := activityKey(namespaceActivityPicker(), mode)
			updated, _ := m.Update(size)
			m = updated.(pickerModel)
			for _, detail := range []bool{false, true} {
				if detail {
					m = activityKey(m, "enter")
					m = activityKey(m, "end")
				}
				view := ansi.Strip(m.View())
				rows := strings.Split(view, "\n")
				if len(rows) != size.Height {
					t.Fatalf("size=%+v lines=%d", size, len(rows))
				}
				for _, row := range rows {
					if lipgloss.Width(row) != size.Width {
						t.Fatalf("size=%+v row width=%d: %q", size, lipgloss.Width(row), row)
					}
				}
				if !strings.Contains(view, "Esc") {
					t.Fatalf("close/back control hidden at %+v", size)
				}
			}
		}
	}
}
func TestNamespaceActivityRejectsStaleRepliesAndPreventsOverlappingLoads(t *testing.T) {
	m := namespaceActivityPicker()
	m.activity.request = 3
	m.activity.busy = true
	at := m.activity.tracker.LastSnapshot
	for _, message := range []namespaceActivityResult{{scope: activityScope("tkgs-dev", "apollo"), request: 3}, {scope: activityScope("tkgs-qa", "apollo"), request: 2}} {
		updated, _ := m.Update(message)
		m = updated.(pickerModel)
		if !m.activity.busy || m.activity.tracker.LastSnapshot != at {
			t.Fatal("stale reply changed current scope")
		}
	}
	if cmd := m.refreshNamespaceActivity(); cmd != nil {
		t.Fatal("overlapping request started")
	}
	m.refreshContext = context.Background()
	updated, cmd := m.Update(namespaceActivityResult{scope: activityScope("tkgs-qa", "apollo"), request: 3, snapshot: core.NamespaceActivitySnapshot{At: at.Add(time.Minute), Complete: map[string]bool{}}})
	m = updated.(pickerModel)
	if m.activity.busy || cmd == nil {
		t.Fatal("completion did not schedule poll")
	}
	updated, cmd = m.Update(namespaceActivityTick{scope: activityScope("tkgs-dev", "apollo")})
	m = updated.(pickerModel)
	if cmd != nil {
		t.Fatal("old scope poll loaded current scope")
	}
	updated, cmd = m.Update(namespaceActivityTick{scope: activityScope("tkgs-qa", "apollo"), request: 3})
	m = updated.(pickerModel)
	if cmd == nil || !m.activity.busy {
		t.Fatal("poll did not load")
	}
	// A refresh invalidates older scheduled ticks so manual R presses cannot
	// accumulate independent polling loops.
	updated, cmd = m.Update(namespaceActivityTick{scope: activityScope("tkgs-qa", "apollo"), request: 3})
	if cmd != nil {
		t.Fatal("stale scheduled tick created another poll loop")
	}
}
func TestNamespaceActivityKeepsPartialErrorsVisibleAndSanitizesMessages(t *testing.T) {
	m := namespaceActivityPicker()
	m.activity.view = "events"
	m.activity.filter = "no-match"
	m.activity.tracker.Errors = []string{"events: Forbidden"}
	rows := m.namespaceActivityEntries()
	if len(rows) != 1 || !strings.Contains(rows[0].detail, "Forbidden") {
		t.Fatal("permission error hidden by filter")
	}
	raw := "text\x1b[31mred\x1b[0m\x1b]52;c;clipboard\x07\r\x00\nnext"
	clean := activityText(raw)
	if strings.ContainsAny(clean, "\x1b\x07\r\x00") || !strings.Contains(clean, "\nnext") {
		t.Fatalf("terminal controls retained: %q", clean)
	}
}
func TestNamespaceActivityPreservesSelectionAsNewEventsArrive(t *testing.T) {
	m := namespaceActivityPicker()
	m.activity.view = "events"
	m.activity.selected = 1
	m.activity.request = 1
	m.activity.busy = true
	snapshot := core.NamespaceActivitySnapshot{At: time.Now(), Complete: map[string]bool{"events": true}, Events: append([]core.NamespaceEvent{{ID: "new", Type: "Warning", LastAt: time.Now().Add(time.Minute)}}, m.activity.tracker.Events...)}
	updated, _ := m.Update(namespaceActivityResult{scope: activityScope(m.kubeContext, m.namespace), request: 1, snapshot: snapshot})
	m = updated.(pickerModel)
	if m.activity.selected != 2 || m.namespaceActivityEntries()[m.activity.selected].id != "event/event-normal" {
		t.Fatal("new event moved selected entry")
	}
}
func TestNamespaceActivityRecordsObservedLogErrorMarkers(t *testing.T) {
	m := namespaceActivityPicker()
	m.monitorEnabled = true
	m.monitorGeneration = 1
	updated, _ := m.Update(podHealthScanMsg{generation: 1, scannedAt: time.Now(), samples: map[string]podHealthSample{"api-1": {errors: 3, lines: 8}, "worker-1": {lines: 4}}})
	m = updated.(pickerModel)
	found := false
	for _, change := range m.activity.tracker.Changes() {
		if change.Action == "Log errors observed" {
			found = true
			if !change.Observed || !strings.Contains(change.Detail, "3 errors") {
				t.Fatalf("bad marker: %+v", change)
			}
		}
	}
	if !found {
		t.Fatal("log error marker missing")
	}
}

func TestNamespaceActivityOpeningPersistsLoadingStateAndRefreshToken(t *testing.T) {
	m := namespaceActivityPicker()
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
	m = updated.(pickerModel)
	if cmd == nil || !m.activity.busy || m.activity.request != 1 {
		t.Fatal("load state not retained while opening modal")
	}
	updated, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	m = updated.(pickerModel)
	if cmd != nil || m.activity.request != 1 {
		t.Fatal("R started an overlapping load")
	}
	m.activity.busy = false
	updated, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	m = updated.(pickerModel)
	if cmd == nil || !m.activity.busy || m.activity.request != 2 {
		t.Fatal("manual refresh token not retained")
	}
}

func TestNamespaceActivityLongFilterKeepsCloseControlVisible(t *testing.T) {
	m := activityKey(namespaceActivityPicker(), "e")
	m.width = 40
	m.height = 12
	m.activity.filter = strings.Repeat("api", 100)
	view := ansi.Strip(m.View())
	if len(strings.Split(view, "\n")) != 12 || !strings.Contains(view, "Esc") {
		t.Fatal("long filter pushed controls out of viewport")
	}
}
