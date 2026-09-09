package kube

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/fernandocpaz/tailg/internal/core"
)

func TestStreamReconnectUsesCursorAndPreservesMissedLogs(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake kubectl uses a POSIX shell")
	}
	dir := t.TempDir()
	argsPath, logsPath := filepath.Join(dir, "args"), filepath.Join(dir, "logs")
	t.Setenv("TAILG_TEST_ARGS", argsPath)
	t.Setenv("TAILG_TEST_LOGS", logsPath)
	binary := filepath.Join(dir, "kubectl")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$TAILG_TEST_ARGS\"\ncat \"$TAILG_TEST_LOGS\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	runner := Runner{Binary: binary}
	cursor := &core.LogCursor{}
	options := LogOptions{Since: "1h", Tail: 2, Follow: true, Cursor: cursor}
	item := core.InventoryItem{Pod: "pod-1", Container: "api"}
	line := "2026-09-09T20:00:00.123456789Z retry\n"
	run := func(logs string) ([]core.LogEvent, string) {
		t.Helper()
		if err := os.WriteFile(logsPath, []byte(logs), 0o600); err != nil {
			t.Fatal(err)
		}
		events := make(chan core.LogEvent, 1024)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := runner.Stream(ctx, item, options, events); err != nil {
			t.Fatal(err)
		}
		close(events)
		var rows []core.LogEvent
		for event := range events {
			if !event.Started && !event.Closed {
				if event.ReceivedAt.IsZero() {
					t.Fatal("received log has no receipt timestamp")
				}
				rows = append(rows, event)
			}
		}
		args, err := os.ReadFile(argsPath)
		if err != nil {
			t.Fatal(err)
		}
		return rows, string(args)
	}
	rows, args := run(line + line)
	if len(rows) != 2 || rows[0].Replayed || rows[1].Replayed || !strings.Contains(args, "--tail\n2\n") || !strings.Contains(args, "--since\n1h\n") {
		t.Fatalf("initial tail changed: rows=%+v args=%q", rows, args)
	}
	// The third identical occurrence is new. A large backlog must not be
	// capped to the original tail, nor mistaken for duplicate log messages.
	backlog := line + line + line
	for i := 1; i <= 600; i++ {
		at := time.Date(2026, 9, 9, 20, 0, 1, i, time.UTC)
		backlog += fmt.Sprintf("%s request %d\n", at.Format(time.RFC3339Nano), i)
	}
	rows, args = run(backlog)
	if len(rows) != 603 || !rows[0].Replayed || !rows[1].Replayed || rows[2].Replayed || rows[602].Replayed {
		t.Fatalf("replay handling lost or hid new logs: %d rows", len(rows))
	}
	if !strings.Contains(args, "--tail\n-1\n") || !strings.Contains(args, "--since-time\n2026-09-09T19:59:59Z\n") || strings.Contains(args, "--since\n") {
		t.Fatalf("incorrect reconnect arguments: %q", args)
	}
}
