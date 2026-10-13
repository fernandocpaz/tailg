package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/fernandocpaz/tailg/internal/core"
)

func TestPickerRetainsAllCronJobsBeyondTwentyApplications(t *testing.T) {
	var apps []core.AppChoice
	for i := 0; i < 30; i++ {
		apps = append(apps, core.AppChoice{Name: fmt.Sprintf("app-%02d", i), DeployedAt: time.Now()})
		name := fmt.Sprintf("job-%02d", i)
		apps = append(apps, core.AppChoice{Name: "cronjob/" + name, CronJob: &core.CronJobInfo{Name: name, Schedule: "0 4 1 * *"}})
	}
	prepared, total := preparePickerApps(apps)
	if total != 60 || len(prepared) != 50 {
		t.Fatalf("total=%d len=%d", total, len(prepared))
	}
	m := newPickerModel(prepared, total, "tkgs-qa", "cronjob", nil)
	m.height, m.index = 24, 49
	view := core.StripANSI(m.View())
	for _, want := range []string{"job-29", "SCHEDULE", "0 4 1 * *", "controller local", "never", "↑↓ to scroll"} {
		if !strings.Contains(view, want) {
			t.Fatalf("missing %q in %q", want, view)
		}
	}
	if strings.Contains(view, "app-00") || len(strings.Split(view, "\n")) > m.height {
		t.Fatalf("picker did not scroll to fit: %q", view)
	}
}

func TestPodlessCronJobCannotOpenOrCheckNamespaceLogs(t *testing.T) {
	app := core.AppChoice{Name: "cronjob/weekly", CronJob: &core.CronJobInfo{Name: "weekly"}}
	for _, key := range []tea.KeyMsg{{Type: tea.KeyEnter}, {Type: tea.KeySpace}} {
		m := newPickerModel([]core.AppChoice{app}, 1, "qa", "jobs", []string{app.Name})
		updated, cmd := m.Update(key)
		m = updated.(pickerModel)
		if cmd != nil || m.result.Action == PickerOpenApp || len(m.checked) != 0 || !strings.Contains(m.note, "no retained pods") {
			t.Fatalf("unsafe podless selection: %+v", m)
		}
	}
}

func TestRunCronJobRequiresConfirmationAndUsesHighlightedRow(t *testing.T) {
	apps := []core.AppChoice{{Name: "api", Pods: []string{"api-1"}}, {Name: "cronjob/weekly", CronJob: &core.CronJobInfo{Name: "weekly", Suspended: true, Active: 1}}}
	m := newPickerModel(apps, 2, "tkgs-qa", "cronjob", []string{"api"})
	m.index = 1
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	m = updated.(pickerModel)
	if cmd != nil || m.confirmRun == nil || m.result.Action == PickerRunCronJob {
		t.Fatalf("run triggered before confirmation: %+v", m)
	}
	m.width, m.height = 100, 30
	if view := m.View(); !strings.Contains(view, "tkgs-qa / cronjob") || !strings.Contains(view, "concurrency policy") {
		t.Fatalf("missing confirmation scope: %q", view)
	}
	updated, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(pickerModel)
	if cmd != nil || m.result.Action == PickerRunCronJob {
		t.Fatal("Enter incorrectly confirmed a create")
	}
	if m.confirmRun != nil {
		t.Fatal("Enter on the default Cancel button did not close the modal")
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	m = updated.(pickerModel)
	updated, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	m = updated.(pickerModel)
	if cmd != nil || m.confirmRun != nil || !m.checked["api"] {
		t.Fatal("cancel lost picker state")
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	updated, cmd = updated.(pickerModel).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	m = updated.(pickerModel)
	if cmd == nil || m.result.Action != PickerRunCronJob || m.result.App.CronJob.Name != "weekly" || strings.Join(m.result.Marked, ",") != "api" {
		t.Fatalf("wrong run scope: %+v", m.result)
	}
}

func TestCronJobWithRetainedPodsStillOpensItsLogs(t *testing.T) {
	app := core.AppChoice{Name: "cronjob/weekly", Pods: []string{"weekly-run-1"}, CronJob: &core.CronJobInfo{Name: "weekly"}}
	m := newPickerModel([]core.AppChoice{app}, 1, "qa", "jobs", nil)
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if result := updated.(pickerModel).result; cmd == nil || result.Action != PickerOpenApp || len(result.App.Pods) != 1 {
		t.Fatalf("cannot open retained logs: %+v", result)
	}
}
