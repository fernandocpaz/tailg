package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

func pickerBinding(label, description string, keys ...string) key.Binding {
	return key.NewBinding(key.WithKeys(keys...), key.WithHelp(label, description))
}

var pickerKeys = struct {
	up, down, pageUp, pageDown, home, end, mark, open, pods                key.Binding
	context, namespace, changes, events, monitor, refresh, run, help, quit key.Binding
}{
	up:        pickerBinding("↑/k", "Move up", "up", "k"),
	down:      pickerBinding("↓/j", "Move down", "down", "j"),
	pageUp:    pickerBinding("PgUp", "Page up", "pgup"),
	pageDown:  pickerBinding("PgDn", "Page down", "pgdown"),
	home:      pickerBinding("Home", "First row", "home"),
	end:       pickerBinding("End", "Last row", "end"),
	mark:      pickerBinding("Space", "Mark", " "),
	open:      pickerBinding("Enter", "Open logs", "enter"),
	pods:      pickerBinding("P", "Exact pods", "p", "P"),
	context:   pickerBinding("C", "Context", "c", "C"),
	namespace: pickerBinding("N", "Namespace", "n", "N"),
	changes:   pickerBinding("H", "Changes", "h", "H"),
	events:    pickerBinding("E", "Events", "e", "E"),
	monitor:   pickerBinding("M", "Monitor", "m", "M"),
	refresh:   pickerBinding("R", "Refresh", "r", "R"),
	run:       pickerBinding("X", "Run now", "x", "X"),
	help:      pickerBinding("?", "Help", "?"),
	quit:      pickerBinding("Q", "Quit", "q", "Q", "esc", "ctrl+c"),
}

func keyMatches(msg tea.KeyMsg, bindings ...key.Binding) bool { return key.Matches(msg, bindings...) }

// The same bindings drive keyboard handling and the Bubbles help component.
func pickerHelpComponent() help.Model {
	h := help.New()
	h.Styles.ShortKey = headerStyle
	h.Styles.ShortDesc = pickerHelpLabelStyle
	h.Styles.ShortSeparator = dimStyle
	h.Styles.FullKey = headerStyle
	h.Styles.FullDesc = pickerHelpLabelStyle
	return h
}

func pickerKeyHints(width int, bindings ...key.Binding) []string {
	h := pickerHelpComponent()
	h.ShortSeparator = "  ·  "
	var lines []string
	var row []key.Binding
	for _, binding := range bindings {
		if !binding.Enabled() {
			continue
		}
		trial := append(append([]key.Binding(nil), row...), binding)
		if len(row) > 0 && lipgloss.Width(h.ShortHelpView(trial)) > width {
			lines = append(lines, h.ShortHelpView(row))
			row = nil
		}
		row = append(row, binding)
	}
	if len(row) > 0 {
		lines = append(lines, h.ShortHelpView(row))
	}
	return pickerWrapLines(lines, width)
}

func (m pickerModel) updatePickerHelp(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		m.podMonitorState.stop()
		m.result.Action = PickerQuit
		return m, tea.Quit
	case "esc", "q", "Q", "?":
		m.helpVisible = false
	case "up", "k":
		m.helpScroll = max(0, m.helpScroll-1)
	case "down", "j", "pgup", "pgdown", "home", "end":
		_, maximum := m.pickerHelpModal()
		switch msg.String() {
		case "down", "j":
			m.helpScroll = min(maximum, m.helpScroll+1)
		case "pgup":
			m.helpScroll = max(0, m.helpScroll-5)
		case "pgdown":
			m.helpScroll = min(maximum, m.helpScroll+5)
		case "home":
			m.helpScroll = 0
		case "end":
			m.helpScroll = maximum
		}
	}
	return m, nil
}

func (m pickerModel) pickerHelpModal() (string, int) {
	width, height := m.modalViewport()
	if width < 24 || height < 8 {
		return ansi.Hardwrap("Resize for help.\nEsc closes.", max(1, width), true), 0
	}
	inner := min(72, width-6)
	h := pickerHelpComponent()
	var body []string
	groups := []struct {
		title string
		keys  []key.Binding
	}{
		{"NAMESPACE", []key.Binding{pickerKeys.context, pickerKeys.namespace, pickerKeys.changes, pickerKeys.events, pickerKeys.monitor, pickerKeys.refresh}},
		{"SELECTED ROW", []key.Binding{pickerKeys.mark, pickerKeys.open, pickerKeys.pods, pickerKeys.run}},
		{"NAVIGATION", []key.Binding{pickerKeys.up, pickerKeys.down, pickerKeys.pageUp, pickerKeys.pageDown, pickerKeys.home, pickerKeys.end, pickerKeys.help, pickerKeys.quit}},
	}
	for _, group := range groups {
		body = append(body, headerStyle.Render(group.title))
		body = append(body, pickerWrapLines(strings.Split(h.FullHelpView([][]key.Binding{group.keys}), "\n"), inner)...)
		body = append(body, "")
	}
	body = append(body, pickerWrapLines([]string{
		"Enter opens marked applications, or the highlighted row when nothing is marked.",
		"P chooses exact pods after unmarking applications. X asks for confirmation before creating a Job from the highlighted CronJob.",
		"Only columns that fit are shown; the selected row's summary appears above the footer.",
		podMonitorLegend(),
	}, inner)...)
	capacity := max(1, height-7)
	maximum := max(0, len(body)-capacity)
	start := min(m.helpScroll, maximum)
	lines := []string{headerStyle.Render("Keyboard help"), ""}
	lines = append(lines, body[start:min(len(body), start+capacity)]...)
	lines = append(lines, "", "↑↓ / PgUp PgDn scroll · Esc closes")
	content := ansi.Hardwrap(strings.Join(lines, "\n"), inner, true)
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(accentColor).
		Foreground(modalForeground).Background(modalBackground).Padding(0, 1).Width(inner + 2).Render(content), maximum
}
