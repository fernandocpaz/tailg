package tui

import (
	"context"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/fernandocpaz/tailg/internal/core"
)

// Both pickers share polling, cancellation, and stale-message handling.
type podMonitorState struct {
	monitorEnabled    bool
	monitorBusy       bool
	monitorGeneration int
	monitorHealth     map[string]podHealthDisplay
	monitorPrevious   map[string]podHealthSample
	monitorLastScan   time.Time
	monitorFlashOn    bool
	monitorError      string
	monitorContext    context.Context
	monitorCancel     context.CancelFunc
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
	m.monitorHealth = nil
	m.monitorPrevious = nil
	m.monitorContext, m.monitorCancel = context.WithCancel(context.Background())
	return tea.Batch(
		scanPodHealthCmd(m.monitorContext, rows, kubeContext, namespace, m.monitorGeneration),
		podMonitorPollCmd(m.monitorGeneration),
		podMonitorFlashCmd(m.monitorGeneration),
	)
}

func (m *podMonitorState) update(message tea.Msg, rows []podPickerRow, kubeContext, namespace string) (bool, tea.Cmd) {
	switch msg := message.(type) {
	case podHealthScanMsg:
		if !m.monitorEnabled || msg.generation != m.monitorGeneration {
			return true, nil
		}
		m.monitorHealth = make(map[string]podHealthDisplay, len(msg.samples))
		failures := 0
		for pod, sample := range msg.samples {
			m.monitorHealth[pod] = classifyPodHealth(sample, m.monitorPrevious[pod])
			if sample.err != nil {
				failures++
			}
		}
		m.monitorPrevious = msg.samples
		m.monitorBusy = false
		m.monitorLastScan = msg.scannedAt
		m.monitorError = ""
		if failures > 0 {
			m.monitorError = fmt.Sprintf("%d pod scans failed", failures)
		}
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
