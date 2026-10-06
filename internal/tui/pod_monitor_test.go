package tui

import (
	"errors"
	"testing"
	"time"
)

func TestClassifyPodHealth(t *testing.T) {
	tests := []struct {
		name       string
		current    podHealthSample
		previous   podHealthSample
		want       podHealthKind
		increasing bool
	}{
		{name: "scan failure is idle", current: podHealthSample{err: errors.New("boom")}, want: podHealthIdle},
		{name: "critical errors flash", current: podHealthSample{lines: 20, errors: podMonitorFlashErrors}, want: podHealthCritical},
		{name: "single error is red", current: podHealthSample{lines: 20, errors: 1}, want: podHealthError},
		{name: "warnings are orange", current: podHealthSample{lines: 20, warnings: 2}, want: podHealthWarning, increasing: true},
		{name: "stable warnings remain orange", current: podHealthSample{lines: 20, warnings: 2}, previous: podHealthSample{warnings: 2}, want: podHealthWarning},
		{name: "quiet pod is gray", current: podHealthSample{lines: podMonitorActivityMinimum - 1}, want: podHealthIdle},
		{name: "active clean pod is green", current: podHealthSample{lines: podMonitorActivityMinimum}, want: podHealthHealthy},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyPodHealth(tt.current, tt.previous)
			if got.kind != tt.want {
				t.Fatalf("kind = %v, want %v", got.kind, tt.want)
			}
			if got.warningsIncreasing != tt.increasing {
				t.Fatalf("warningsIncreasing = %v, want %v", got.warningsIncreasing, tt.increasing)
			}
		})
	}
}

func TestPodMonitorSummaryOff(t *testing.T) {
	if got := podMonitorSummary(false, false, time.Time{}, ""); got != "Monitor: OFF" {
		t.Fatalf("summary = %q", got)
	}
}
