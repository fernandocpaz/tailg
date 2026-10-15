package tui

import (
	"fmt"
	"time"

	"github.com/fernandocpaz/tailg/internal/core"
)

type podLogActivity struct {
	startedAt       time.Time
	lastLogAt       time.Time
	lastRecordAt    time.Time
	lastRecordError bool
}

func (a *podLogActivity) observe(sample podHealthSample, previous podHealthSample, at, startedAt time.Time) podHealthDisplay {
	if !startedAt.IsZero() && !a.startedAt.IsZero() && !startedAt.Equal(a.startedAt) {
		*a = podLogActivity{}
	}
	a.startedAt = startedAt
	display := classifyPodHealth(sample, previous)
	if sample.err == nil && sample.lines > 0 {
		lastLog := sample.lastLogAt
		if lastLog.IsZero() || lastLog.After(at) {
			lastLog = at
		}
		if lastLog.After(a.lastLogAt) {
			a.lastLogAt = lastLog
		}
		if sample.hasRecord {
			lastRecord := sample.lastRecordAt
			if lastRecord.IsZero() || lastRecord.After(at) {
				lastRecord = at
			}
			if lastRecord.After(a.lastRecordAt) || (lastRecord.Equal(a.lastRecordAt) && sample.lastRecordError) {
				a.lastRecordAt, a.lastRecordError = lastRecord, sample.lastRecordError
			}
		}
	}
	if a.lastRecordError && sample.errors == 0 {
		elapsed := max(time.Duration(0), at.Sub(a.lastLogAt))
		if elapsed >= podMonitorSilentAfterError {
			display.kind = podHealthUnknown
			display.reason = fmt.Sprintf("Unknown / possibly unhealthy: no later log entry observed after an error; silent for %s", elapsed.Round(time.Second))
			if sample.err != nil {
				display.reason += "; latest log read failed"
			}
		} else {
			display.kind = podHealthError
			display.reason = "Last observed log entry was an error; waiting for later activity"
		}
	}
	return display
}

// A healthy replica must not mask a sibling whose last entry was an error.
func appMonitorHistory(app core.AppChoice, display podHealthDisplay, health map[string]podHealthDisplay) podHealthDisplay {
	if display.kind == podHealthError || display.kind == podHealthCritical {
		return display
	}
	for _, row := range preparePodPickerRows([]core.AppChoice{app}) {
		if state := health[row.pod.Name]; state.kind == podHealthUnknown {
			display.kind, display.reason = podHealthUnknown, state.reason+" · Pod: "+row.pod.Name
			return display
		}
	}
	for _, row := range preparePodPickerRows([]core.AppChoice{app}) {
		if state := health[row.pod.Name]; state.kind == podHealthError && state.reason != "" {
			display.kind, display.reason = podHealthError, state.reason+" · Pod: "+row.pod.Name
			return display
		}
	}
	return display
}
