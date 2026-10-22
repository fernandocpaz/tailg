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
	if !strings.ContainsAny(start, "█▀▄") {
		t.Fatalf("missing Road Runner pixel animation: %s", start)
	}
	if strings.Contains(start, "tailg") || strings.Contains(start, "MEEP") || strings.Contains(start, "Starting") || strings.Contains(start, "Press") {
		t.Fatalf("splash must display only the bird, not text: %s", start)
	}
	previous := start
	moved := false
	for frame := 0; frame < startupIntroFrames(m.width); frame++ {
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

func TestStartupIntroRunsAcrossEntireTerminal(t *testing.T) {
    if startupIntroRenderWidth != 22 || startupIntroRenderHeight != 6 {
        t.Fatalf("expected a compact 22x6 runner, got %dx%d", startupIntroRenderWidth, startupIntroRenderHeight)
    }

    for _, width := range []int{22, 32, 48, 80, 120, 180, 320} {
        frames := startupIntroFrames(width)
        if startupIntroLeft(0) != -startupIntroRenderWidth+2 {
            t.Fatal("runner must begin entering from off-screen left")
        }
        if startupIntroLeft(frames-1) >= width {
            t.Fatalf("runner left the viewport before the last frame at width %d", width)
        }
        if startupIntroLeft(frames) < width {
            t.Fatalf("runner has not fully exited after %d frames at width %d", frames, width)
        }
        covered := make([]bool, width)
        for frame := 0; frame < frames; frame++ {
            left := startupIntroLeft(frame)
            if frame > 0 && left-startupIntroLeft(frame-1) != startupIntroStep {
                t.Fatalf("inconsistent movement at frame %d", frame)
            }
            for col := max(0, left); col < min(width, left+startupIntroRenderWidth); col++ {
                covered[col] = true
            }
        }
        for col, visited := range covered {
            if !visited {
                t.Fatalf("runner skipped viewport column %d at width %d", col, width)
            }
        }
    }
}

func TestStartupIntroFramesContainNoText(t *testing.T) {
	m := newPickerModel(nil, 0, "qa", "apollo", nil)
	m.width, m.height = 80, 24
	m.introActive = true
	for frame := 0; frame < startupIntroFrames(m.width); frame++ {
		m.introFrame = frame
		view := ansi.Strip(m.View())
		for _, runeValue := range view {
			switch runeValue {
			case ' ', '\n', '█', '▀', '▄':
			default:
				t.Fatalf("splash frame %d contains non-art character %q", frame, runeValue)
			}
		}
	}
}

func TestStartupIntroLegsActuallyAnimate(t *testing.T) {
	for frame := 0; frame < len(startupIntroLegFrames)-1; frame++ {
		current, next := startupIntroRaster(frame), startupIntroRaster(frame+1)
		var changed bool
		for y := 26; y < startupIntroSpriteHeight && !changed; y++ {
			for x := 0; x < startupIntroSpriteWidth; x++ {
				if current[y][x] != next[y][x] {
					changed = true
					break
				}
			}
		}
		if !changed {
			t.Fatalf("running stride %d must differ from the next frame", frame)
		}
	}
}

func TestStartupIntroDefersReleaseUpdateModal(t *testing.T) {
	m := newPickerModel(nil, 0, "qa", "apollo", nil)
	m.width, m.height = 100, 28
	m.introActive = true
	m.updates.pending = true
	if m.showUpdate() {
		t.Fatal("release update dialog must not interrupt the splash")
	}
	if strings.Contains(ansi.Strip(m.View()), "APPLICATION") {
		t.Fatal("only the bird should appear during the splash")
	}
	m.introActive = false
	if !m.showUpdate() {
		t.Fatal("pending release update should appear after the splash")
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
	for _, size := range []tea.WindowSizeMsg{{Width: 24, Height: 8}, {Width: 45, Height: 14}, {Width: 32, Height: 9}, {Width: 55, Height: 20}, {Width: 64, Height: 18}, {Width: 80, Height: 24}, {Width: 180, Height: 40}, {Width: 320, Height: 45}} {
		for i := 0; i < startupIntroFrames(m.width); i++ {
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
	m.width, m.height = 100, 28
	if m.introActive || m.View() != m.pickerView() {
		t.Fatal("normal picker must be visible when no intro was requested")
	}
}

func TestStartupIntroSkipsWhenTerminalTooSmall(t *testing.T) {
	m := newPickerModel(nil, 0, "qa", "apollo", nil)
	m.introActive = true
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 20, Height: 5})
	m = updated.(pickerModel)
	if m.introActive {
		t.Fatal("the intro must be disabled when the terminal is too small to show it")
	}
	updated, _ = m.Update(startupIntroTick{frame: 0})
	if updated.(pickerModel).introActive {
		t.Fatal("a pending frame must not reenable an intro skipped for terminal size")
	}
}
