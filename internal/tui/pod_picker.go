package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
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
	podMonitorState
}

// PickPods pins exactly the selected pod names. Esc returns to the app picker.
func PickPods(apps []core.AppChoice, kubeContext, namespace string) (selected []string, ok bool, err error) {
	model := podPickerModel{rows: preparePodPickerRows(apps), kubeContext: kubeContext, namespace: namespace}
	result, err := tea.NewProgram(model).Run()
	if final, ok := result.(podPickerModel); ok {
		final.podMonitorState.stop()
	}
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
	if handled, cmd := m.podMonitorState.update(message, m.rows, m.kubeContext, m.namespace); handled {
		return m, cmd
	}

	if key, ok := message.(tea.KeyMsg); ok {
		switch key.String() {
		case "ctrl+c", "q", "esc":
			m.podMonitorState.stop()
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
		case "m", "M":
			return m, m.podMonitorState.toggle(m.rows, m.kubeContext, m.namespace)
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
				m.podMonitorState.stop()
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
			shortcut("M", "Monitor"),
			shortcut("ESC", "Applications"),
		),
		fmt.Sprintf("Selected: %d pods · pinned to these pod names", m.checkedCount()),
		podMonitorSummary(m.monitorEnabled, m.monitorBusy, m.monitorLastScan, m.monitorError),
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
		selected := index == m.index
		if selected {
			marker = "> "
		}
		appName := row.app
		if index > start && m.rows[index-1].app == row.app {
			appName = ""
		}

		prefix := fmt.Sprintf("%s%s%-25s ", marker, check, truncatePickerCell(appName, 25))
		podCell := fmt.Sprintf("%-53s", truncatePickerCell(row.pod.Name, 53))
		suffix := fmt.Sprintf(" %-7s %-12s %s",
			valueOrDash(row.pod.Ready), valueOrDash(row.pod.Phase), formatPickerAge(now, row.pod.StartedAt))

		if m.monitorEnabled {
			if health, ok := m.monitorHealth[row.pod.Name]; ok {
				podCell = renderMonitoredPodName(podCell, health, m.monitorFlashOn)
			} else {
				podCell = dimStyle.Render(podCell)
			}
		}
		if selected {
			prefix = selectedStyle.Render(prefix)
			suffix = selectedStyle.Render(suffix)
			if !m.monitorEnabled {
				podCell = selectedStyle.Render(podCell)
			}
		}
		lines = append(lines, prefix+podCell+suffix)
	}
	if m.monitorEnabled {
		lines = append(lines, dimStyle.Render(podMonitorLegend()))
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
