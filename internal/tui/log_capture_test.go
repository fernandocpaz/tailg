package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/fernandocpaz/tailg/internal/core"
)

func TestLogCaptureKeepsFormattedBatchesBeyondBuffer(t *testing.T) {
	var output strings.Builder
	excludes, err := core.CompilePatterns([]string{"hidden log"})
	if err != nil {
		t.Fatal(err)
	}
	m := model{
		capture:     logCapture{output: &output},
		config:      Config{Formatter: core.Formatter{Exclude: excludes}},
		state:       core.NewFilterState(1),
		heartbeat:   &core.HeartbeatAnalyzer{},
		followsLive: true,
	}
	long := strings.Repeat("long message ", 400) + "end"
	at := time.Date(2026, 10, 14, 12, 0, 0, 0, time.Local)
	if err := m.processLogBatch([]core.LogEvent{
		{ObservedAt: at, Message: `{"level":"Error","message":"request failed","exception":"stack trace"}`},
		{ObservedAt: at, Message: "hidden log"},
		{ObservedAt: at, Message: "replayed log", Replayed: true},
		{ObservedAt: at, Message: "Heartbeat: uptime=00:01:00 inflight=0"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := m.processLogBatch([]core.LogEvent{
		{ObservedAt: at, Message: long},
		{ObservedAt: at.AddDate(0, 0, 1), Message: "next day"},
	}); err != nil {
		t.Fatal(err)
	}
	got := output.String()
	if strings.Count(got, "2026-10-14") != 1 || strings.Count(got, "2026-10-15") != 1 ||
		!strings.Contains(got, "[ERR] request failed\nstack trace\n"+long+"\n") ||
		!strings.HasSuffix(got, "2026-10-15\nnext day\n") {
		t.Fatalf("unexpected capture: %q", got)
	}
	for _, unwanted := range []string{"hidden log", "replayed log", "Heartbeat:"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("unexpected captured text %q", unwanted)
		}
	}
}
