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
	apps              []core.AppChoice
	totalApps         int
	index             int
	checked           map[string]bool
	note              string
	result            PickerResult
	kubeContext       string
	namespace         string
	loadError         string
	width             int
	height            int
	confirmRun        *core.AppChoice
	confirmRunFocused bool
	confirmRunScroll  int
	podMonitorState
	monitorApps map[string]podHealthDisplay
}

type PickerAction int

const (
	PickerQuit PickerAction = iota
	PickerOpenApp
	PickerSwitchContext
	PickerSwitchNamespace
	PickerRefresh
	PickerSelectPods
	PickerRunCronJob
)

type PickerResult struct {
	Action PickerAction
	App    core.AppChoice
	Apps   []core.AppChoice
	Pods   []string
	Marked []string
}

func PickApp(apps []core.AppChoice, kubeContext, namespace string, loadErr error, previous []string, notice ...string) (PickerResult, error) {
	prepared, total := preparePickerApps(apps)
	model := newPickerModel(prepared, total, kubeContext, namespace, previous)
	if len(notice) > 0 {
		model.note = notice[0]
	}
	if loadErr != nil {
		model.loadError = pickerErrorSummary(loadErr)
	}
	result, err := tea.NewProgram(model).Run()
	if final, ok := result.(pickerModel); ok {
		final.podMonitorState.stop()
	}
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
		if previousSet[app.Name] && (app.CronJob == nil || len(app.Pods) > 0) {
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
	// Aggregate before updating previous pod samples so warning trends compare
	// the current minute with the preceding scan across the whole application.
	if msg, ok := message.(podHealthScanMsg); ok && m.monitorEnabled && msg.generation == m.monitorGeneration {
		m.monitorApps = make(map[string]podHealthDisplay, len(m.apps))
		for _, app := range m.apps {
			m.monitorApps[app.Name] = classifyAppHealth(app, msg.samples, m.monitorPrevious)
		}
	}
	if handled, cmd := m.podMonitorState.update(message, preparePodPickerRows(m.apps), m.kubeContext, m.namespace); handled {
		return m, cmd
	}
	if size, ok := message.(tea.WindowSizeMsg); ok {
		m.width = size.Width
		m.height = size.Height
		if m.confirmRun != nil && size.Width >= minCronJobModalWidth && size.Height >= minCronJobModalHeight {
			_, maxScroll := m.cronJobModalLayout(size.Width, size.Height)
			m.confirmRunScroll = min(m.confirmRunScroll, maxScroll)
		}
		return m, nil
	}
	if key, ok := message.(tea.KeyMsg); ok {
		if m.confirmRun != nil {
			return m.updateCronJobModal(key)
		}
		switch key.String() {
		case "ctrl+c", "q", "esc":
			m.podMonitorState.stop()
			m.result.Action = PickerQuit
			return m, tea.Quit
		case "c":
			m.podMonitorState.stop()
			m.result.Action = PickerSwitchContext
			return m, tea.Quit
		case "n":
			m.podMonitorState.stop()
			m.result.Action = PickerSwitchNamespace
			return m, tea.Quit
		case "r":
			m.podMonitorState.stop()
			m.result.Action = PickerRefresh
			return m, tea.Quit
		case "x", "X":
			if len(m.apps) > 0 && m.apps[m.index].CronJob != nil {
				app := m.apps[m.index]
				m.confirmRun = &app
				m.confirmRunFocused = false // Cancel is focused until explicitly changed.
				m.confirmRunScroll = 0
				m.note = ""
			} else {
				m.note = "Highlight a CronJob to run it on demand."
			}
		case "p":
			if len(m.checked) > 0 {
				m.note = "Uncheck applications before choosing exact pods."
				return m, nil
			}
			m.podMonitorState.stop()
			m.result.Action = PickerSelectPods
			return m, tea.Quit
		case "m", "M":
			m.monitorApps = nil
			return m, m.podMonitorState.toggle(preparePodPickerRows(m.apps), m.kubeContext, m.namespace)
		case " ":
			if len(m.apps) > 0 {
				if m.apps[m.index].CronJob != nil && len(m.apps[m.index].Pods) == 0 {
					m.note = "This CronJob has no retained pods. Press X to run it now."
					return m, nil
				}
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
				if len(selected) == 1 && selected[0].CronJob != nil && len(selected[0].Pods) == 0 {
					m.note = "This CronJob has no retained pods. Press X to run it now."
					return m, nil
				}
				m.result = PickerResult{Action: PickerOpenApp, App: selected[0], Apps: selected, Marked: marked}
				m.podMonitorState.stop()
				return m, tea.Quit
			}
		}
	}
	return m, nil
}

func (m pickerModel) markedNames() []string {
	var names []string
	for _, app := range m.checkedChoices() {
		names = append(names, app.Name)
	}
	return names
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
	background := m.pickerView()
	if m.confirmRun == nil {
		return background
	}
	width, height := m.modalViewport()
	dialog, _ := m.cronJobModalLayout(width, height)
	return overlayModal(background, dialog, width, height)
}

func (m pickerModel) pickerView() string {
	var lines = []string{
		headerStyle.Render("Select an application"),
		fmt.Sprintf("Context: %s  |  Namespace: %s", m.kubeContext, m.namespace),
		renderPickerShortcuts(
			shortcut("↑↓", "Move"),
			shortcut("SPACE", "Select"),
			shortcut("ENTER", "Open"),
			shortcut("P", "Exact pods"),
			shortcut("M", "Monitor"),
		),
		renderPickerShortcuts(
			shortcut("C", "Context"),
			shortcut("N", "Namespace"),
			shortcut("R", "Retry"),
			shortcut("X", "Run now"),
			shortcut("Q", "Quit"),
		),
		podMonitorSummary(m.monitorEnabled, m.monitorBusy, m.monitorLastScan, m.monitorError),
		"",
	}
	if m.totalApps > len(m.apps) {
		cronJobs := 0
		for _, app := range m.apps {
			if app.CronJob != nil {
				cronJobs++
			}
		}
		lines = append(lines, dimStyle.Render(fmt.Sprintf("Showing newest %d of %d applications and all %d CronJobs", len(m.apps)-cronJobs, m.totalApps-cronJobs, cronJobs)), "")
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
	if m.loadError != "" {
		lines = append(lines, "Inventory warning: "+m.loadError, "")
	}
	now := time.Now()
	imageWidth := pickerImageWidth(m.width)
	// Reserve room for both section headers, the footer, and a selected row's
	// full schedule details. Scroll so CronJobs remain reachable in short terminals.
	capacity := len(m.apps)
	if m.height > 0 {
		capacity = max(1, m.height-len(lines)-8)
	}
	start := max(0, m.index-capacity+1)
	end := min(len(m.apps), start+capacity)
	for index := start; index < end; index++ {
		app := m.apps[index]
		if index == start || (app.CronJob != nil) != (m.apps[index-1].CronJob != nil) {
			header := renderPickerRow("      ", "APPLICATION", "READY", "PHASE", "RESTARTS", "STARTED", "DEPLOYED", "IMAGE", imageWidth)
			if app.CronJob != nil {
				header = renderCronJobRow("      ", "CRONJOB", "SCHEDULE", "TIME ZONE", "STATE", "ACTIVE", "PODS", "LAST SCHEDULED")
			}
			lines = append(lines, dimStyle.Render(header), dimStyle.Render(strings.Repeat("-", lipgloss.Width(header))))
		}
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
		if app.CronJob != nil {
			job := app.CronJob
			state, zone, last := "Scheduled", job.TimeZone, "never"
			if job.Suspended {
				state = "Suspended"
			}
			if zone == "" {
				zone = "controller local"
			}
			if !job.LastScheduledAt.IsZero() {
				last = formatPickerAge(now, job.LastScheduledAt)
			}
			line = renderCronJobRow(marker+check, job.Name, job.Schedule, zone, state, fmt.Sprint(job.Active), fmt.Sprint(len(app.Pods)), last)
		}
		if m.monitorEnabled {
			// Style cells separately so cursor highlighting cannot replace the
			// application's health color. Keep the existing fixed column widths.
			prefix := marker + check
			displayName := app.Name
			if app.CronJob != nil {
				displayName = app.CronJob.Name
			}
			name := fmt.Sprintf("%-*s", pickerAppWidth, truncatePickerCell(displayName, pickerAppWidth))
			suffix := strings.TrimPrefix(line, prefix+name)
			name = renderMonitoredPodName(name, m.monitorApps[app.Name], m.monitorFlashOn)
			lines = append(lines, style.Render(prefix)+name+style.Render(suffix))
		} else {
			lines = append(lines, style.Render(line))
		}
	}
	if start > 0 || end < len(m.apps) {
		lines = append(lines, dimStyle.Render(fmt.Sprintf("Rows %d–%d of %d · ↑↓ to scroll", start+1, end, len(m.apps))))
	}
	if m.index < len(m.apps) && m.apps[m.index].CronJob != nil {
		job := m.apps[m.index].CronJob
		zone, last := job.TimeZone, "never"
		if zone == "" {
			zone = "controller local"
		}
		if !job.LastScheduledAt.IsZero() {
			last = job.LastScheduledAt.Local().Format("2006-01-02 15:04:05 MST")
		}
		lines = append(lines, fmt.Sprintf("%s · Schedule: %s (%s) · Last scheduled: %s", job.Name, job.Schedule, zone, last))
	}
	if m.monitorEnabled {
		lines = append(lines, dimStyle.Render(podMonitorLegend()))
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
		if (prepared[i].CronJob != nil) != (prepared[j].CronJob != nil) {
			return prepared[i].CronJob == nil
		}
		if prepared[i].CronJob != nil {
			return strings.ToLower(prepared[i].Name) < strings.ToLower(prepared[j].Name)
		}
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
	visible := make([]core.AppChoice, 0, len(prepared))
	appCount := 0
	for _, app := range prepared {
		if app.CronJob != nil || appCount < maxPickerApps {
			visible = append(visible, app)
		}
		if app.CronJob == nil {
			appCount++
		}
	}
	return visible, total
}

func renderCronJobRow(marker, name, schedule, zone, state, active, pods, last string) string {
	return fmt.Sprintf("%s%-36s %-22s %-26s %-10s %6s %6s %-18s", marker,
		truncatePickerCell(name, 36), truncatePickerCell(schedule, 22), truncatePickerCell(zone, 26), state, active, pods, last)
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
