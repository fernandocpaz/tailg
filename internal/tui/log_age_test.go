package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/fernandocpaz/tailg/internal/core"
	"github.com/muesli/termenv"
)

func TestOlderLogsUseLocalCalendarDates(t *testing.T) {
	location := time.FixedZone("UTC-4", -4*60*60)
	now := time.Date(2026, 10, 15, 0, 5, 0, 0, location)
	for _, test := range []struct {
		name string
		at   time.Time
		old  bool
	}{
		{"yesterday ten minutes ago", now.Add(-10 * time.Minute).UTC(), true},
		{"today in UTC", now.Add(-time.Minute).UTC(), false},
		{"older", now.AddDate(0, 0, -10), true},
		{"future", now.AddDate(0, 0, 1), false},
		{"unknown", time.Time{}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := logFromEarlierDay(test.at, now); got != test.old {
				t.Fatalf("historical = %t, want %t", got, test.old)
			}
		})
	}
}

func TestHistoricalLogFadesPreserveHighlightsSelectionAndText(t *testing.T) {
	profile := lipgloss.ColorProfile()
	t.Cleanup(func() { lipgloss.SetColorProfile(profile) })
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	for _, p := range []termenv.Profile{termenv.TrueColor, termenv.ANSI256, termenv.ANSI} {
		lipgloss.SetColorProfile(p)
		record := timelineTestRecord("[api-7d9] [12:00:00 ERR] failed needle", now.AddDate(0, 0, -1))
		record.Event.Pod = "api-7d9"
		old := renderRecordRowAt(record, "needle", false, 100, true, true, now)
		if !strings.Contains(old, errorLineStyle.Faint(true).Render("failed ")) || !strings.Contains(old, matchStyle.Render("needle")) {
			t.Fatalf("historical severity or bright search match lost: %q", old)
		}
		fresh := renderRecordRowAt(record, "needle", false, 100, true, true, now.AddDate(0, 0, -1))
		if old == fresh || core.StripANSI(old) != core.StripANSI(fresh) {
			t.Fatal("age must change styling without changing log text")
		}
		selected := renderRecordRowAt(record, "needle", true, 100, true, true, now)
		selectedFresh := renderRecordRowAt(record, "needle", true, 100, true, true, now.AddDate(0, 0, -1))
		if selected != selectedFresh {
			t.Fatal("selected historical row must retain normal selection intensity")
		}
		plain := renderRecordRowAt(record, "needle", false, 100, true, false, now)
		if plain != core.StripANSI(plain) || plain != core.StripANSI(old) {
			t.Fatal("--no-color must preserve plain text without dimming codes")
		}
	}
}

func TestTimelineAndRetainedErrorsRefreshFadingAtMidnight(t *testing.T) {
	profile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(profile) })
	now := time.Date(2026, 10, 15, 23, 59, 0, 0, time.UTC)
	record := timelineTestRecord("[23:59:00 ERR] midnight failure", now)
	today := strings.Join(renderLogTimelineRows([]core.LogRecord{record}, "", -1, 5, 100, false, true, true, now), "\n")
	tomorrow := strings.Join(renderLogTimelineRows([]core.LogRecord{record}, "", -1, 5, 100, false, true, true, now.Add(2*time.Minute)), "\n")
	if strings.Contains(today, errorLineStyle.Faint(true).Render("midnight failure")) || !strings.Contains(tomorrow, errorLineStyle.Faint(true).Render("midnight failure")) {
		t.Fatal("timeline did not refresh age at midnight")
	}
	m := workflowModel()
	m.config.Formatter.Color = true
	m.errorLogs = errorLogsState{layout: &errorLogsLayout{}, blocks: [][]core.LogEvent{{record.Event, {Message: "at StackFrame()"}}}}
	rows := m.errorLogRows(now)
	for _, row := range rows {
		if strings.Contains(row.text, "StackFrame") && row.text != core.StripANSI(row.text) {
			t.Fatal("today's stack frame was dimmed")
		}
	}
	rows = m.errorLogRows(now.Add(2 * time.Minute))
	found := false
	for _, row := range rows {
		if strings.Contains(row.text, "StackFrame") {
			found = true
			if !strings.Contains(row.text, lipgloss.NewStyle().Faint(true).Render("at StackFrame()")) {
				t.Fatal("cached stack frame did not inherit its error's older date")
			}
		}
	}
	if !found {
		t.Fatal("stack frame missing")
	}
}
