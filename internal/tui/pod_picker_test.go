package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/fernandocpaz/tailg/internal/core"
)

func TestPodPickerSelectsExactPodsAcrossApplications(t *testing.T) {
	rows := preparePodPickerRows([]core.AppChoice{
		{Name: "worker", Pods: []string{"worker-2", "worker-1"}},
		{Name: "api", PodChoices: []core.PodChoice{{Name: "api-1", Ready: "1/1", Phase: "Running"}}},
	})
	m := podPickerModel{rows: rows, kubeContext: "tkgs-dev", namespace: "apollo"}
	if len(rows) != 3 || rows[0].pod.Name != "api-1" || rows[1].pod.Name != "worker-1" {
		t.Fatalf("unexpected rows: %+v", rows)
	}
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeySpace})
	m = updated.(podPickerModel)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(podPickerModel)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeySpace})
	m = updated.(podPickerModel)
	if !strings.Contains(m.View(), "2 pods · pinned") {
		t.Fatalf("missing pinned count: %q", m.View())
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if got := strings.Join(updated.(podPickerModel).selected, ","); got != "api-1,worker-1" {
		t.Fatalf("selected %q", got)
	}
}

func TestPodPickerTogglesWholeAppAndCancels(t *testing.T) {
	m := podPickerModel{rows: preparePodPickerRows([]core.AppChoice{{Name: "api", Pods: []string{"api-1", "api-2"}}})}
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	m = updated.(podPickerModel)
	if m.checkedCount() != 2 {
		t.Fatalf("app toggle selected %d pods", m.checkedCount())
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	m = updated.(podPickerModel)
	if m.checkedCount() != 0 {
		t.Fatal("app toggle should uncheck all pods")
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if updated.(podPickerModel).selected != nil {
		t.Fatal("Esc should return without pod selection")
	}
}

func TestPickerShowsSidecarsOnUnhighlightedRows(t *testing.T) {
	apps := []core.AppChoice{
		{Name: "api", Pods: []string{"api-1"}},
		{Name: "worker", PodChoices: []core.PodChoice{
			{Name: "worker-1", Sidecars: []string{"logger"}},
			{Name: "worker-2", Sidecars: []string{"istio-proxy", "logger"}},
		}},
	}
	for _, tc := range []struct {
		name      string
		width     int
		workload  string
		podLabels []string
	}{
		{"wide", 100, "worker 2", []string{"worker-1 · Sidecars: logger", "worker-2 · Sidecars: istio-proxy, logger"}},
		{"narrow", 40, "worker [2 sc]", []string{"worker-1 [1 sc]", "worker-2 [2 sc]"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newPickerModel(apps, len(apps), "tkgs-qa", "apollo", nil)
			m.width, m.height = tc.width, 24
			view := ansi.Strip(m.View())
			if !strings.Contains(strings.Join(strings.Fields(view), " "), tc.workload) {
				t.Fatalf("workload row missing sidecar count:\n%s", view)
			}
			m = pickerTestKey(m, "p")
			if m.selection == nil || m.selection.pods == nil {
				t.Fatal("exact pod modal did not open")
			}
			view = ansi.Strip(m.View())
			for _, label := range tc.podLabels {
				if !strings.Contains(view, label) {
					t.Errorf("pod row missing %q:\n%s", label, view)
				}
			}
			assertPickerFits(t, m)
		})
	}
}
