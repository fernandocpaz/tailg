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

const maxPickerApps = 20

type pickerModel struct {
	apps        []core.AppChoice
	totalApps   int
	index       int
	checked     map[string]bool
	note        string
	result      PickerResult
	kubeContext string
	namespace   string
	loadError   string
	width       int
}

type PickerAction int

const (
	PickerQuit PickerAction = iota
	PickerOpenApp
	PickerSwitchContext
	PickerSwitchNamespace
	PickerRefresh
	PickerSelectPods
)

type PickerResult struct {
	Action PickerAction
	App    core.AppChoice
	Apps   []core.AppChoice
	Pods   []string
	Marked []string
}

func PickApp(apps []core.AppChoice, kubeContext, namespace string, loadErr error, previous []string) (PickerResult, error) {
	prepared, total := preparePickerApps(apps)
	model := newPickerModel(prepared, total, kubeContext, namespace, previous)
	if loadErr != nil {
		model.loadError = pickerErrorSummary(loadErr)
	}
	result, err := tea.NewProgram(model).Run()
	if err != nil {
		return PickerResult{}, err
	}
	return result.(pickerModel).result, nil
}

func newPickerModel(apps []core.AppChoice, total int, kubeContext, namespace string, previous []string) pickerModel {
	model := pickerModel{apps: apps, totalApps: total, kubeContext: kubeContext, namespace: namespace}
	previousSet := make(map[string]bool, len(previous))
	for _, name := range previous {
		previousSet[name] = true
	}
	for _, app := range apps {
		if previousSet[app.Name] {
			if model.checked == nil {
				model.checked = make(map[string]bool)
			}
			model.checked[app.Name] = true
		}
	}
	return model
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
			m.result.Action = PickerQuit
			return m, tea.Quit
		case "c":
			m.result.Action = PickerSwitchContext
			return m, tea.Quit
		case "n":
			m.result.Action = PickerSwitchNamespace
			return m, tea.Quit
		case "r":
			m.result.Action = PickerRefresh
			return m, tea.Quit
		case "p":
			if len(m.checked) > 0 {
				m.note = "Uncheck applications before choosing exact pods."
				return m, nil
			}
			m.result.Action = PickerSelectPods
			return m, tea.Quit
		case " ":
			if len(m.apps) > 0 {
				if m.checked == nil {
					m.checked = make(map[string]bool)
				}
				name := m.apps[m.index].Name
				if m.checked[name] {
					delete(m.checked, name)
				} else {
					m.checked[name] = true
				}
				m.note = ""
			}
		case "up", "k":
			if len(m.apps) > 0 {
				m.index = max(0, m.index-1)
			}
		case "down", "j":
			if len(m.apps) > 0 {
				m.index = min(len(m.apps)-1, m.index+1)
			}
		case "enter":
			if len(m.apps) > 0 {
				selected := m.checkedChoices()
				marked := make([]string, 0, len(selected))
				for _, app := range selected {
					marked = append(marked, app.Name)
				}
				if len(selected) == 0 {
					selected = []core.AppChoice{m.apps[m.index]}
				}
				m.result = PickerResult{Action: PickerOpenApp, App: selected[0], Apps: selected, Marked: marked}
				return m, tea.Quit
			}
		}
	}
	return m, nil
}

func (m pickerModel) checkedChoices() []core.AppChoice {
	var selected []core.AppChoice
	for _, app := range m.apps {
		if m.checked[app.Name] {
			selected = append(selected, app)
		}
	}
	return selected
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
		fmt.Sprintf("Context: %s  |  Namespace: %s", m.kubeContext, m.namespace),
		renderPickerShortcuts(
			shortcut("↑↓", "Move"),
			shortcut("SPACE", "Select"),
			shortcut("ENTER", "Open"),
			shortcut("P", "Exact pods"),
		),
		renderPickerShortcuts(
			shortcut("C", "Context"),
			shortcut("N", "Namespace"),
			shortcut("R", "Retry"),
			shortcut("Q", "Quit"),
		),
		"",
	}
	if m.totalApps > len(m.apps) {
		lines = append(lines, dimStyle.Render(fmt.Sprintf("Showing newest %d of %d deployments", len(m.apps), m.totalApps)), "")
	}
	if len(m.checked) > 0 {
		podCount := 0
		for _, app := range m.checkedChoices() {
			podCount += len(app.Pods)
		}
		lines = append(lines, fmt.Sprintf("Selected: %d applications · %d current pods", len(m.checked), podCount), "")
	}
	if m.note != "" {
		lines = append(lines, m.note, "")
	}
	if len(m.apps) == 0 {
		if m.loadError != "" {
			return strings.Join(append(lines, "Could not load applications: "+m.loadError, "Press C to switch context, N to choose namespace, or R to retry."), "\n")
		}
		return strings.Join(append(lines, "No applications found in this namespace. Press C to switch context or N to choose namespace."), "\n")
	}
	now := time.Now()
	imageWidth := pickerImageWidth(m.width)
	header := renderPickerRow("  ", "APPLICATION", "READY", "PHASE", "RESTARTS", "STARTED", "DEPLOYED", "IMAGE", imageWidth)
	lines = append(lines, dimStyle.Render(header), dimStyle.Render(strings.Repeat("-", lipgloss.Width(header))))
	for index, app := range m.apps {
		marker := "  "
		check := "[ ] "
		if m.checked[app.Name] {
			check = "[x] "
		}
		style := lipgloss.NewStyle()
		if index == m.index {
			marker = "> "
			style = selectedStyle
		}
		line := renderPickerRow(
			marker+check,
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

// kubectl may emit repeated discovery cache errors before its useful final
// message. Show that final line so the picker stays readable during recovery.
func pickerErrorSummary(err error) string {
	lines := strings.Split(strings.TrimSpace(err.Error()), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

func preparePickerApps(apps []core.AppChoice) ([]core.AppChoice, int) {
	total := len(apps)
	prepared := append([]core.AppChoice(nil), apps...)
	sort.SliceStable(prepared, func(i, j int) bool {
		iTime := prepared[i].DeployedAt
		jTime := prepared[j].DeployedAt
		if iTime.IsZero() != jTime.IsZero() {
			return !iTime.IsZero()
		}
		if !iTime.Equal(jTime) {
			return iTime.After(jTime)
		}
		return strings.ToLower(prepared[i].Name) < strings.ToLower(prepared[j].Name)
	})
	if len(prepared) > maxPickerApps {
		prepared = prepared[:maxPickerApps]
	}
	return prepared, total
}

func pickerImageWidth(terminalWidth int) int {
	// marker, selection checkbox, fixed columns, and six inter-column spaces.
	fixed := 6 + pickerAppWidth + pickerReadyWidth + pickerPhaseWidth + pickerRestartsWidth + 2*pickerAgeWidth + 6
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
