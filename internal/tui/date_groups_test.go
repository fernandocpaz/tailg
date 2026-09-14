package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/fernandocpaz/tailg/internal/core"
)

func TestLogDateGroupLabelUsesLocalCalendarDays(t *testing.T) {
	location := time.FixedZone("UTC-4", -4*60*60)
	now := time.Date(2026, 9, 14, 0, 5, 0, 0, location)
	tests := []struct {
		name string
		at   time.Time
		want string
	}{
		{"today", time.Date(2026, 9, 14, 0, 1, 0, 0, location), "Today · 2026-09-14"},
		{"yesterday", time.Date(2026, 9, 13, 23, 59, 0, 0, location), "1 day ago · 2026-09-13"},
		{"two days", time.Date(2026, 9, 12, 12, 0, 0, 0, location), "2 days ago · 2026-09-12"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := logDateGroupLabel(test.at, now); got != test.want {
				t.Fatalf("label=%q, want %q", got, test.want)
			}
		})
	}
}

func TestRenderLogTimelineRowsGroupsDatesAndKeepsNewestVisible(t *testing.T) {
	now := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	records := []core.LogRecord{
		timelineTestRecord("oldest", now.AddDate(0, 0, -2)),
		timelineTestRecord("yesterday", now.AddDate(0, 0, -1)),
		timelineTestRecord("today earlier", now.Add(-time.Hour)),
		timelineTestRecord("newest", now),
	}

	all := strings.Join(renderLogTimelineRows(records, "", 3, 10, 100, false, false, true, now), "\n")
	for _, expected := range []string{
		"2 days ago · 2026-09-12",
		"1 day ago · 2026-09-13",
		"Today · 2026-09-14",
		"oldest",
		"yesterday",
		"newest",
	} {
		if !strings.Contains(all, expected) {
			t.Fatalf("timeline missing %q:\n%s", expected, all)
		}
	}

	tight := strings.Join(renderLogTimelineRows(records, "", 3, 2, 100, false, false, true, now), "\n")
	if !strings.Contains(tight, "Today · 2026-09-14") || !strings.Contains(tight, "newest") {
		t.Fatalf("tight live viewport lost its date or newest log:\n%s", tight)
	}
	if strings.Contains(tight, "today earlier") {
		t.Fatalf("tight live viewport should sacrifice the older log, not the newest:\n%s", tight)
	}
}

func TestRenderLogTimelineRowsKeepsSelectedLogVisible(t *testing.T) {
	now := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	records := []core.LogRecord{
		timelineTestRecord("selected oldest", now.AddDate(0, 0, -2)),
		timelineTestRecord("middle", now.AddDate(0, 0, -1)),
		timelineTestRecord("newest", now),
	}
	rendered := strings.Join(renderLogTimelineRows(records, "", 0, 2, 100, false, false, false, now), "\n")
	if !strings.Contains(rendered, "2 days ago · 2026-09-12") || !strings.Contains(rendered, "selected oldest") {
		t.Fatalf("paused viewport lost its date or selected log:\n%s", rendered)
	}
}

func timelineTestRecord(message string, observedAt time.Time) core.LogRecord {
	event := core.LogEvent{Message: message, ObservedAt: observedAt}
	return core.LogRecord{Event: event, Text: message}
}
