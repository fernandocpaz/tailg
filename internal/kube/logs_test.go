package kube

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/fernandocpaz/tailg/internal/core"
)

func TestSendLogEventStopsAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if sendLogEvent(ctx, make(chan core.LogEvent), core.LogEvent{Message: "blocked"}) {
		t.Fatal("event was sent after cancellation")
	}
}

func TestSendLogEventDelivers(t *testing.T) {
	events := make(chan core.LogEvent, 1)
	want := core.LogEvent{Pod: "pod-1", Container: "app", Message: "ready"}
	if !sendLogEvent(context.Background(), events, want) {
		t.Fatal("event was not delivered")
	}
	if got := <-events; got.Pod != want.Pod || got.Container != want.Container || got.Message != want.Message {
		t.Fatalf("event = %#v, want %#v", got, want)
	}
}

func TestVisibleBackfillExpandsPastFilteredProbeTraffic(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake kubectl uses a POSIX shell")
	}
	directory := t.TempDir()
	logsPath := filepath.Join(directory, "logs")
	t.Setenv("TAILG_TEST_LOGS", logsPath)
	binary := filepath.Join(directory, "kubectl")
	script := `#!/bin/sh
tail_count=0
while [ "$#" -gt 0 ]; do
  if [ "$1" = "--tail" ]; then
    shift
    tail_count="$1"
  fi
  shift
done
tail -n "$tail_count" "$TAILG_TEST_LOGS"
`
	if err := os.WriteFile(binary, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}

	lines := []string{
		"2026-09-16T11:20:00Z actual-1",
		"2026-09-16T11:21:00Z actual-2",
		"2026-09-16T11:22:00Z actual-3",
	}
	for index := 0; index < 8; index++ {
		lines = append(lines, `2026-09-16T11:24:33Z [11:24:33 INF] HTTP GET /ready responded 200 {"SourceContext":"Microsoft.Extensions.Diagnostics.HealthChecks.DefaultHealthCheckService"}`)
	}
	if err := os.WriteFile(logsPath, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	excludes, err := core.CompilePatterns(core.DefaultExcludePatterns)
	if err != nil {
		t.Fatal(err)
	}
	formatter := core.Formatter{Exclude: excludes}
	visible := func(message string) bool {
		return len(formatter.Format("api-pod", "api", message, true)) > 0
	}
	runner := Runner{Binary: binary}
	events, err := runner.visibleBackfill(context.Background(), core.InventoryItem{Pod: "api-pod", Container: "api"}, LogOptions{
		Tail: 2, Visible: visible, InitialScanLimit: 16,
	})
	if err != nil {
		t.Fatal(err)
	}

	var actual []string
	for _, event := range events {
		if visible(event.Message) {
			actual = append(actual, event.Message)
		}
	}
	if len(actual) != 2 || actual[0] != "actual-2" || actual[1] != "actual-3" {
		t.Fatalf("visible backfill = %v, want [actual-2 actual-3]", actual)
	}
}
