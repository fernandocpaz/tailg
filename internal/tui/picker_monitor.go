package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/fernandocpaz/tailg/internal/core"
	"github.com/fernandocpaz/tailg/internal/kube"
)

// Both pickers share polling, cancellation, and stale-message handling.
type podMonitorState struct {
	monitorEnabled    bool
	monitorBusy       bool
	monitorGeneration int
	monitorHealth     map[string]podHealthDisplay
	monitorPrevious   map[string]podHealthSample
	monitorActivity   map[string]podLogActivity
	monitorLastScan   time.Time
	monitorFlashOn    bool
	monitorError      string
	monitorContext    context.Context
	monitorCancel     context.CancelFunc
	metricsPods       map[string]kube.PodResourceUsage
	metricsWarning    string
	metricsAt         time.Time
	metricsBusy       bool
}

func (m *podMonitorState) stop() {
	if m.monitorCancel != nil {
		m.monitorCancel()
		m.monitorCancel = nil
	}
	m.monitorGeneration++
	m.monitorEnabled = false
	m.monitorBusy = false
	m.monitorFlashOn = false
	m.metricsPods = nil
	m.metricsWarning = ""
	m.metricsAt = time.Time{}
	m.metricsBusy = false
}

func (m *podMonitorState) toggle(rows []podPickerRow, kubeContext, namespace string) tea.Cmd {
	if m.monitorEnabled {
		m.stop()
		return nil
	}
	m.monitorGeneration++
	m.monitorEnabled = true
	m.monitorBusy = true
	m.monitorFlashOn = true
	m.monitorError = ""
	m.monitorLastScan = time.Time{}
	m.metricsPods = nil
	m.metricsWarning = ""
	m.metricsAt = time.Time{}
	m.metricsBusy = true
	m.monitorHealth = nil
	m.monitorPrevious = nil
	m.monitorActivity = nil
	m.monitorContext, m.monitorCancel = context.WithCancel(context.Background())
	return tea.Batch(
		scanPodHealthCmd(m.monitorContext, rows, kubeContext, namespace, m.monitorGeneration),
		podMonitorPollCmd(m.monitorGeneration),
		podMonitorFlashCmd(m.monitorGeneration),
		namespaceMetricsReadCmd(m.monitorContext, namespace, kubeContext, m.monitorGeneration),
		namespaceMetricsPollCmd(m.monitorGeneration),
	)
}

func (m *podMonitorState) update(message tea.Msg, rows []podPickerRow, kubeContext, namespace string) (bool, tea.Cmd) {
	if handled, cmd := m.updateMetrics(message, kubeContext, namespace); handled {
		return true, cmd
	}
	switch msg := message.(type) {
	case podHealthScanMsg:
		if !m.monitorEnabled || msg.generation != m.monitorGeneration {
			return true, nil
		}
		m.monitorHealth = make(map[string]podHealthDisplay, len(msg.samples))
		if m.monitorActivity == nil {
			m.monitorActivity = make(map[string]podLogActivity)
		}
		now := msg.scannedAt
		if now.IsZero() {
			now = time.Now()
		}
		live := make(map[string]time.Time, len(rows))
		for _, row := range rows {
			live[row.pod.Name] = row.pod.StartedAt
		}
		for pod := range m.monitorActivity {
			if _, ok := live[pod]; !ok {
				delete(m.monitorActivity, pod)
			}
		}
		failures := 0
		unknown := 0
		for pod, sample := range msg.samples {
			startedAt, ok := live[pod]
			if !ok {
				continue
			}
			activity := m.monitorActivity[pod]
			m.monitorHealth[pod] = activity.observe(sample, m.monitorPrevious[pod], now, startedAt)
			m.monitorActivity[pod] = activity
			if m.monitorHealth[pod].kind == podHealthUnknown {
				unknown++
			}
			if sample.err != nil {
				failures++
			}
		}
		m.monitorPrevious = msg.samples
		m.monitorBusy = false
		m.monitorLastScan = msg.scannedAt
		var notices []string
		if failures > 0 {
			notices = append(notices, fmt.Sprintf("%d pod scans failed", failures))
		}
		if unknown > 0 {
			notices = append(notices, fmt.Sprintf("%d unknown / possibly unhealthy", unknown))
		}
		m.monitorError = strings.Join(notices, " · ")
		return true, nil
	case podMonitorPollMsg:
		if !m.monitorEnabled || msg.generation != m.monitorGeneration {
			return true, nil
		}
		commands := []tea.Cmd{podMonitorPollCmd(m.monitorGeneration)}
		if !m.monitorBusy {
			m.monitorBusy = true
			commands = append(commands, scanPodHealthCmd(m.monitorContext, rows, kubeContext, namespace, m.monitorGeneration))
		}
		return true, tea.Batch(commands...)
	case podMonitorFlashMsg:
		if !m.monitorEnabled || msg.generation != m.monitorGeneration {
			return true, nil
		}
		m.monitorFlashOn = !m.monitorFlashOn
		return true, podMonitorFlashCmd(m.monitorGeneration)
	}
	return false, nil
}

// Errors and warnings from any replica take precedence over quiet pods or
// collection failures. An incomplete clean scan must not imply healthy.
func classifyAppHealth(app core.AppChoice, samples, previous map[string]podHealthSample) podHealthDisplay {
	var current, prior podHealthSample
	seen := make(map[string]bool)
	incomplete := false
	for _, row := range preparePodPickerRows([]core.AppChoice{app}) {
		pod := row.pod.Name
		if pod == "" || seen[pod] {
			continue
		}
		seen[pod] = true
		sample, ok := samples[pod]
		if !ok || sample.err != nil {
			incomplete = true
		}
		current.lines += sample.lines
		current.warnings += sample.warnings
		current.errors += sample.errors
		prior.warnings += previous[pod].warnings
	}
	display := classifyPodHealth(current, prior)
	if incomplete && display.kind == podHealthHealthy {
		display.kind = podHealthIdle
	}
	return display
}
