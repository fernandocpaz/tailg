package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/fernandocpaz/tailg/internal/core"
)

type pickerModel struct {
	apps      []core.AppChoice
	index     int
	selected  *core.AppChoice
	cancelled bool
}

func PickApp(apps []core.AppChoice) (core.AppChoice, error) {
	if len(apps) == 0 {
		return core.AppChoice{}, fmt.Errorf("no applications were found")
	}
	result, err := tea.NewProgram(pickerModel{apps: apps}).Run()
	if err != nil {
		return core.AppChoice{}, err
	}
	picker := result.(pickerModel)
	if picker.cancelled || picker.selected == nil {
		return core.AppChoice{}, fmt.Errorf("selection cancelled")
	}
	return *picker.selected, nil
}
func (m pickerModel) Init() tea.Cmd { return nil }
func (m pickerModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := message.(tea.KeyMsg); ok {
		switch key.String() {
		case "ctrl+c", "q", "esc":
			m.cancelled = true
			return m, tea.Quit
		case "up", "k":
			m.index = max(0, m.index-1)
		case "down", "j":
			m.index = min(len(m.apps)-1, m.index+1)
		case "enter":
			selected := m.apps[m.index]
			m.selected = &selected
			return m, tea.Quit
		}
	}
	return m, nil
}
func (m pickerModel) View() string {
	var lines = []string{headerStyle.Render("Select an application"), dimStyle.Render("Up/Down move | Enter selects | Esc cancels"), ""}
	now := time.Now()
	for index, app := range m.apps {
		marker := "  "
		style := lipgloss.NewStyle()
		if index == m.index {
			marker = "> "
			style = selectedStyle
		}
		line := fmt.Sprintf("%s%-30s ready=%-7s phase=%-20s restarts=%-3d started=%-10s deployed=%-10s image=%s",
			marker, app.Name, app.Ready, app.Phases, app.Restarts,
			formatPickerAge(now, app.StartedAt), formatPickerAge(now, app.DeployedAt), valueOrDash(app.ImageTag))
		lines = append(lines, style.Render(strings.TrimRight(line, " ")))
	}
	return strings.Join(lines, "\n")
}

func formatPickerAge(now, value time.Time) string {
	if value.IsZero() {
		return "-"
	}
	age := now.Sub(value)
	if age < 0 {
		return value.Local().Format("Jan 02")
	}
	switch {
	case age < time.Minute:
		return "now"
	case age < time.Hour:
		return fmt.Sprintf("%dm ago", int(age/time.Minute))
	case age < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(age/time.Hour))
	case age < 7*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(age/(24*time.Hour)))
	default:
		return value.Local().Format("Jan 02")
	}
}

func valueOrDash(value string) string {
	if value == "" {
		return "-"
	}
	return value
}
