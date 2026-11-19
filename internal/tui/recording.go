package tui

import (
	"strings"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

type recordingPathCopiedMsg string

func (m model) defaultMouseCommand() tea.Cmd {
	if m.wantsMouse() {
		return tea.EnableMouseCellMotion
	}
	return tea.DisableMouse
}

func (m model) wantsMouse() bool {
	return m.recordingVisible() || m.resourceMouseCapture
}

func (m model) recordingVisible() bool {
	return m.config.RecordingPath != "" && m.width > 0 && m.height > 0 &&
		m.aiSetup == nil && m.serviceRepoSetup == nil && !m.showUpdate() &&
		!m.errorLogs.open && !m.replicas.open && !m.traceOpen && !m.freshnessOpen &&
		!m.heartbeatOpen && !m.issueOpen && !m.resourceOpen && m.detail == ""
}

func (m model) recordingLines() []string {
	if m.config.RecordingPath == "" {
		return nil
	}
	// File names may contain terminal controls. Display them safely, but copy
	// the exact original path, including spaces and Unicode.
	path := strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return '\uFFFD'
		}
		return r
	}, m.config.RecordingPath)
	text := "Recording: [Copy path] " + path + " (Ctrl+Y; Shift-drag selects text)"
	return strings.Split(ansi.Hardwrap(text, max(1, m.width), true), "\n")
}

func (m model) renderRecordingSource() string {
	lines := m.recordingLines()
	if len(lines) == 0 {
		return m.renderLogSource()
	}
	if m.config.Formatter.Color {
		style := lipgloss.NewStyle().Foreground(accentColor).Underline(true)
		for i := range lines {
			lines[i] = style.Render(lines[i])
		}
	}
	return strings.Join(lines, "\n") + "\n" + m.renderLogSource()
}

func (m model) recordingLinkHit(msg tea.MouseMsg) bool {
	if !m.recordingVisible() || msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonLeft {
		return false
	}
	lines := m.recordingLines()
	row := msg.Y - 1 // the header is the first screen row
	return row >= 0 && row < len(lines) && msg.X >= 0 && msg.X < lipgloss.Width(lines[row])
}

func (m model) copyRecordingPath() tea.Cmd {
	path := m.config.RecordingPath
	return func() tea.Msg {
		result := copyText(path)
		if strings.HasPrefix(result, "Copied ") {
			result = "Copied file path"
		}
		return recordingPathCopiedMsg(result)
	}
}
