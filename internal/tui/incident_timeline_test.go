package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/fernandocpaz/tailg/internal/core"
)

func incidentTimelinePicker() pickerModel {
	m := namespaceActivityPicker()
	now := time.Now()
	m.activity.tracker.Apply(core.NamespaceActivitySnapshot{
		At: now,
		Complete: map[string]bool{"pods": true, "deployments": true, "events": true},
		Objects: []core.NamespaceObject{
			{Kind: "Deployment", Resource: "deployments", Name: "api", UID: "deployment-uid"},
			{Kind: "Pod", Resource: "pods", Name: "api-1", UID: "pod-uid", Owner: "Deployment/api"},
		},
		Events: []core.NamespaceEvent{
			{ID: "warning", Type: "Warning", Reason: "BackOff", Object: "Pod/api-1",
				Count: 2, FirstAt: now.Add(-time.Minute), LastAt: now},
			{ID: "normal", Type: "Normal", Reason: "Started", Object: "Pod/api-1",
				LastAt: now.Add(-time.Second)},
		},
	})
	m.activity.tracker.Record(core.NamespaceChange{
		ID: "rollout", Object: "Deployment/api", Action: "Rollout revision created",
		Detail: "revision 12", At: now.Add(-3 * time.Minute),
	})
	return m
}

func TestIncidentTimelineKeyMergesSignalsAndReturnsToPicker(t *testing.T) {
	m := activityKey(incidentTimelinePicker(), "i")
	if m.activity.view != "timeline" || !strings.Contains(ansi.Strip(m.View()), "Incident timeline") {
		t.Fatal("I did not open integrated timeline")
	}
	rows := m.namespaceActivityEntries()
	var index = -1
	for i, row := range rows {
		if strings.Contains(row.heading, "BackOff") {
			index = i
			break
		}
	}
	if index == -1 {
		t.Fatalf("warning missing from integrated timeline: %+v", rows)
	}
	m.activity.selected = index
	m = activityKey(m, "enter")
	if m.activity.detail == nil {
		t.Fatal("warning detail not opened")
	}
	for _, expected := range []string{
		"Occurrences: 2", "Rollout revision created", "Deployment/api",
		"not proof of causation", "first observed signal",
	} {
		if !strings.Contains(m.activity.detail.detail, expected) {
			t.Fatalf("missing evidence label %q: %s", expected, m.activity.detail.detail)
		}
	}
	m = activityKey(m, "esc")
	if m.activity.view != "timeline" || m.activity.detail != nil {
		t.Fatal("Esc should return from detail to timeline")
	}
	m = activityKey(m, "w")
	hasNormal := false
	for _, row := range m.namespaceActivityEntries() {
		hasNormal = hasNormal || strings.Contains(row.heading, "Started")
	}
	if !hasNormal {
		t.Fatal("W did not include Normal Kubernetes events")
	}
	m = activityKey(m, "h")
	if m.activity.view != "changes" {
		t.Fatal("H did not switch to changes")
	}
	m = activityKey(m, "e")
	if m.activity.view != "events" {
		t.Fatal("E did not switch to events")
	}
	m = activityKey(m, "i")
	m = activityKey(m, "esc")
	if m.activity.view != "" || m.index != 1 || !m.checked["api"] {
		t.Fatal("closing timeline modified picker selection")
	}
}

func TestIncidentTimelineKeepsLogErrorMarkersAndIncompleteCoverage(t *testing.T) {
	m := incidentTimelinePicker()
	now := time.Now()
	m.activity.tracker.Record(core.NamespaceChange{
		ID: "errors", At: now, Object: m.activity.tracker.IncidentWorkloadObject("api"),
		Action: "Log errors observed", Detail: "5 errors in preceding minute", Observed: true,
	})
	m.activity.tracker.Errors = []string{"events: forbidden"}
	m = activityKey(m, "i")
	rows := m.namespaceActivityEntries()
	var logEntry, incomplete bool
	for _, row := range rows {
		logEntry = logEntry || (strings.Contains(row.heading, "LOG") && strings.Contains(row.detail, "5 errors"))
		incomplete = incomplete || row.id == "collection-errors"
	}
	if !logEntry || !incomplete {
		t.Fatalf("timeline hid scan marker or partial collection: %+v", rows)
	}
}

func TestIncidentTimelineModalRespectsTerminalSizes(t *testing.T) {
	for _, size := range []tea.WindowSizeMsg{
		{Width: 40, Height: 12}, {Width: 80, Height: 24},
		{Width: 140, Height: 40}, {Width: 24, Height: 8},
	} {
		m := activityKey(incidentTimelinePicker(), "i")
		model, _ := m.Update(size)
		m = model.(pickerModel)
		for _, inDetail := range []bool{false, true} {
			if inDetail {
				m = activityKey(m, "enter")
			}
			view := ansi.Strip(m.View())
			lines := strings.Split(view, "\n")
			if len(lines) != size.Height {
				t.Fatalf("height %d != %d for %+v", len(lines), size.Height, size)
			}
			for _, line := range lines {
				if lipgloss.Width(line) != size.Width {
					t.Fatalf("width %d != %d for %+v: %q", lipgloss.Width(line), size.Width, size, line)
				}
			}
			if !strings.Contains(view, "Esc") {
				t.Fatalf("close control missing at %+v", size)
			}
		}
	}
}
