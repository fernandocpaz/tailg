package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/fernandocpaz/tailg/internal/kube"
)

type statusErrorPickerModel struct {
	errors    []kube.StatusErrorSelection
	index     int
	selected  *kube.StatusErrorSelection
	cancelled bool
	width     int
}

const statusErrorRowsStart = 5

func PickStatusError(errors []kube.StatusErrorSelection) (kube.StatusErrorSelection, bool, error) {
	if len(errors) == 0 {
		return kube.StatusErrorSelection{}, false, nil
	}
	result, err := tea.NewProgram(
		statusErrorPickerModel{errors: errors},
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	).Run()
	if err != nil {
		return kube.StatusErrorSelection{}, false, err
	}
	model := result.(statusErrorPickerModel)
	if model.cancelled || model.selected == nil {
		return kube.StatusErrorSelection{}, false, nil
	}
	return *model.selected, true, nil
}

func (m statusErrorPickerModel) Init() tea.Cmd { return nil }

func (m statusErrorPickerModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := message.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		return m, nil
	case tea.MouseMsg:
		if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
			index := msg.Y - statusErrorRowsStart
			if index >= 0 && index < len(m.errors) {
				m.index = index
				selected := m.errors[index]
				m.selected = &selected
				return m, tea.Quit
			}
		}
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q", "esc":
			m.cancelled = true
			return m, tea.Quit
		case "up", "k":
			m.index = max(0, m.index-1)
		case "down", "j":
			m.index = min(len(m.errors)-1, m.index+1)
		case "enter":
			selected := m.errors[m.index]
			m.selected = &selected
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m statusErrorPickerModel) View() string {
	width := m.width
	if width <= 0 {
		width = 120
	}
	summaryWidth := max(24, width-93)
	header := renderStatusErrorPickerRow("  ", "SELECT", "COUNT", "TYPE", "SERVICE", "POD / CONTAINER", "LAST SEEN", "SUMMARY", summaryWidth)
	lines := []string{
		headerStyle.Render("Recent errors — select one to open its logs"),
		dimStyle.Render("Up/Down move | Enter opens | Mouse click opens | Esc returns"),
		"",
		dimStyle.Render(header),
		dimStyle.Render(strings.Repeat("-", min(width, lipgloss.Width(header)))),
	}
	for index, item := range m.errors {
		marker := "  "
		style := lipgloss.NewStyle()
		if index == m.index {
			marker = "> "
			style = selectedStyle
		}
		source := item.Pod
		if item.Container != "" {
			source += "/" + item.Container
		}
		lastSeen := "-"
		if !item.LastSeen.IsZero() {
			lastSeen = formatStatusErrorAge(time.Now(), item.LastSeen)
		}
		row := renderStatusErrorPickerRow(
			marker,
			fmt.Sprintf("[%d]", index+1),
			fmt.Sprint(item.Count),
			item.Kind,
			item.Service,
			source,
			lastSeen,
			item.Summary,
			summaryWidth,
		)
		lines = append(lines, style.Render(row))
	}
	return strings.Join(lines, "\n")
}

func renderStatusErrorPickerRow(marker, selectValue, count, kind, service, source, lastSeen, summary string, summaryWidth int) string {
	return fmt.Sprintf(
		"%s%-8s %5s %-14s %-16s %-30s %-10s %-*s",
		marker,
		truncatePickerCell(selectValue, 8),
		truncatePickerCell(count, 5),
		truncatePickerCell(kind, 14),
		truncatePickerCell(service, 16),
		truncatePickerCell(source, 30),
		truncatePickerCell(lastSeen, 10),
		summaryWidth,
		truncatePickerCell(summary, summaryWidth),
	)
}

func formatStatusErrorAge(now, value time.Time) string {
	if value.IsZero() {
		return "-"
	}
	age := now.Sub(value)
	if age < 0 {
		age = 0
	}
	switch {
	case age < time.Minute:
		return "now"
	case age < time.Hour:
		return fmt.Sprintf("%dm ago", int(age/time.Minute))
	case age < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(age/time.Hour))
	default:
		return fmt.Sprintf("%dd ago", int(age/(24*time.Hour)))
	}
}
