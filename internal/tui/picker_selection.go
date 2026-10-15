package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/fernandocpaz/tailg/internal/core"
	"github.com/fernandocpaz/tailg/internal/kube"
)

type pickerSelectionModal struct {
	kind       string
	target     string
	loading    bool
	err        string
	cancel     context.CancelFunc
	contexts   *contextPickerModel
	namespaces *namespacePickerModel
	pods       *podPickerModel
}

type pickerChoicesResult struct {
	scope      string
	request    int
	contexts   []kube.ContextInfo
	namespaces []string
	err        error
}

type pickerScopeResult struct {
	fromScope                string
	request                  int
	kubeContext, namespace   string
	apps                     []core.AppChoice
	resolveErr, inventoryErr error
}

func (m *pickerModel) closePickerSelection() {
	if m.selection != nil && m.selection.cancel != nil {
		m.selection.cancel()
	}
	m.selection = nil
}

func (m *pickerModel) selectionContext() (context.Context, context.CancelFunc) {
	ctx := m.refreshContext
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithTimeout(ctx, 15*time.Second)
}

func (m *pickerModel) openPickerSelection(kind string) tea.Cmd {
	m.closePickerSelection()
	m.selectionRequest++
	m.selection = &pickerSelectionModal{kind: kind, loading: kind != "pods"}
	if kind == "pods" {
		m.selection.pods = &podPickerModel{rows: preparePodPickerRows(m.allApps), kubeContext: m.kubeContext, namespace: m.namespace}
		return nil
	}
	ctx, cancel := m.selectionContext()
	m.selection.cancel = cancel
	scope, request := activityScope(m.kubeContext, m.namespace), m.selectionRequest
	runner := kube.NewRunner(m.namespace, m.kubeContext)
	return func() tea.Msg {
		defer cancel()
		result := pickerChoicesResult{scope: scope, request: request}
		if kind == "context" {
			result.contexts, result.err = runner.Contexts(ctx)
		} else {
			result.namespaces, result.err = runner.Namespaces(ctx)
		}
		return result
	}
}

func (m *pickerModel) selectPickerScope(kubeContext, namespace string) tea.Cmd {
	if kubeContext == m.kubeContext && namespace == m.namespace {
		m.closePickerSelection()
		return nil
	}
	if m.selection.cancel != nil {
		m.selection.cancel()
	}
	ctx, cancel := m.selectionContext()
	m.selection.cancel, m.selection.loading, m.selection.err = cancel, true, ""
	m.selection.target = kubeContext + " / " + namespace
	m.selectionRequest++
	request, scope := m.selectionRequest, activityScope(m.kubeContext, m.namespace)
	return func() tea.Msg {
		defer cancel()
		result := pickerScopeResult{fromScope: scope, request: request, kubeContext: kubeContext, namespace: namespace}
		runner := kube.NewRunner(namespace, kubeContext)
		if namespace == "" {
			_, result.namespace, result.resolveErr = runner.CurrentContext(ctx)
			if result.resolveErr != nil {
				return result
			}
			runner.Namespace = result.namespace
		}
		result.apps, result.inventoryErr = runner.Apps(ctx)
		return result
	}
}

func (m *pickerModel) updatePickerSelectionMessage(message tea.Msg) (bool, tea.Cmd) {
	switch msg := message.(type) {
	case pickerChoicesResult:
		if m.selection == nil || msg.request != m.selectionRequest || msg.scope != activityScope(m.kubeContext, m.namespace) {
			return true, nil
		}
		m.selection.loading = false
		if m.selection.kind == "context" {
			if msg.err != nil {
				m.selection.err = pickerErrorSummary(msg.err)
				return true, nil
			}
			child := newContextPickerModel(msg.contexts, m.kubeContext, m.usage.Contexts)
			m.selection.contexts = &child
			if len(child.contexts) == 0 {
				m.selection.err = "No Kubernetes contexts found. Press R to retry."
			}
		} else {
			child := newNamespacePickerModel(msg.namespaces, m.namespace, msg.err, m.usage.Namespaces[m.kubeContext])
			m.selection.namespaces = &child
			m.resizePickerSelectionInput()
		}
		return true, nil
	case pickerScopeResult:
		if m.selection == nil || msg.request != m.selectionRequest || msg.fromScope != activityScope(m.kubeContext, m.namespace) {
			return true, nil
		}
		if msg.resolveErr != nil {
			m.selection.loading, m.selection.err = false, pickerErrorSummary(msg.resolveErr)
			if m.selection.contexts != nil {
				m.selection.contexts.done = false
				m.selection.contexts.selected = ""
			}
			return true, nil
		}
		m.closePickerSelection()
		m.podMonitorState.stop()
		m.monitorApps = nil
		m.kubeContext, m.namespace = msg.kubeContext, msg.namespace
		m.index, m.checked, m.note, m.loadError = 0, nil, "", ""
		m.apps, m.totalApps = preparePickerApps(msg.apps)
		m.allApps = msg.apps
		if msg.inventoryErr != nil {
			m.loadError = pickerErrorSummary(msg.inventoryErr)
		}
		// Invalidate old inventory and activity requests, even after A → B → A.
		m.inventoryRequest++
		m.inventoryBusy = false
		m.activity = namespaceActivityState{request: m.activity.request + 1}
		return true, tea.Batch(m.pickerRefreshAfter(), m.refreshNamespaceActivity())
	}
	// Text input also needs its blink and paste messages while the parent keeps
	// processing inventory, monitoring and terminal-size messages normally.
	if m.selection != nil && m.selection.namespaces != nil && m.selection.namespaces.entering {
		if _, key := message.(tea.KeyMsg); !key {
			child, cmd := m.selection.namespaces.Update(message)
			*m.selection.namespaces = child.(namespacePickerModel)
			if cmd != nil {
				return true, cmd
			}
		}
	}
	return false, nil
}

