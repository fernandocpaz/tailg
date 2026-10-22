package tui

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/fernandocpaz/tailg/internal/core"
	"github.com/fernandocpaz/tailg/internal/kube"
)

const (
	namespaceMetricsInterval = 15 * time.Second
	namespaceMetricsTimeout = 10 * time.Second
)

type namespaceMetricsPollMsg struct{ generation int }
type namespaceMetricsResultMsg struct {
	generation int
	scope string
	metrics kube.NamespaceResourceMetrics
	at time.Time
	err error
}

func namespaceMetricsPollCmd(generation int) tea.Cmd {
	return tea.Tick(namespaceMetricsInterval, func(time.Time) tea.Msg {
		return namespaceMetricsPollMsg{generation: generation}
	})
}

func namespaceMetricsReadCmd(parent context.Context, namespace, kubeContext string, generation int) tea.Cmd {
	scope := activityScope(kubeContext, namespace)
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(parent, namespaceMetricsTimeout)
		defer cancel()
		metrics, err := kube.NewRunner(namespace, kubeContext).NamespaceResourceMetrics(ctx)
		return namespaceMetricsResultMsg{generation: generation, scope: scope, at: time.Now(), metrics: metrics, err: err}
	}
}

func (m *podMonitorState) updateMetrics(msg tea.Msg, kubeContext, namespace string) (bool, tea.Cmd) {
	switch update := msg.(type) {
	case namespaceMetricsPollMsg:
		if !m.monitorEnabled || update.generation != m.monitorGeneration {
			return true, nil
		}
		commands := []tea.Cmd{namespaceMetricsPollCmd(m.monitorGeneration)}
		if !m.metricsBusy {
			m.metricsBusy = true
			commands = append(commands, namespaceMetricsReadCmd(m.monitorContext, namespace, kubeContext, m.monitorGeneration))
		}
		return true, tea.Batch(commands...)
	case namespaceMetricsResultMsg:
		if !m.monitorEnabled || update.generation != m.monitorGeneration || update.scope != activityScope(kubeContext, namespace) {
			return true, nil
		}
		m.metricsBusy = false
		m.metricsAt = update.at
		if update.err != nil {
			// Do not reuse readings from previous successful polls: they would
			// look current even though the Metrics API is now unavailable.
			m.metricsPods = nil
			m.metricsWarning = "unavailable (Metrics Server / RBAC)"
			return true, nil
		}
		m.metricsPods = update.metrics.Pods
		m.metricsWarning = update.metrics.LimitsWarning
		return true, nil
	}
	return false, nil
}

func (m podMonitorState) metricsSummary() string {
	if !m.monitorEnabled {
		return ""
	}
	if m.metricsBusy && m.metricsAt.IsZero() {
		return "CPU/MEM: loading"
	}
	if m.metricsWarning != "" && m.metricsPods == nil {
		return "CPU/MEM: " + m.metricsWarning
	}
	if m.metricsAt.IsZero() {
		return "CPU/MEM: waiting"
	}
	status := fmt.Sprintf("CPU/MEM: %d pods · %s", len(m.metricsPods), m.metricsAt.Format("15:04:05"))
	if m.metricsWarning != "" {
		status += " · " + m.metricsWarning
	}
	return status
}

// Aggregate only the pods belonging to a displayed workload. A missing
// replica must not be represented as zero CPU or memory usage.
func (m podMonitorState) appResources(app core.AppChoice) (kube.PodResourceUsage, bool) {
	if m.metricsPods == nil {
		return kube.PodResourceUsage{}, false
	}
	seen := map[string]bool{}
	total := kube.PodResourceUsage{CPUValid: true, MemoryValid: true, CPULimitKnown: true, MemoryLimitKnown: true,
		CPURequestKnown: true, MemoryRequestKnown: true}
	for _, row := range preparePodPickerRows([]core.AppChoice{app}) {
		pod := row.pod.Name
		if pod == "" || seen[pod] {
			continue
		}
		seen[pod] = true
		usage, ok := m.metricsPods[pod]
		if !ok {
			return kube.PodResourceUsage{}, false
		}
		total.CPUMilli += usage.CPUMilli
		total.MemoryBytes += usage.MemoryBytes
		total.CPULimitMilli += usage.CPULimitMilli
		total.MemoryLimitBytes += usage.MemoryLimitBytes
		total.CPURequestMilli += usage.CPURequestMilli
		total.MemoryRequestBytes += usage.MemoryRequestBytes
		total.CPUValid = total.CPUValid && usage.CPUValid
		total.MemoryValid = total.MemoryValid && usage.MemoryValid
		total.CPULimitKnown = total.CPULimitKnown && usage.CPULimitKnown
		total.MemoryLimitKnown = total.MemoryLimitKnown && usage.MemoryLimitKnown
		total.CPURequestKnown = total.CPURequestKnown && usage.CPURequestKnown
		total.MemoryRequestKnown = total.MemoryRequestKnown && usage.MemoryRequestKnown
		total.Containers += usage.Containers
	}
	return total, len(seen) > 0
}

