package tui

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/fernandocpaz/tailg/internal/core"
)

func monitorLog(at time.Time, message string) string {
	return at.Format(time.RFC3339Nano) + " " + message + "\n"
}

func TestErrorThenSilenceBecomesUnknownAndLaterActivityClearsIt(t *testing.T) {
	at := time.Date(2026, 10, 15, 14, 0, 0, 0, time.UTC)
	var activity podLogActivity
	errorSample := parsePodHealthSample("api-1", monitorLog(at, "ERR database unavailable"))
	if got := activity.observe(errorSample, podHealthSample{}, at, time.Time{}); got.kind != podHealthError {
		t.Fatal("initial error not red")
	}
	for _, elapsed := range []time.Duration{time.Minute, 3*time.Minute - time.Nanosecond, 3 * time.Minute, 9 * time.Minute} {
		got := activity.observe(podHealthSample{}, podHealthSample{}, at.Add(elapsed), time.Time{})
		want := podHealthError
		if elapsed >= 3*time.Minute {
			want = podHealthUnknown
		}
		if got.kind != want || got.reason == "" {
			t.Fatalf("after %v: %+v", elapsed, got)
		}
	}
	later := at.Add(10 * time.Minute)
	clean := parsePodHealthSample("api-1", monitorLog(later, "INF resumed")+monitorLog(later.Add(time.Second), "INF active")+monitorLog(later.Add(2*time.Second), "INF active"))
	if got := activity.observe(clean, podHealthSample{}, later.Add(3*time.Second), time.Time{}); got.kind != podHealthHealthy || got.reason != "" {
		t.Fatalf("resumed activity did not clear pending error: %+v", got)
	}
	if got := activity.observe(podHealthSample{}, clean, later.Add(time.Hour), time.Time{}); got.kind != podHealthIdle {
		t.Fatal("old error reappeared after recovery")
	}
}

func TestTimestampOrderAndExceptionFramesDoNotInventRecovery(t *testing.T) {
	at := time.Date(2026, 10, 15, 14, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name, logs string
		pending    bool
		last       time.Time
	}{
		{"newer error printed before older info", monitorLog(at.Add(time.Second), "FTL failed") + monitorLog(at, "INF old message"), true, at.Add(time.Second)},
		{"newer info printed before older error", monitorLog(at.Add(time.Second), "INF resumed") + monitorLog(at, "ERR old error"), false, at.Add(time.Second)},
		{"error with stack frames", monitorLog(at, "ERR failed") + monitorLog(at.Add(time.Second), "   at Database.Save()"), true, at.Add(time.Second)},
		{"same timestamp is ambiguous", monitorLog(at, "ERR failed") + monitorLog(at, "INF another container"), true, at},
	} {
		t.Run(test.name, func(t *testing.T) {
			sample := parsePodHealthSample("api-1", test.logs)
			var activity podLogActivity
			activity.observe(sample, podHealthSample{}, at.Add(2*time.Second), time.Time{})
			got := activity.observe(podHealthSample{}, sample, test.last.Add(3*time.Minute), time.Time{})
			if (got.kind == podHealthUnknown) != test.pending {
				t.Fatalf("wrong final record chosen: sample=%+v display=%+v", sample, got)
			}
		})
	}
}

func TestTimestampedMonitorCountsPlainFatalAndJSONLevels(t *testing.T) {
	at := time.Now()
	sample := parsePodHealthSample("api", monitorLog(at, "ERR failed")+monitorLog(at, "FTL failed")+monitorLog(at, "ERROR: failed")+monitorLog(at, `{"@l":"Fatal","@m":"failed"}`)+monitorLog(at, "WRN retry"))
	if sample.lines != 5 || sample.errors != 4 || sample.warnings != 1 {
		t.Fatalf("timestamp prefix broke level parsing: %+v", sample)
	}
}

func TestReadFailureDoesNotClearPendingErrorAndReplacementResetsIt(t *testing.T) {
	at := time.Now().Add(-time.Hour)
	started := at.Add(-time.Hour)
	var activity podLogActivity
	activity.observe(parsePodHealthSample("api", monitorLog(at, "ERR failed")), podHealthSample{}, at, started)
	got := activity.observe(podHealthSample{err: errors.New("Forbidden")}, podHealthSample{}, at.Add(4*time.Minute), started)
	if got.kind != podHealthUnknown || !strings.Contains(got.reason, "read failed") {
		t.Fatal("failed read forgot error history")
	}
	got = activity.observe(podHealthSample{}, podHealthSample{}, at.Add(5*time.Minute), at.Add(4*time.Minute))
	if got.kind != podHealthIdle || activity.lastRecordError {
		t.Fatal("replacement inherited old error")
	}
	var ordinary podLogActivity
	if got := ordinary.observe(podHealthSample{}, podHealthSample{}, at, started); got.kind != podHealthIdle {
		t.Fatal("ordinary silence became unknown")
	}
}

