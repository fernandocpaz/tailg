package tui

import (
	"context"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/fernandocpaz/tailg/internal/core"
	"github.com/fernandocpaz/tailg/internal/kube"
)

const pickerRefreshInterval = 5 * time.Second

type pickerRefreshTick struct {
	scope   string
	request int
}
type pickerRefreshResult struct {
	scope   string
	request int
	apps    []core.AppChoice
	err     error
}

func (m pickerModel) pickerRefreshAfter() tea.Cmd {
	scope, request := activityScope(m.kubeContext, m.namespace), m.inventoryRequest
	return tea.Tick(pickerRefreshInterval, func(time.Time) tea.Msg { return pickerRefreshTick{scope: scope, request: request} })
}

func (m *pickerModel) refreshPickerInventory() tea.Cmd {
	if m.inventoryBusy {
		return nil
	}
	m.inventoryBusy = true
	m.inventoryRequest++
	ctx := m.refreshContext
	if ctx == nil {
		ctx = context.Background()
	}
	kubeContext, namespace, request := m.kubeContext, m.namespace, m.inventoryRequest
	return func() tea.Msg {
		limited, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		apps, err := kube.NewRunner(namespace, kubeContext).Apps(limited)
		return pickerRefreshResult{scope: activityScope(kubeContext, namespace), request: request, apps: apps, err: err}
	}
}

func (m *pickerModel) refreshApps(msg pickerRefreshResult) {
	if msg.err != nil && len(msg.apps) == 0 {
		m.loadError = pickerErrorSummary(msg.err)
		return
	}
	name := ""
	if m.index < len(m.apps) {
		name = m.apps[m.index].Name
	}
	m.apps, m.totalApps = preparePickerApps(msg.apps)
	m.allApps = append([]core.AppChoice(nil), msg.apps...)
	m.loadError = ""
	if msg.err != nil {
		m.loadError = pickerErrorSummary(msg.err)
	}
	m.index = min(m.index, max(0, len(m.apps)-1))
	for i, app := range m.apps {
		if app.Name == name {
			m.index = i
			break
		}
	}
}

func (m *podPickerModel) refreshPods(msg pickerRefreshResult) {
	if msg.err != nil && len(msg.apps) == 0 {
		return
	}
	name := ""
	if m.index < len(m.rows) {
		name = m.rows[m.index].pod.Name
	}
	m.rows = preparePodPickerRows(msg.apps)
	m.index = min(m.index, max(0, len(m.rows)-1))
	live := map[string]bool{}
	for i, row := range m.rows {
		live[row.pod.Name] = true
		if row.pod.Name == name {
			m.index = i
		}
	}
	for pod := range m.checked {
		if !live[pod] {
			delete(m.checked, pod)
		}
	}
}
