package tui

import (
	"context"
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
	refreshContext    context.Context
	apps              []core.AppChoice
	allApps           []core.AppChoice
	selection         *pickerSelectionModal
	selectionRequest  int
	usage             PickerUsage
	inventoryRequest  int
	inventoryBusy     bool
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
	helpVisible       bool
	helpScroll        int
	confirmRun        *core.AppChoice
	confirmRunFocused bool
	confirmRunScroll  int
	podMonitorState
	monitorApps map[string]podHealthDisplay
	activity    namespaceActivityState
}

type PickerAction int

const (
	PickerQuit PickerAction = iota
	PickerOpenApp
	PickerRunCronJob
)

type PickerResult struct {
	Action      PickerAction
	App         core.AppChoice
	Apps        []core.AppChoice
	Pods        []string
	Marked      []string
	KubeContext string
	Namespace   string
}

type PickerUsage struct {
	Contexts   map[string]ChoiceUsage
	Namespaces map[string]map[string]ChoiceUsage
}

func PickApp(apps []core.AppChoice, kubeContext, namespace string, loadErr error, previous []string, usage PickerUsage, notice ...string) (PickerResult, error) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	prepared, total := preparePickerApps(apps)
	model := newPickerModel(prepared, total, kubeContext, namespace, previous)
	model.allApps = apps
	model.usage = usage
	model.refreshContext = ctx
	model.activity.busy = true
	model.activity.request = 1
	if len(notice) > 0 {
		model.note = notice[0]
	}
	if loadErr != nil {
		model.loadError = pickerErrorSummary(loadErr)
	}
	result, err := tea.NewProgram(model, tea.WithAltScreen()).Run()
	if final, ok := result.(pickerModel); ok {
		final.podMonitorState.stop()
		final.closePickerSelection()
	}
	if err != nil {
		return PickerResult{}, err
	}
	final := result.(pickerModel)
	final.result.KubeContext, final.result.Namespace = final.kubeContext, final.namespace
	return final.result, nil
}

