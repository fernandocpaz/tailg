package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/fernandocpaz/tailg/internal/core"
)

func pickerViewportFixture() []core.AppChoice {
	var apps []core.AppChoice
	for i := 0; i < 10; i++ {
		apps = append(apps, core.AppChoice{Name: fmt.Sprintf("app-%02d", i), Ready: "1/1", Phases: "Running"})
	}
	for _, name := range []string{"encounterjsonaudit", "pendingorderalerts", "auditlistener"} {
		jobName := "vitas-cronjob-" + name
		apps = append(apps, core.AppChoice{Name: "cronjob/" + jobName, CronJob: &core.CronJobInfo{Name: jobName, Schedule: "*/5 * * * *", TimeZone: "America/New_York"}})
	}
	return apps
}

func TestPickerShowsLastCronJobBeforeItIsSelected(t *testing.T) {
	apps := pickerViewportFixture()
	m := newPickerModel(apps, len(apps), "tkgs-qa", "apollo", nil)
	// Thirteen workloads plus both section headers fit. The old fixed eight-line
	// reservation incorrectly limited this window to twelve workloads.
	m.width, m.height = 160, 26
	for _, selected := range []int{0, 9, 10, 11, 12} {
		m.index = selected
		view := core.StripANSI(m.View())
		for _, app := range apps {
			name := app.Name
			if app.CronJob != nil {
				name = app.CronJob.Name
			}
			if !strings.Contains(view, name) {
				t.Fatalf("selected=%d: missing %s:\n%s", selected, name, view)
			}
		}
		if strings.Contains(view, "↑↓ to scroll") {
			t.Fatal("scroll hint displayed despite all rows fitting")
		}
		assertPickerFits(t, m)
	}
}

func TestPickerScrollsOnlyWhenNeededAndFitsWrappedLines(t *testing.T) {
	for _, width := range []int{80, 120, 160} {
		for _, height := range []int{24, 26, 40} {
			m := newPickerModel(pickerViewportFixture(), 13, "tkgs-qa", "apollo", nil)
			m.width, m.height, m.monitorEnabled = width, height, true
			m.checked = map[string]bool{"app-00": true}
			m.note = "Inventory refresh completed; select a CronJob and press X to run it on demand."
			m.loadError = "partial inventory could not be loaded"
			for selected := range m.apps {
				m.index = selected
				view := core.StripANSI(m.View())
				name := m.apps[selected].Name
				if m.apps[selected].CronJob != nil {
					name = m.apps[selected].CronJob.Name
				}
				// Long fixed-width cells can wrap within the name on narrow terminals.
				if !strings.Contains(strings.ReplaceAll(view, "\n", ""), name) {
					t.Fatalf("cursor row missing: %s", name)
				}
				assertPickerFits(t, m)
			}
		}
	}
}

func TestPickerResizeRevealsAllCronJobsWithoutMovingCursor(t *testing.T) {
	apps := pickerViewportFixture()
	m := newPickerModel(apps, len(apps), "tkgs-qa", "apollo", nil)
	m.width, m.height, m.index = 160, 20, 9
	if !strings.Contains(core.StripANSI(m.View()), "↑↓ to scroll") {
		t.Fatal("short viewport must indicate hidden rows")
	}
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 160, Height: 26})
	m = updated.(pickerModel)
	view := core.StripANSI(m.View())
	if m.index != 9 || !strings.Contains(view, "vitas-cronjob-auditlistener") || strings.Contains(view, "↑↓ to scroll") {
		t.Fatalf("resize left a CronJob hidden:\n%s", view)
	}
	assertPickerFits(t, m)
}

func assertPickerFits(t *testing.T, m pickerModel) {
	t.Helper()
	lines := strings.Split(core.StripANSI(m.View()), "\n")
	if len(lines) > m.height {
		t.Fatalf("view uses %d lines in %dx%d terminal:\n%s", len(lines), m.width, m.height, strings.Join(lines, "\n"))
	}
	for _, line := range lines {
		if lipgloss.Width(line) > m.width {
			t.Fatalf("line exceeds terminal width: %q", line)
		}
	}
}
