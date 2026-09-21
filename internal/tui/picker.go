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
	width     int
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
	if size, ok := message.(tea.WindowSizeMsg); ok {
		m.width = size.Width
		return m, nil
	}
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
const (
	pickerAppWidth      = 36
	pickerReadyWidth    = 7
	pickerPhaseWidth    = 18
	pickerRestartsWidth = 8
	pickerAgeWidth      = 10
	pickerImageMinWidth = 12
)

func (m pickerModel) View() string {
	var lines = []string{
		headerStyle.Render("Select an application"),
		dimStyle.Render("Up/Down move | Enter selects | Esc cancels"),
		"",
	}
	now := time.Now()
	imageWidth := pickerImageWidth(m.width)
	header := renderPickerRow("  ", "APPLICATION", "READY", "PHASE", "RESTARTS", "STARTED", "DEPLOYED", "IMAGE", imageWidth)
	lines = append(lines, dimStyle.Render(header), dimStyle.Render(strings.Repeat("-", lipgloss.Width(header))))
	for index, app := range m.apps {
		marker := "  "
		style := lipgloss.NewStyle()
		if index == m.index {
			marker = "> "
			style = selectedStyle
		}
		line := renderPickerRow(
			marker,
			app.Name,
			app.Ready,
			app.Phases,
			fmt.Sprint(app.Restarts),
			formatPickerAge(now, app.StartedAt),
			formatPickerAge(now, app.DeployedAt),
			valueOrDash(app.ImageTag),
			imageWidth,
		)
		lines = append(lines, style.Render(line))
	}
	return strings.Join(lines, "\n")
}

func pickerImageWidth(terminalWidth int) int {
	// marker + fixed columns + six inter-column spaces.
	fixed := 2 + pickerAppWidth + pickerReadyWidth + pickerPhaseWidth + pickerRestartsWidth + 2*pickerAgeWidth + 6
	if terminalWidth <= 0 {
		return 24
	}
	available := terminalWidth - fixed
	if available < pickerImageMinWidth {
		return pickerImageMinWidth
	}
	return available
}

func renderPickerRow(marker, app, ready, phase, restarts, started, deployed, image string, imageWidth int) string {
	return fmt.Sprintf(
		"%s%-*s %-*s %-*s %*s %-*s %-*s %-*s",
		marker,
		pickerAppWidth, truncatePickerCell(app, pickerAppWidth),
		pickerReadyWidth, truncatePickerCell(ready, pickerReadyWidth),
		pickerPhaseWidth, truncatePickerCell(phase, pickerPhaseWidth),
		pickerRestartsWidth, truncatePickerCell(restarts, pickerRestartsWidth),
		pickerAgeWidth, truncatePickerCell(started, pickerAgeWidth),
		pickerAgeWidth, truncatePickerCell(deployed, pickerAgeWidth),
		imageWidth, truncatePickerCell(image, imageWidth),
	)
}

func truncatePickerCell(value string, width int) string {
	if width <= 0 || lipgloss.Width(value) <= width {
		return value
	}
	if width == 1 {
		return "…"
	}
	runes := []rune(value)
	for len(runes) > 0 && lipgloss.Width(string(runes)) > width-1 {
		runes = runes[:len(runes)-1]
	}
	return string(runes) + "…"
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
