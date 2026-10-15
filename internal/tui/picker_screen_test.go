package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

func TestPickerFillsViewportAndAnchorsActions(t *testing.T) {
	for _, size := range []tea.WindowSizeMsg{{Width: 24, Height: 8}, {Width: 40, Height: 12}, {Width: 80, Height: 24}, {Width: 120, Height: 30}, {Width: 200, Height: 50}} {
		for _, populated := range []bool{false, true} {
			t.Run(fmt.Sprintf("%dx%d/populated=%t", size.Width, size.Height, populated), func(t *testing.T) {
				m := newPickerModel(nil, 0, "tkgs-qa", "apollo", nil)
				if populated {
					m.apps = pickerViewportFixture()
					m.totalApps = len(m.apps)
				}
				updated, _ := m.Update(size)
				m = updated.(pickerModel)
				view := ansi.Strip(m.View())
				lines := strings.Split(view, "\n")
				if len(lines) != size.Height {
					t.Fatalf("got %d lines, want %d", len(lines), size.Height)
				}
				for _, line := range lines {
					if lipgloss.Width(line) != size.Width {
						t.Fatalf("line width = %d, want %d: %q", lipgloss.Width(line), size.Width, line)
					}
				}
				if !strings.Contains(lines[len(lines)-1], "Q Quit") {
					t.Fatalf("quit action is not pinned to bottom: %q", lines[len(lines)-1])
				}
				if !strings.Contains(view, "? Help") {
					t.Fatal("help action hidden")
				}
			})
		}
	}
}

func TestPickerGroupedHeaderAndContextualRun(t *testing.T) {
	m := newPickerModel(pickerViewportFixture(), 13, "tkgs-qa", "apollo", nil)
	m.width, m.height = 160, 26
	view := ansi.Strip(m.View())
	for _, label := range []string{"C Context: tkgs-qa", "N Namespace: apollo", "INSPECT", "H Changes", "E Events", "WATCH", "M Monitor: OFF", "Space Mark", "P Exact pods"} {
		if !strings.Contains(view, label) {
			t.Fatalf("missing %q:\n%s", label, view)
		}
	}
	if strings.Contains(view, "X Run now") {
		t.Fatal("application row should not advertise a CronJob action")
	}
	m.index = 12
	if !strings.Contains(ansi.Strip(m.View()), "X Run now") {
		t.Fatal("CronJob action missing")
	}
}

func TestPickerResponsiveColumnsStayOnOneLine(t *testing.T) {
	// Include every threshold and wide characters to catch cell-width errors.
	for width := 24; width <= 200; width++ {
		for _, job := range []bool{false, true} {
			values := map[string]string{"name": strings.Repeat("界", 100), "schedule": "*/5 * * * *", "ready": "123456/123456", "phase": "CrashLoopBackOff", "image": strings.Repeat("x", 200)}
			line := pickerTableLine("> [x] ", pickerColumns(width, job), values)
			if got := lipgloss.Width(line); got != width {
				t.Fatalf("width=%d job=%t: got %d", width, job, got)
			}
		}
	}
}

func TestPickerHelpCapturesKeysAndPreservesSelection(t *testing.T) {
	m := newPickerModel(pickerViewportFixture(), 13, "tkgs-qa", "apollo", []string{"app-00"})
	m.width, m.height, m.index = 80, 24, 12
	m = pickerTestKey(m, "?")
	if !m.helpVisible || !strings.Contains(ansi.Strip(m.View()), "Keyboard help") {
		t.Fatal("help did not open")
	}
	for _, name := range []string{"x", "enter", "c", "n", "r", "h", "e", " "} {
		updated, cmd := m.Update(pickerTestKeyMsg(name))
		m = updated.(pickerModel)
		if cmd != nil || m.confirmRun != nil || m.activity.view != "" || !m.helpVisible || m.index != 12 || !m.checked["app-00"] {
			t.Fatalf("%q escaped the help modal", name)
		}
	}
	m = pickerTestKey(m, "end")
	if m.helpScroll == 0 || !strings.Contains(ansi.Strip(m.View()), "Only columns that fit") {
		t.Fatal("full help cannot be reached")
	}
	m = pickerTestKey(m, "esc")
	if m.helpVisible || m.index != 12 || !m.checked["app-00"] {
		t.Fatal("closing help lost selection")
	}
	updated, cmd := m.Update(pickerTestKeyMsg("x"))
	if cmd != nil || updated.(pickerModel).confirmRun == nil {
		t.Fatal("CronJob confirmation did not resume after closing help")
	}
}

func TestPickerHelpFitsAfterResize(t *testing.T) {
	m := newPickerModel(pickerViewportFixture(), 13, "tkgs-qa", "apollo", nil)
	m = pickerTestKey(m, "?")
	for _, size := range []tea.WindowSizeMsg{{Width: 24, Height: 8}, {Width: 40, Height: 12}, {Width: 80, Height: 24}, {Width: 160, Height: 40}} {
		updated, _ := m.Update(size)
		m = pickerTestKey(updated.(pickerModel), "end")
		view := ansi.Strip(m.View())
		assertPickerFits(t, m)
		if !strings.Contains(view, "Esc") {
			t.Fatalf("close hint hidden at %+v:\n%s", size, view)
		}
	}
}

func TestPickerPageAndEndNavigation(t *testing.T) {
	m := newPickerModel(pickerViewportFixture(), 13, "qa", "apollo", nil)
	m.width, m.height = 80, 24
	for _, target := range []struct {
		key   string
		index int
	}{{"pgdown", 12}, {"pgup", 0}, {"end", 12}, {"home", 0}} {
		m = pickerTestKey(m, target.key)
		if m.index != target.index {
			t.Fatalf("%s: index=%d", target.key, m.index)
		}
		if !strings.Contains(ansi.Strip(m.View()), "> [ ]") {
			t.Fatal("cursor row hidden")
		}
	}
}

func pickerTestKeyMsg(name string) tea.KeyMsg {
	switch name {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "end":
		return tea.KeyMsg{Type: tea.KeyEnd}
	case "home":
		return tea.KeyMsg{Type: tea.KeyHome}
	case "pgup":
		return tea.KeyMsg{Type: tea.KeyPgUp}
	case "pgdown":
		return tea.KeyMsg{Type: tea.KeyPgDown}
	case " ":
		return tea.KeyMsg{Type: tea.KeySpace}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(name)}
}

func pickerTestKey(m pickerModel, name string) pickerModel {
	updated, _ := m.Update(pickerTestKeyMsg(name))
	return updated.(pickerModel)
}
