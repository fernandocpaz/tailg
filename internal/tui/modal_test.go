package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/fernandocpaz/tailg/internal/core"
)

func modalPicker() pickerModel {
	apps := []core.AppChoice{
		{Name: "api", Pods: []string{"api-1"}},
		{Name: "cronjob/monthly", CronJob: &core.CronJobInfo{
			Name: "monthly", Schedule: "0 4 1 * *", TimeZone: "America/New_York", Suspended: true, Active: 2,
		}},
	}
	m := newPickerModel(apps, len(apps), "tkgs-qa", "cronjob", []string{"api"})
	m.width, m.height, m.index = 100, 30, 1
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	return updated.(pickerModel)
}

func TestCronJobModalCapturesKeysAndPreservesSelectionOnCancel(t *testing.T) {
	m := modalPicker()
	for _, key := range []tea.KeyMsg{
		{Type: tea.KeyDown}, {Type: tea.KeySpace},
		{Type: tea.KeyRunes, Runes: []rune{'c'}},
		{Type: tea.KeyRunes, Runes: []rune{'r'}},
	} {
		updated, cmd := m.Update(key)
		m = updated.(pickerModel)
		if cmd != nil || m.index != 1 || len(m.checked) != 1 || !m.checked["api"] || m.confirmRun == nil {
			t.Fatalf("key %q reached underlying picker: %+v", key.String(), m)
		}
	}
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(pickerModel)
	if cmd != nil || m.confirmRun != nil || m.index != 1 || !m.checked["api"] {
		t.Fatalf("cancel lost list state: %+v", m)
	}
	if strings.Contains(m.View(), "Run CronJob now?") {
		t.Fatal("modal remains visible after cancellation")
	}
}

func TestCronJobModalEnterRunsOnlyAfterSelectingRun(t *testing.T) {
	for _, key := range []tea.KeyMsg{{Type: tea.KeyTab}, {Type: tea.KeyLeft}, {Type: tea.KeyShiftTab}} {
		m := modalPicker()
		updated, cmd := m.Update(key)
		m = updated.(pickerModel)
		if cmd != nil || !m.confirmRunFocused {
			t.Fatalf("key %q did not focus Run", key.String())
		}
		updated, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		result := updated.(pickerModel).result
		if cmd == nil || result.Action != PickerRunCronJob || result.App.CronJob.Name != "monthly" || strings.Join(result.Marked, ",") != "api" {
			t.Fatalf("wrong confirmed run: %+v", result)
		}
	}
}

func TestCronJobModalFitsResizedViewportsAndScrollsLongDetails(t *testing.T) {
	for _, size := range []tea.WindowSizeMsg{{Width: 40, Height: 12}, {Width: 80, Height: 24}, {Width: 140, Height: 40}} {
		m := modalPicker()
		m.confirmRun.CronJob.Name = strings.Repeat("long-cronjob-", 5)
		m.kubeContext = strings.Repeat("long-context-", 10)
		updated, _ := m.Update(size)
		m = updated.(pickerModel)
		view := core.StripANSI(m.View())
		rows := strings.Split(view, "\n")
		if len(rows) != size.Height {
			t.Fatalf("size=%+v: height=%d", size, len(rows))
		}
		for _, row := range rows {
			if lipgloss.Width(row) != size.Width {
				t.Fatalf("size=%+v: width=%d: %q", size, lipgloss.Width(row), row)
			}
		}
		for _, text := range []string{"Run CronJob now?", "[ Run now ]", "[ Cancel ]", "╭", "╯"} {
			if !strings.Contains(view, text) {
				t.Fatalf("size=%+v: missing %q in %q", size, text, view)
			}
		}
		if size.Width == 40 {
			updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnd})
			m = updated.(pickerModel)
			if m.confirmRunScroll == 0 || !strings.Contains(m.View(), "concurrency policy.") || m.index != 1 {
				t.Fatalf("long details did not scroll: %s", m.View())
			}
			updated, _ = m.Update(tea.WindowSizeMsg{Width: 140, Height: 40})
			m = updated.(pickerModel)
			if m.confirmRunScroll != 0 {
				t.Fatal("resize retained an invalid scroll offset")
			}
		}
	}
}

func TestCronJobModalCannotConfirmWhenTerminalIsTooSmall(t *testing.T) {
	m := modalPicker()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 30, Height: 8})
	m = updated.(pickerModel)
	for _, key := range []tea.KeyMsg{{Type: tea.KeyRunes, Runes: []rune{'y'}}, {Type: tea.KeyTab}, {Type: tea.KeyEnter}} {
		updated, cmd := m.Update(key)
		m = updated.(pickerModel)
		if cmd != nil || m.confirmRun == nil || m.result.Action == PickerRunCronJob {
			t.Fatal("hidden modal confirmed a create")
		}
	}
	if !strings.Contains(m.View(), "Resize terminal") {
		t.Fatal("no resize guidance")
	}
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd != nil || updated.(pickerModel).confirmRun != nil {
		t.Fatal("small modal cannot cancel")
	}
}

func TestModalOverlayCentersAndHandlesStyledWideCharacters(t *testing.T) {
	background := strings.Repeat("背景abcdefghij\n", 8)
	dialog := "╭────╮\n│Hi  │\n╰────╯"
	view := core.StripANSI(overlayModal("\x1b[31m"+background+"\x1b[0m", dialog, 20, 9))
	rows := strings.Split(view, "\n")
	if len(rows) != 9 || !strings.Contains(rows[3], "╭────╮") || lipgloss.Width(strings.Split(rows[3], "╭")[0]) != 7 {
		t.Fatalf("dialog is not centered: %q", view)
	}
	for _, row := range rows {
		if lipgloss.Width(row) != 20 {
			t.Fatalf("wide character shifted border: %q", row)
		}
	}
}
