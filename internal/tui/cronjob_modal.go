package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

const (
	minCronJobModalWidth  = 40
	minCronJobModalHeight = 12
)

func (m pickerModel) modalViewport() (int, int) {
	width, height := m.width, m.height
	if width <= 0 {
		width = 80
	}
	if height <= 0 {
		height = 24
	}
	return width, height
}

func (m pickerModel) updateCronJobModal(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "ctrl+c":
		m.podMonitorState.stop()
		m.result.Action = PickerQuit
		return m, tea.Quit
	case "n", "N", "esc", "q":
		m.confirmRun = nil
		return m, nil
	}
	width, height := m.modalViewport()
	// A create must never be confirmed while its target and controls are hidden.
	if width < minCronJobModalWidth || height < minCronJobModalHeight {
		return m, nil
	}
	switch key.String() {
	case "tab", "shift+tab":
		m.confirmRunFocused = !m.confirmRunFocused
	case "left":
		m.confirmRunFocused = true
	case "right":
		m.confirmRunFocused = false
	case "up", "down", "pgup", "pgdown", "home", "end":
		_, maxScroll := m.cronJobModalLayout(width, height)
		switch key.String() {
		case "up":
			m.confirmRunScroll = max(0, m.confirmRunScroll-1)
		case "down":
			m.confirmRunScroll = min(maxScroll, m.confirmRunScroll+1)
		case "pgup":
			m.confirmRunScroll = max(0, m.confirmRunScroll-5)
		case "pgdown":
			m.confirmRunScroll = min(maxScroll, m.confirmRunScroll+5)
		case "home":
			m.confirmRunScroll = 0
		case "end":
			m.confirmRunScroll = maxScroll
		}
	case "enter":
		if !m.confirmRunFocused {
			m.confirmRun = nil
			return m, nil
		}
		return m.confirmCronJobRun()
	case "y", "Y":
		return m.confirmCronJobRun()
	}
	return m, nil
}

func (m pickerModel) confirmCronJobRun() (tea.Model, tea.Cmd) {
	m.result = PickerResult{Action: PickerRunCronJob, App: *m.confirmRun, Marked: m.markedNames()}
	m.podMonitorState.stop()
	return m, tea.Quit
}

func (m pickerModel) cronJobModalLayout(width, height int) (string, int) {
	if width < minCronJobModalWidth || height < minCronJobModalHeight {
		return ansi.Wrap("Resize terminal to at least 40 × 12 to confirm.\nEsc cancels.", max(1, width-2), ""), 0
	}
	job := m.confirmRun.CronJob
	zone := job.TimeZone
	if zone == "" {
		zone = "controller local"
	}
	fields := []string{
		"CronJob: " + job.Name,
		"Context / namespace: " + m.kubeContext + " / " + m.namespace,
		"Schedule: " + job.Schedule + " (" + zone + ")",
		fmt.Sprintf("Suspended: %t · Active Jobs: %d", job.Suspended, job.Active),
		"",
		"Create one Job now. The schedule stays unchanged.",
	}
	if job.Suspended || job.Active > 0 {
		fields = append(fields, "Manual runs bypass the schedule and concurrency policy.")
	}
	modalWidth := min(76, width-4)
	innerWidth := modalWidth - 4 // border and one cell of horizontal padding
	body := strings.Split(ansi.Wrap(strings.Join(fields, "\n"), innerWidth, ""), "\n")
	// Reserve border, title, spacing, buttons, key help, and scroll indicator.
	capacity := max(1, height-2-8)
	maxScroll := max(0, len(body)-capacity)
	start := min(m.confirmRunScroll, maxScroll)
	end := min(len(body), start+capacity)
	run, cancel := "  [ Run now ]", "  [ Cancel ]"
	buttonStyle := modeStyle.Padding(0)
	if m.confirmRunFocused {
		run = buttonStyle.Render("> [ Run now ]")
	} else {
		cancel = buttonStyle.Render("> [ Cancel ]")
	}
	lines := []string{headerStyle.Render("Run CronJob now?"), ""}
	lines = append(lines, body[start:end]...)
	if maxScroll > 0 {
		lines = append(lines, fmt.Sprintf("Details %d–%d/%d · ↑↓ scroll", start+1, end, len(body)))
	}
	lines = append(lines, "", run+"  "+cancel, "Tab/←→ select · Enter · Y · Esc")
	content := ansi.Wrap(strings.Join(lines, "\n"), innerWidth, "")
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(accentColor).
		Foreground(modalForeground).Background(modalBackground).Padding(0, 1).Width(modalWidth - 2).Render(content), maxScroll
}
