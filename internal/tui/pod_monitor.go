package tui

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/fernandocpaz/tailg/internal/core"
	"github.com/fernandocpaz/tailg/internal/kube"
)

const (
	podMonitorInterval         = time.Minute
	podMonitorFlashInterval    = 550 * time.Millisecond
	podMonitorActivityMinimum  = 3
	podMonitorFlashErrors      = 5
	podMonitorTimeout          = 20 * time.Second
	podMonitorWorkers          = 4
	podMonitorSilentAfterError = 3 * time.Minute
)

type podHealthKind int

const (
	podHealthIdle podHealthKind = iota
	podHealthHealthy
	podHealthWarning
	podHealthError
	podHealthCritical
	podHealthUnknown
)

type podHealthSample struct {
	lines           int
	warnings        int
	errors          int
	err             error
	lastLogAt       time.Time
	lastRecordAt    time.Time
	hasRecord       bool
	lastRecordError bool
}

type podHealthDisplay struct {
	sample             podHealthSample
	kind               podHealthKind
	warningsIncreasing bool
	reason             string
}

type podHealthScanMsg struct {
	generation int
	samples    map[string]podHealthSample
	scannedAt  time.Time
}

type podMonitorPollMsg struct{ generation int }
type podMonitorFlashMsg struct{ generation int }

func podMonitorPollCmd(generation int) tea.Cmd {
	return tea.Tick(podMonitorInterval, func(time.Time) tea.Msg {
		return podMonitorPollMsg{generation: generation}
	})
}

func podMonitorFlashCmd(generation int) tea.Cmd {
	return tea.Tick(podMonitorFlashInterval, func(time.Time) tea.Msg {
		return podMonitorFlashMsg{generation: generation}
	})
}

func scanPodHealthCmd(parent context.Context, rows []podPickerRow, kubeContext, namespace string, generation int) tea.Cmd {
	pods := make([]string, 0, len(rows))
	seen := make(map[string]bool, len(rows))
	for _, row := range rows {
		if row.pod.Name != "" && !seen[row.pod.Name] {
			seen[row.pod.Name] = true
			pods = append(pods, row.pod.Name)
		}
	}

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(parent, podMonitorTimeout)
		defer cancel()

		runner := kube.NewRunner(namespace, kubeContext)
		samples := make(map[string]podHealthSample, len(pods))
		var mu sync.Mutex
		jobs := make(chan string)

		workers := min(podMonitorWorkers, len(pods))
		if workers == 0 {
			return podHealthScanMsg{generation: generation, samples: samples, scannedAt: time.Now()}
		}

		var wg sync.WaitGroup
		wg.Add(workers)
		for i := 0; i < workers; i++ {
			go func() {
				defer wg.Done()
				for pod := range jobs {
					sample := scanOnePod(ctx, runner, pod)
					mu.Lock()
					samples[pod] = sample
					mu.Unlock()
				}
			}()
		}

	feed:
		for _, pod := range pods {
			select {
			case jobs <- pod:
			case <-ctx.Done():
				break feed
			}
		}
		close(jobs)
		wg.Wait()

		if ctx.Err() != nil {
			mu.Lock()
			for _, pod := range pods {
				if _, ok := samples[pod]; !ok {
					samples[pod] = podHealthSample{err: ctx.Err()}
				}
			}
			mu.Unlock()
		}

		return podHealthScanMsg{generation: generation, samples: samples, scannedAt: time.Now()}
	}
}

func scanOnePod(ctx context.Context, runner kube.Runner, pod string) podHealthSample {
	output, err := runner.Run(ctx,
		"logs", "pod/"+pod,
		"--all-containers=true",
		"--since=1m",
		"--tail=-1",
		"--timestamps=true",
	)
	if err != nil {
		return podHealthSample{err: err}
	}

	return parsePodHealthSample(pod, output)
}

func parsePodHealthSample(pod, output string) podHealthSample {
	var sample podHealthSample
	for _, line := range strings.Split(output, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		message, at := kube.SplitTimestamp(line)
		sample.lines++
		if at.After(sample.lastLogAt) {
			sample.lastLogAt = at
		}
		fields := core.ParseLogFields(core.LogEvent{Pod: pod, Message: message})
		// Exception continuations belong to the preceding error; they are not
		// evidence of a new, non-error log entry. Timestamp order also handles
		// kubectl concatenating container streams in a different order.
		if !core.IsLogContinuation(message) && (!sample.hasRecord || at.IsZero() || at.After(sample.lastRecordAt) || (at.Equal(sample.lastRecordAt) && fields.Level == "ERR")) {
			sample.hasRecord, sample.lastRecordAt, sample.lastRecordError = true, at, fields.Level == "ERR"
		}
		switch fields.Level {
		case "ERR":
			sample.errors++
		case "WRN":
			sample.warnings++
		}
	}
	return sample
}

func classifyPodHealth(current podHealthSample, previous podHealthSample) podHealthDisplay {
	display := podHealthDisplay{sample: current}
	if current.err != nil {
		display.kind = podHealthIdle
		return display
	}
	if current.errors >= podMonitorFlashErrors {
		display.kind = podHealthCritical
		return display
	}
	if current.errors > 0 {
		display.kind = podHealthError
		return display
	}
	if current.warnings > 0 {
		display.kind = podHealthWarning
		display.warningsIncreasing = current.warnings > previous.warnings
		return display
	}
	if current.lines < podMonitorActivityMinimum {
		display.kind = podHealthIdle
		return display
	}
	display.kind = podHealthHealthy
	return display
}

func renderMonitoredPodName(name string, health podHealthDisplay, flashOn bool) string {
	switch health.kind {
	case podHealthUnknown:
		width := lipgloss.Width(name)
		if width <= 0 {
			return name
		}
		marked := "?"
		if width > 1 {
			marked = "? " + truncatePickerCell(strings.TrimRight(name, " "), width-2)
		}
		return warnStyle.Underline(true).Render(pickerCell(marked, width))
	case podHealthCritical:
		if !flashOn {
			return dimStyle.Render(name)
		}
		return alertStyle.Render(name)
	case podHealthError:
		return alertStyle.Render(name)
	case podHealthWarning:
		return warnStyle.Render(name)
	case podHealthHealthy:
		return okStyle.Render(name)
	default:
		return dimStyle.Render(name)
	}
}

func podMonitorSummary(enabled, busy bool, last time.Time, monitorErr string) string {
	if !enabled {
		return "Monitor: OFF"
	}
	status := "Monitor: ON · every 1m"
	if busy {
		status += " · scanning"
	} else if !last.IsZero() {
		status += " · last " + last.Format("15:04:05")
	}
	if monitorErr != "" {
		status += " · " + monitorErr
	}
	return status
}

func podMonitorLegend() string {
	return fmt.Sprintf("green healthy · gray quiet · orange warnings · red errors · flashing red >= %d errors/min · ? underlined amber unknown / possibly unhealthy after 3m silence following an error", podMonitorFlashErrors)
}