func metricCPU(milli float64, valid bool) string {
	if !valid {
		return "—"
	}
	if milli > 0 && milli < 1 {
		return "<1m"
	}
	return fmt.Sprintf("%.0fm", milli)
}

func metricMemory(bytes float64, valid bool) string {
	if !valid {
		return "—"
	}
	mib := bytes / (1024 * 1024)
	if mib >= 1024 {
		return fmt.Sprintf("%.1fGi", mib/1024)
	}
	if mib > 0 && mib < 1 {
		return "<1Mi"
	}
	return fmt.Sprintf("%.0fMi", mib)
}

func metricPercent(usage, capacity float64, known bool) string {
	if !known || capacity <= 0 {
		return ""
	}
	return fmt.Sprintf(" (%.0f%%)", math.Round(100*usage/capacity))
}

func (m podMonitorState) resourceCells(app core.AppChoice) (string, string) {
	usage, found := m.appResources(app)
	if !found {
		return "—", "—"
	}
	return metricCPU(usage.CPUMilli, usage.CPUValid), metricMemory(usage.MemoryBytes, usage.MemoryValid)
}

func (m podMonitorState) selectedMetricsDetail(app core.AppChoice) string {
	usage, ok := m.appResources(app)
	if !ok {
		return "CPU/MEM: no current pod metrics"
	}
	cpu, mem := m.resourceCells(app)
	details := []string{"CPU " + cpu, "memory " + mem}
	if usage.CPUValid && usage.CPULimitKnown {
		details[0] += " / limit " + metricCPU(usage.CPULimitMilli, true) + metricPercent(usage.CPUMilli, usage.CPULimitMilli, true)
	}
	if usage.MemoryValid && usage.MemoryLimitKnown {
		details[1] += " / limit " + metricMemory(usage.MemoryLimitBytes, true) + metricPercent(usage.MemoryBytes, usage.MemoryLimitBytes, true)
	}
	if usage.CPUValid && usage.CPURequestKnown {
		details = append(details, "CPU request "+metricCPU(usage.CPURequestMilli, true))
	}
	if usage.MemoryValid && usage.MemoryRequestKnown {
		details = append(details, "memory request "+metricMemory(usage.MemoryRequestBytes, true))
	}
	return strings.Join(details, " · ")
}

func (m podMonitorState) resourceCellStyles(app core.AppChoice) (lipgloss.Style, lipgloss.Style) {
	usage, ok := m.appResources(app)
	if !ok {
		return lipgloss.NewStyle(), lipgloss.NewStyle()
	}
	style := func(used, limit float64, known bool) lipgloss.Style {
		if !known || limit <= 0 {
			return lipgloss.NewStyle()
		}
		if used/limit >= .95 {
			return alertStyle
		}
		if used/limit >= .8 {
			return warnStyle
		}
		return lipgloss.NewStyle()
	}
	return style(usage.CPUMilli, usage.CPULimitMilli, usage.CPUValid && usage.CPULimitKnown),
		style(usage.MemoryBytes, usage.MemoryLimitBytes, usage.MemoryValid && usage.MemoryLimitKnown)
}

func podMetricPreview(m podMonitorState, pod string) string {
	usage, found := m.metricsPods[pod]
	if !found {
		return "CPU —  MEM —"
	}
	return fmt.Sprintf("CPU %s  MEM %s", metricCPU(usage.CPUMilli, usage.CPUValid), metricMemory(usage.MemoryBytes, usage.MemoryValid))
}

func (m podMonitorState) metricsSummaryForApp(app core.AppChoice) string {
	if !m.monitorEnabled {
		return ""
	}
	return m.selectedMetricsDetail(app)
}
