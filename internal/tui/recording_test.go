package tui

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func TestRecordingPathLayoutAndHitTarget(t *testing.T) {
	m := workflowModel()
	m.width, m.height = 40, 24
	baseline := m.logHeight()
	if strings.Contains(m.View(), "Recording:") {
		t.Fatal("inactive recording banner")
	}
	m.config.RecordingPath = "/logs/患者/" + strings.Repeat("long directory/", 4) + "api.ndjson"
	lines := m.recordingLines()
	if len(lines) < 2 || !strings.Contains(m.View(), "[Copy path]") {
		t.Fatal("missing wrapped recording link")
	}
	if m.logHeight() != baseline-len(lines) {
		t.Fatal("recording rows must reduce log viewport")
	}
	for row, line := range lines {
		if lipgloss.Width(line) > m.width {
			t.Fatalf("overflow: %q", line)
		}
		hit := tea.MouseMsg{X: 0, Y: row + 1, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress}
		if !m.recordingLinkHit(hit) {
			t.Fatalf("row %d not clickable", row)
		}
	}
	for _, hit := range []tea.MouseMsg{
		{X: 0, Y: 0, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress},
		{X: 0, Y: len(lines) + 1, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress},
		{X: 0, Y: 1, Button: tea.MouseButtonRight, Action: tea.MouseActionPress},
		{X: 0, Y: 1, Button: tea.MouseButtonLeft, Action: tea.MouseActionRelease},
	} {
		if m.recordingLinkHit(hit) {
			t.Fatal("click outside link copied path")
		}
	}
	m.detail = "entry"
	if m.recordingLinkHit(tea.MouseMsg{X: 0, Y: 1, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress}) {
		t.Fatal("hidden link clickable")
	}
}

func TestRecordingClickAndShortcutCopyFullPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake clipboard uses POSIX shell")
	}
	dir := t.TempDir()
	output := filepath.Join(dir, "copied")
	t.Setenv("TAILG_TEST_CLIPBOARD", output)
	t.Setenv("PATH", dir)
	if err := os.WriteFile(filepath.Join(dir, "wl-copy"), []byte("#!/bin/sh\n/bin/cat > \"$TAILG_TEST_CLIPBOARD\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, msg := range []tea.Msg{
		tea.MouseMsg{X: 15, Y: 1, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress},
		tea.KeyMsg{Type: tea.KeyCtrlY},
	} {
		m := workflowModel()
		m.width, m.height = 45, 24
		m.config.RecordingPath = "/logs/患者 directory/" + strings.Repeat("long/", 20) + "api.ndjson"
		updated, cmd := m.Update(msg)
		if cmd == nil {
			t.Fatal("copy not requested")
		}
		updated, _ = updated.Update(cmd())
		if updated.(model).notice != "Copied file path" {
			t.Fatal(updated.(model).notice)
		}
		copied, err := os.ReadFile(output)
		if err != nil || string(copied) != m.config.RecordingPath {
			t.Fatalf("copied %q: %v", copied, err)
		}
	}
}

func TestRecordingMouseRestoresAfterPanelsAndResize(t *testing.T) {
	m := workflowModel()
	m.config.RecordingPath = "/logs/api.ndjson"
	m.width, m.height = 0, 0
	updated, cmd := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(model)
	if cmd == nil || !m.wantsMouse() {
		t.Fatal("recording clicks not enabled after resize")
	}
	updated, cmd = m.Update(tea.KeyMsg{Type: tea.KeyF5})
	m = updated.(model)
	if cmd == nil || m.wantsMouse() {
		t.Fatal("panel must restore terminal selection")
	}
	updated, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(model)
	if cmd == nil || !m.wantsMouse() {
		t.Fatal("recording clicks not restored after closing panel")
	}
}