func (m pickerModel) updatePickerSelectionKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "ctrl+c" {
		m.closePickerSelection()
		m.podMonitorState.stop()
		m.result.Action = PickerQuit
		return m, tea.Quit
	}
	state := m.selection
	width, height := m.modalViewport()
	if state.loading || width < 40 || height < 12 {
		if msg.String() == "esc" || msg.String() == "q" {
			m.closePickerSelection()
		}
		return m, nil
	}
	if state.namespaces == nil || !state.namespaces.entering {
		if msg.String() == "r" || msg.String() == "R" {
			if state.kind == "pods" {
				return m, m.refreshPickerInventory()
			}
			return m, m.openPickerSelection(state.kind)
		}
	}
	if state.contexts != nil {
		child, cmd := state.contexts.Update(msg)
		*state.contexts = child.(contextPickerModel)
		if state.contexts.done {
			selected := state.contexts.selected
			if selected == "" || selected == m.kubeContext {
				m.closePickerSelection()
				return m, nil
			}
			return m, m.selectPickerScope(selected, "")
		}
		return m, cmd
	}
	if state.namespaces != nil {
		child, cmd := state.namespaces.Update(msg)
		*state.namespaces = child.(namespacePickerModel)
		if state.namespaces.done {
			selected := state.namespaces.selected
			if selected == "" {
				m.closePickerSelection()
				return m, nil
			}
			return m, m.selectPickerScope(m.kubeContext, selected)
		}
		return m, cmd
	}
	if state.pods != nil {
		if msg.String() == "m" || msg.String() == "M" {
			m.monitorApps = nil
			return m, m.podMonitorState.toggle(state.pods.rows, m.kubeContext, m.namespace)
		}
		child, cmd := state.pods.Update(msg)
		*state.pods = child.(podPickerModel)
		if state.pods.done {
			pods := state.pods.selected
			m.closePickerSelection()
			if len(pods) > 0 {
				m.result = PickerResult{Action: PickerOpenApp, Pods: pods}
				m.podMonitorState.stop()
				return m, tea.Quit
			}
		}
		return m, cmd
	}
	if msg.String() == "esc" || msg.String() == "q" {
		m.closePickerSelection()
	}
	return m, nil
}

func (m pickerModel) pickerMonitorRows() []podPickerRow {
	if m.selection != nil && m.selection.pods != nil {
		return m.selection.pods.rows
	}
	return preparePodPickerRows(m.apps)
}

func (m *pickerModel) resizePickerSelectionInput() {
	if m.selection == nil || m.selection.namespaces == nil {
		return
	}
	width, _ := m.modalViewport()
	input := &m.selection.namespaces.input
	input.Width = max(1, min(96, width-6)-lipgloss.Width(input.Prompt)-2)
	input.SetCursor(input.Position())
}

