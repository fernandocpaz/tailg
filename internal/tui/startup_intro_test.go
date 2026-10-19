package tui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/fernandocpaz/tailg/internal/core"
)

func TestStartupIntroAnimatesAndTransitionsToPicker(t *testing.T) {
	m := newPickerModel([]core.AppChoice{{Name: "api", Pods: []string{"api-1"}}}, 1, "tkgs-qa", "apollo", nil)
	m.introActive = true
	m.refreshContext = context.Background()
	m.width, m.height = 100, 28
	if m.Init() == nil {
		t.Fatal("interactive startup must schedule animation frames")
	}
	start := ansi.Strip(m.View())
	if !strings.Contains(start, "tailg") || !strings.Contains(start, "(o)") || !strings.Contains(start, "Press any key to skip") {
		t.Fatalf("missing Road Runner animation: %s", start)
	}
	previous := start
	moved := false
	for frame := 0; frame < startupIntroFrameCount; frame++ {
		updated, _ := m.Update(startupIntroTick{frame: frame})
		m = updated.(pickerModel)
		if m.introActive && m.View() != previous {
			moved = true
		}
		previous = m.View()
	}
	if !moved || m.introActive || !strings.Contains(ansi.Strip(m.View()), "APPLICATION") {
		t.Fatal("animation must change frames then return to interactive application picker")
	}
}

func TestStartupIntroKeySkipConsumesOnlyDismissKey(t *testing.T) {
	m := newPickerModel([]core.AppChoice{{Name: "api", Pods: []string{"api-1"}}}, 1, "tkgs-qa", "apollo", nil)
	m.introActive = true
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("m")})
	m = updated.(pickerModel)
	if m.introActive || m.monitorEnabled || cmd != nil {
		t.Fatal("dismiss key must not perform a picker action")
	}
	updated, cmd = m.Update(startupIntroTick{frame: 0})
	if cmd != nil || updated.(pickerModel).introActive {
		t.Fatal("old animation tick must not restart a dismissed intro")
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("m")})
	if !updated.(pickerModel).monitorEnabled {
		t.Fatal("picker keys must work normally after intro is dismissed")
	}
	updated.(pickerModel).monitorCancel()
}

func TestStartupIntroCtrlCQuitsImmediately(t *testing.T) {
	m := newPickerModel(nil, 0, "qa", "apollo", nil)
	m.introActive = true
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if updated.(pickerModel).introActive || cmd == nil || updated.(pickerModel).result.Action != PickerQuit {
		t.Fatal("ctrl+c must exit even during startup animation")
	}
}

func TestStartupIntroRendersWithinTerminalBounds(t *testing.T) {
	m := newPickerModel(nil, 0, "tkgs-qa", "apollo", nil)
	m.introActive = true
	for _, size := range []tea.WindowSizeMsg{{Width: 24, Height: 8}, {Width: 45, Height: 14}, {Width: 55, Height: 20}, {Width: 80, Height: 24}, {Width: 180, Height: 40}} {
		for i := 0; i < startupIntroFrameCount; i++ {
			m.width, m.height, m.introFrame = size.Width, size.Height, i
			lines := strings.Split(ansi.Strip(m.View()), "\n")
			if len(lines) != size.Height {
				t.Fatalf("frame %d viewport height %d, want %d", i, len(lines), size.Height)
			}
			for _, line := range lines {
				if lipgloss.Width(line) != size.Width {
					t.Fatalf("frame %d width %d, want %d: %q", i, lipgloss.Width(line), size.Width, line)
				}
			}
		}
	}
}

func TestStartupIntroNotActiveByDefault(t *testing.T) {
	m := newPickerModel(nil, 0, "qa", "apollo", nil)
	if m.introActive || strings.Contains(ansi.Strip(m.View()), "Press any key to skip") {
		t.Fatal("intro should be enabled explicitly only for the first interactive picker")
	}
}