func TestUnknownSiblingStaysVisibleInApplicationAndPodModal(t *testing.T) {
	at := time.Date(2026, 10, 15, 14, 0, 0, 0, time.UTC)
	m := newPickerModel([]core.AppChoice{{Name: "api", Pods: []string{"api-1", "api-2"}}}, 1, "qa", "apollo", nil)
	m.width, m.height, m.monitorEnabled, m.monitorGeneration = 80, 24, true, 1
	scan := func(when time.Time, first, second podHealthSample) {
		updated, _ := m.Update(podHealthScanMsg{generation: 1, scannedAt: when, samples: map[string]podHealthSample{"api-1": first, "api-2": second}})
		m = updated.(pickerModel)
	}
	scan(at, parsePodHealthSample("api-1", monitorLog(at, "ERR failed")), podHealthSample{})
	clean := parsePodHealthSample("api-2", strings.Repeat(monitorLog(at.Add(3*time.Minute), "INF active"), 3))
	scan(at.Add(3*time.Minute), podHealthSample{}, clean)
	if m.monitorApps["api"].kind != podHealthUnknown || m.monitorHealth["api-2"].kind != podHealthHealthy {
		t.Fatal("busy replica masked silent sibling")
	}
	view := ansi.Strip(m.View())
	for _, text := range []string{"? api", "Unknown / possibly unhealthy", "api-1", "1 unknown / possibly unhealthy"} {
		if !strings.Contains(view, text) {
			t.Fatalf("missing %q in:\n%s", text, view)
		}
	}
	m = pickerTestKey(m, "p")
	if m.selection == nil || !strings.Contains(ansi.Strip(m.View()), "Unknown / possibly unhealthy") {
		t.Fatal("pod modal hid unknown state")
	}
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 40, Height: 12})
	m = updated.(pickerModel)
	assertPickerFits(t, m)
	if !strings.Contains(ansi.Strip(m.View()), "Esc") {
		t.Fatal("unknown state displaced close control")
	}
}

func TestMonitorHistoryPrunesRemovedPodsAndResetsWhenRestarted(t *testing.T) {
	at := time.Now()
	state := podMonitorState{monitorEnabled: true, monitorGeneration: 1}
	rows := []podPickerRow{{pod: core.PodChoice{Name: "api"}}}
	state.update(podHealthScanMsg{generation: 1, scannedAt: at, samples: map[string]podHealthSample{"api": parsePodHealthSample("api", monitorLog(at, "ERR failed"))}}, rows, "qa", "apollo")
	if !state.monitorActivity["api"].lastRecordError {
		t.Fatal("error not tracked")
	}
	state.update(podHealthScanMsg{generation: 1, scannedAt: at.Add(time.Minute), samples: map[string]podHealthSample{}}, nil, "qa", "apollo")
	if len(state.monitorActivity) != 0 {
		t.Fatal("removed pod's history retained")
	}
	state.monitorActivity["api"] = podLogActivity{lastRecordError: true}
	state.stop()
	state.toggle(rows, "qa", "apollo")
	defer state.stop()
	if len(state.monitorActivity) != 0 {
		t.Fatal("monitor restart retained old history")
	}
	state.stop()
	state.update(podHealthScanMsg{generation: state.monitorGeneration, scannedAt: at, samples: map[string]podHealthSample{"api": parsePodHealthSample("api", monitorLog(at, "ERR ignored"))}}, rows, "qa", "apollo")
	if len(state.monitorActivity) != 0 {
		t.Fatal("disabled monitoring recorded error history")
	}
}

func TestUnknownNameMarkerPreservesTerminalCellWidth(t *testing.T) {
	for _, width := range []int{1, 2, 12, 36} {
		name := pickerCell("api-界界", width)
		got := renderMonitoredPodName(name, podHealthDisplay{kind: podHealthUnknown}, false)
		if lipgloss.Width(got) != width || !strings.HasPrefix(ansi.Strip(got), "?") {
			t.Fatal(fmt.Sprintf("unknown marker changed width %d: %q", width, got))
		}
	}
}