func newPickerModel(apps []core.AppChoice, total int, kubeContext, namespace string, previous []string) pickerModel {
	model := pickerModel{apps: apps, allApps: apps, totalApps: total, kubeContext: kubeContext, namespace: namespace}
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
func (m pickerModel) Init() tea.Cmd {
	if m.refreshContext == nil {
		return nil
	}
	return tea.Batch(m.pickerRefreshAfter(), loadNamespaceActivity(m.refreshContext, m.kubeContext, m.namespace, m.activity.request))
}
func (m pickerModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	if handled, cmd := m.updatePickerSelectionMessage(message); handled {
		return m, cmd
	}
	if handled, cmd := m.updateNamespaceActivity(message); handled {
		return m, cmd
	}
	switch msg := message.(type) {
	case pickerRefreshTick:
		if m.refreshContext == nil || msg.scope != activityScope(m.kubeContext, m.namespace) || msg.request != m.inventoryRequest {
			return m, nil
		}
		// Keep the confirmation tied to the workload shown when it was opened.
		if m.confirmRun != nil {
			return m, m.pickerRefreshAfter()
		}
		return m, m.refreshPickerInventory()
	case pickerRefreshResult:
		if msg.scope != activityScope(m.kubeContext, m.namespace) || msg.request != m.inventoryRequest || !m.inventoryBusy {
			return m, nil
		}
		m.inventoryBusy = false
		if m.confirmRun == nil {
			m.refreshApps(msg)
			if m.selection != nil && m.selection.pods != nil {
				m.selection.pods.refreshPods(msg)
			}
		}
		return m, m.pickerRefreshAfter()
	}

	// Aggregate before updating previous pod samples so warning trends compare
	// the current minute with the preceding scan across the whole application.
	if msg, ok := message.(podHealthScanMsg); ok && m.monitorEnabled && msg.generation == m.monitorGeneration {
		m.monitorApps = make(map[string]podHealthDisplay, len(m.apps))
		for _, app := range m.apps {
			m.monitorApps[app.Name] = classifyAppHealth(app, msg.samples, m.monitorPrevious)
			if count := m.monitorApps[app.Name].sample.errors; count > 0 {
				at := msg.scannedAt
				if at.IsZero() {
					at = time.Now()
				}
				m.activity.tracker.Record(core.NamespaceChange{ID: fmt.Sprintf("log-errors/%s/%d", app.Name, at.UnixNano()), At: at, Object: app.Name, Action: "Log errors observed", Detail: fmt.Sprintf("%d errors in the preceding minute across monitored pods of this displayed application; this observation does not establish when the issue began", count), Observed: true})
			}
		}
	}
	if handled, cmd := m.podMonitorState.update(message, m.pickerMonitorRows(), m.kubeContext, m.namespace); handled {
		return m, cmd
	}
	if size, ok := message.(tea.WindowSizeMsg); ok {
		m.width = size.Width
		m.height = size.Height
		m.resizePickerSelectionInput()
		if m.confirmRun != nil && size.Width >= minCronJobModalWidth && size.Height >= minCronJobModalHeight {
			_, maxScroll := m.cronJobModalLayout(size.Width, size.Height)
			m.confirmRunScroll = min(m.confirmRunScroll, maxScroll)
		}
		return m, nil
	}
	if key, ok := message.(tea.KeyMsg); ok {
		if m.selection != nil {
			return m.updatePickerSelectionKey(key)
		}
		if m.confirmRun != nil {
			return m.updateCronJobModal(key)
		}
		if m.activity.view != "" {
			return m.updateNamespaceActivityKey(key)
		}
		if m.helpVisible {
			return m.updatePickerHelp(key)
		}
		switch {
		case keyMatches(key, pickerKeys.help):
			m.helpVisible, m.helpScroll = true, 0
		case keyMatches(key, pickerKeys.changes, pickerKeys.events):
			m.activity.view = "changes"
			if strings.EqualFold(key.String(), "e") {
				m.activity.view = "events"
			}
			m.activity.selected = 0
			cmd := m.refreshNamespaceActivity()
			return m, cmd
		case keyMatches(key, pickerKeys.quit):
			m.podMonitorState.stop()
			m.result.Action = PickerQuit
			return m, tea.Quit
		case keyMatches(key, pickerKeys.context):
			return m, m.openPickerSelection("context")
		case keyMatches(key, pickerKeys.namespace):
			return m, m.openPickerSelection("namespace")
		case keyMatches(key, pickerKeys.refresh):
			return m, m.refreshPickerInventory()
		case keyMatches(key, pickerKeys.run):
			if len(m.apps) > 0 && m.apps[m.index].CronJob != nil {
				app := m.apps[m.index]
				m.confirmRun = &app
				m.confirmRunFocused = false // Cancel is focused until explicitly changed.
				m.confirmRunScroll = 0
				m.note = ""
			} else {
				m.note = "Highlight a CronJob to run it on demand."
			}
		case keyMatches(key, pickerKeys.pods):
			if len(m.checked) > 0 {
				m.note = "Uncheck applications before choosing exact pods."
				return m, nil
			}
			return m, m.openPickerSelection("pods")
		case keyMatches(key, pickerKeys.monitor):
			m.monitorApps = nil
			return m, m.podMonitorState.toggle(preparePodPickerRows(m.apps), m.kubeContext, m.namespace)
		case keyMatches(key, pickerKeys.mark):
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
		case keyMatches(key, pickerKeys.up):
			if len(m.apps) > 0 {
				m.index = max(0, m.index-1)
			}
		case keyMatches(key, pickerKeys.down):
			if len(m.apps) > 0 {
				m.index = min(len(m.apps)-1, m.index+1)
			}
		case keyMatches(key, pickerKeys.pageUp, pickerKeys.pageDown, pickerKeys.home, pickerKeys.end):
			step := max(1, m.height/2)
			switch {
			case keyMatches(key, pickerKeys.pageUp):
				m.index = max(0, m.index-step)
			case keyMatches(key, pickerKeys.pageDown):
				m.index = min(max(0, len(m.apps)-1), m.index+step)
			case keyMatches(key, pickerKeys.home):
				m.index = 0
			case keyMatches(key, pickerKeys.end):
				m.index = max(0, len(m.apps)-1)
			}
		case keyMatches(key, pickerKeys.open):
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

const pickerAppWidth = 36

func (m pickerModel) View() string {
	background := m.pickerView()
	if m.selection != nil {
		width, height := m.modalViewport()
		return overlayModal(background, m.pickerSelectionView(), width, height)
	}
	if m.helpVisible {
		width, height := m.modalViewport()
		dialog, _ := m.pickerHelpModal()
		return overlayModal(background, dialog, width, height)
	}
	if m.activity.view != "" {
		width, height := m.modalViewport()
		dialog, _ := m.namespaceActivityModal()
		return overlayModal(background, dialog, width, height)
	}
	if m.confirmRun == nil {
		return background
	}
	width, height := m.modalViewport()
	dialog, _ := m.cronJobModalLayout(width, height)
	return overlayModal(background, dialog, width, height)
}

func (m pickerModel) pickerView() string {
	return m.pickerScreen()
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
