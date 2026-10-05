package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/fernandocpaz/tailg/internal/core"
)

const visiblePodRows = 15

type podPickerRow struct {
	app string
	pod core.PodChoice
}

type podPickerModel struct {
	rows        []podPickerRow
	checked     map[string]bool
	index       int
	selected    []string
	kubeContext string
	namespace   string
}

// PickPods pins exactly the selected pod names. Esc returns to the app picker.
func PickPods(apps []core.AppChoice, kubeContext, namespace string) (selected []string, ok bool, err error) {
	model := podPickerModel{rows: preparePodPickerRows(apps), kubeContext: kubeContext, namespace: namespace}
	result, err := tea.NewProgram(model).Run()
	if err != nil {
		return nil, false, err
	}
	selected = result.(podPickerModel).selected
	return selected, len(selected) > 0, nil
}

func preparePodPickerRows(apps []core.AppChoice) []podPickerRow {
	var rows []podPickerRow
	for _, app := range apps {
		if len(app.PodChoices) > 0 {
			for _, pod := range app.PodChoices {
				rows = append(rows, podPickerRow{app: app.Name, pod: pod})
			}
			continue
		}
		for _, name := range app.Pods {
			rows = append(rows, podPickerRow{app: app.Name, pod: core.PodChoice{Name: name}})
		}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].app != rows[j].app {
			return rows[i].app < rows[j].app
		}
		return rows[i].pod.Name < rows[j].pod.Name
	})
	return rows
}

func (m podPickerModel) Init() tea.Cmd { return nil }

func (m podPickerModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := message.(tea.KeyMsg); ok {
		switch key.String() {
		case "ctrl+c", "q", "esc":
			return m, tea.Quit
		case "up", "k":
			m.index = max(0, m.index-1)
		case "down", "j":
			if len(m.rows) > 0 {
				m.index = min(len(m.rows)-1, m.index+1)
			}
		case "pgup":
			m.index = max(0, m.index-visiblePodRows)
		case "pgdown":
			if len(m.rows) > 0 {
				m.index = min(len(m.rows)-1, m.index+visiblePodRows)
			}
		case "home":
			m.index = 0
		case "end":
			if len(m.rows) > 0 {
				m.index = len(m.rows) - 1
			}
		case " ":
			if len(m.rows) > 0 {
				if m.checked == nil {
					m.checked = make(map[string]bool)
				}
				name := m.rows[m.index].pod.Name
				m.checked[name] = !m.checked[name]
			}
		case "a":
			if len(m.rows) > 0 {
				if m.checked == nil {
					m.checked = make(map[string]bool)
				}
				app := m.rows[m.index].app
				allChecked := true
				for _, row := range m.rows {
					if row.app == app && !m.checked[row.pod.Name] {
						allChecked = false
					}
				}
				for _, row := range m.rows {
					if row.app == app {
						m.checked[row.pod.Name] = !allChecked
					}
				}
			}
		case "enter":
			if len(m.rows) > 0 {
				for _, row := range m.rows {
					if m.checked[row.pod.Name] {
						m.selected = append(m.selected, row.pod.Name)
					}
				}
				if len(m.selected) == 0 {
					m.selected = []string{m.rows[m.index].pod.Name}
				}
				return m, tea.Quit
			}
		}
	}
	return m, nil
}

func (m podPickerModel) View() string {
	lines := []string{
		headerStyle.Render("Select exact pods"),
		fmt.Sprintf("Context: %s  |  Namespace: %s", m.kubeContext, m.namespace),
		renderPickerShortcuts(
			shortcut("↑↓ / PGUP PGDN", "Move"),
			shortcut("SPACE", "Select"),
			shortcut("A", "Toggle app"),
		),
		renderPickerShortcuts(
			shortcut("ENTER", "Open"),
			shortcut("ESC", "Applications"),
		),
		fmt.Sprintf("Selected: %d pods · pinned to these pod names", m.checkedCount()),
		"",
	}
	if len(m.rows) == 0 {
		return strings.Join(append(lines, "No pods found in this namespace."), "\n")
	}
	lines = append(lines, dimStyle.Render(fmt.Sprintf("%-31s %-53s %-7s %-12s %s", "APPLICATION", "POD", "READY", "PHASE", "STARTED")))
	start := max(0, m.index-visiblePodRows/2)
	start = min(start, max(0, len(m.rows)-visiblePodRows))
	end := min(len(m.rows), start+visiblePodRows)
	now := time.Now()
	for index := start; index < end; index++ {
		row := m.rows[index]
		marker := "  "
		check := "[ ] "
		if m.checked[row.pod.Name] {
			check = "[x] "
		}
		style := lipgloss.NewStyle()
		if index == m.index {
			marker = "> "
			style = selectedStyle
		}
		appName := row.app
		if index > start && m.rows[index-1].app == row.app {
			appName = ""
		}
		line := fmt.Sprintf("%s%s%-25s %-53s %-7s %-12s %s", marker, check,
			truncatePickerCell(appName, 25), truncatePickerCell(row.pod.Name, 53),
			valueOrDash(row.pod.Ready), valueOrDash(row.pod.Phase), formatPickerAge(now, row.pod.StartedAt))
		lines = append(lines, style.Render(line))
	}
	if len(m.rows) > visiblePodRows {
		lines = append(lines, dimStyle.Render(fmt.Sprintf("Showing %d–%d of %d pods", start+1, end, len(m.rows))))
	}
	return strings.Join(lines, "\n")
}

func (m podPickerModel) checkedCount() int {
	count := 0
	for _, selected := range m.checked {
		if selected {
			count++
		}
	}
	return count
}
