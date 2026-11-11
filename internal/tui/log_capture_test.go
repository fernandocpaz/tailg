package tui

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/fernandocpaz/tailg/internal/agent"
	"github.com/fernandocpaz/tailg/internal/core"
)

func TestLogCaptureKeepsFormattedBatchesBeyondBuffer(t *testing.T) {
	var output strings.Builder
	excludes, err := core.CompilePatterns([]string{"hidden log"})
	if err != nil {
		t.Fatal(err)
	}
	m := model{
		config: Config{
			Formatter:   core.Formatter{Exclude: excludes},
			CaptureLogs: agent.NewLogCapture(&output, "test", "default").WriteBatch,
		},
		state:       core.NewFilterState(1),
		heartbeat:   &core.HeartbeatAnalyzer{},
		followsLive: true,
	}
	long := strings.Repeat("long message ", 400) + "end"
	at := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
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
		{ObservedAt: at, Message: long},
		{ObservedAt: at.AddDate(0, 0, 1), Message: "next day"},
	}); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(output.String(), "\n"), "\n")
	expected := []string{"[ERR] request failed\nstack trace", long, long, "next day"}
	if len(lines) != len(expected) {
		t.Fatalf("capture has %d lines, want %d", len(lines), len(expected))
	}
	for index, line := range lines {
		var record struct {
			Message string
		}
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("capture line %d: %v", index, err)
		}
		if record.Message != expected[index] {
			t.Errorf("unexpected message at line %d", index)
		}
	}
}