func (m pickerModel) pickerSelectionView() string {
	width, height := m.modalViewport()
	if width < 40 || height < 12 {
		return ansi.Wrap("Resize to at least 40 × 12.\nEsc closes.", max(1, width), "")
	}
	state := m.selection
	inner := min(96, width-6)
	title := map[string]string{"context": "Switch Kubernetes context", "namespace": "Switch Kubernetes namespace", "pods": "Select exact pods"}[state.kind]
	header := []string{headerStyle.Render(title), truncatePickerCell(m.kubeContext+" / "+m.namespace, inner)}
	var rows []string
	index, count := 0, 0
	selected := ""
	controls := "↑↓ Move · Enter Choose · Esc Back · R Retry"
	if state.loading {
		rows = []string{truncatePickerCell("Loading… "+state.target, inner)}
		controls = "Esc Cancel · Ctrl+C Quit"
	} else if state.contexts != nil {
		child := state.contexts
		index, count = child.index, visibleChoiceCount(len(child.contexts), child.expanded)
		for i := 0; i < count; i++ {
			label := ""
			if isMoreChoice(i, len(child.contexts), child.expanded) {
				label = fmt.Sprintf("More… (%d other contexts)", len(child.contexts)-frequentChoices)
			} else {
				entry := child.contexts[i]
				label = entry.Name + "  /  " + entry.Namespace
			}
			rows = append(rows, selectionChoiceRow(label, inner, i == index))
		}
		if count > 0 && !isMoreChoice(index, len(child.contexts), child.expanded) {
			selected = child.contexts[index].Name
		}
	} else if state.namespaces != nil {
		child := state.namespaces
		index, count = child.index, visibleChoiceCount(len(child.namespaces), child.expanded)
		controls = "↑↓ Move · Enter Choose · / Name · Esc Back"
		if child.loadError != "" {
			header = append(header, pickerLimitedLines("Namespace list unavailable: "+child.loadError, inner, 2)...)
		}
		if child.entering {
			header = append(header, child.input.View())
			controls = "Enter Use name · Esc List · Ctrl+U Clear"
		}
		for i := 0; i < count; i++ {
			label := ""
			if isMoreChoice(i, len(child.namespaces), child.expanded) {
				label = fmt.Sprintf("More… (%d other namespaces)", len(child.namespaces)-frequentChoices)
			} else {
				label = child.namespaces[i]
			}
			rows = append(rows, selectionChoiceRow(label, inner, i == index))
		}
		if count > 0 && !isMoreChoice(index, len(child.namespaces), child.expanded) {
			selected = child.namespaces[index]
		}
	} else if state.pods != nil {
		child := state.pods
		index, count = child.index, len(child.rows)
		header = append(header, truncatePickerCell(fmt.Sprintf("Selected: %d pods · pinned · %s", child.checkedCount(), podMonitorSummary(m.monitorEnabled, m.monitorBusy, m.monitorLastScan, m.monitorError)), inner))
		controls = "Space Mark · A App · Enter Logs · M Monitor · Esc Back"
		for i, row := range child.rows {
			label := row.pod.Name
			if inner >= 70 {
				label = pickerCell(row.app, 24) + " " + row.pod.Name
			}
			line := selectionRow(label, inner, i == index, child.checked[row.pod.Name])
			if m.monitorEnabled {
				prefix := ansi.Cut(line, 0, 6)
				name := renderMonitoredPodName(pickerCell(label, inner-6), m.monitorHealth[row.pod.Name], m.monitorFlashOn)
				line = prefix + name
			}
			rows = append(rows, line)
		}
		if count > 0 {
			row := child.rows[index]
			selected = row.app + " / " + row.pod.Name + " · " + valueOrDash(row.pod.Ready) + " · " + valueOrDash(row.pod.Phase)
		}
	}
	if state.err != "" {
		header = append(header, pickerLimitedLines(state.err, inner, 2)...)
	}
	footer := pickerLimitedLines(selected, inner, 1)
	footer = append(footer, strings.Split(ansi.Wrap(controls, inner, ""), "\n")...)
	// Reserve the border, headings and controls, then scroll around the cursor.
	capacity := max(1, height-4-len(header)-len(footer))
	if len(rows) == 0 {
		rows = []string{truncatePickerCell("No choices available. R retries.", inner)}
	}
	start := min(max(0, index-capacity/2), max(0, len(rows)-capacity))
	end := min(len(rows), start+capacity)
	lines := append(header, rows[start:end]...)
	lines = append(lines, footer...)
	content := strings.Join(lines, "\n")
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(accentColor).
		Foreground(modalForeground).Background(modalBackground).Padding(0, 1).Width(inner + 2).Render(content)
}

func selectionChoiceRow(label string, width int, selected bool) string {
	marker := "  "
	if selected {
		marker = "> "
	}
	line := marker + pickerCell(label, width-2)
	if selected {
		return selectedStyle.Render(line)
	}
	return line
}

func selectionRow(label string, width int, selected, checked bool) string {
	prefix := "  [ ] "
	if checked {
		prefix = "  [x] "
	}
	if selected {
		prefix = "> " + prefix[2:]
	}
	line := prefix + pickerCell(label, width-6)
	if selected {
		return selectedStyle.Render(line)
	}
	return line
}
