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

func logFixtureRunner(t *testing.T, logs string) Runner {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake kubectl uses a POSIX shell")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "logs")
	if err := os.WriteFile(path, []byte(logs), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TAILG_DETAIL_LOGS", path)
	binary := filepath.Join(dir, "kubectl")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\ncat \"$TAILG_DETAIL_LOGS\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return Runner{Binary: binary}
}

func TestLogReadersPreserveLinesLargerThanFourMB(t *testing.T) {
	message := "[10:00:00 ERR] " + strings.Repeat("x", 5*1024*1024) + " END_OF_LARGE_ERROR"
	runner := logFixtureRunner(t, "2026-09-28T10:00:00Z "+message+"\r\n2026-09-28T10:00:01Z after")
	item := core.InventoryItem{Pod: "patient", Container: "api"}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	events, err := runner.Snapshot(ctx, item, LogOptions{Tail: -1})
	if err != nil || len(events) != 2 {
		t.Fatalf("snapshot: %d events, err=%v", len(events), err)
	}
	if events[0].Message != message || events[1].Message != "after" {
		t.Fatal("snapshot lost the large line or unterminated final line")
	}
	output := make(chan core.LogEvent, 4)
	if err := runner.Stream(ctx, item, LogOptions{Tail: -1}, output); err != nil {
		t.Fatal(err)
	}
	close(output)
	var streamed []string
	for event := range output {
		if !event.Started && !event.Closed {
			streamed = append(streamed, event.Message)
		}
	}
	if len(streamed) != 2 || streamed[0] != message || streamed[1] != "after" {
		t.Fatal("live stream lost the large error or stopped reading after it")
	}
}

func TestLogDetailsReloadsFullErrorBeyondTailAndFilters(t *testing.T) {
	const timestamp = "2026-09-28T10:00:00Z"
	message := "[10:00:00 ERR] DB query failed"
	var logs strings.Builder
	fmt.Fprintf(&logs, "%s %s\n", timestamp, message)
	for i := 0; i < 1500; i++ {
		fmt.Fprintf(&logs, "%s    at Frame%d()\n", timestamp, i)
	}
	fmt.Fprintf(&logs, "%s Error Number:3980,State:1,Class:16\n", timestamp)
	fmt.Fprintf(&logs, "%s [10:00:01 INF] next request\n", timestamp)
	runner := logFixtureRunner(t, logs.String())
	at, _ := time.Parse(time.RFC3339, timestamp)
	selected := core.LogEvent{Pod: "p", Container: "c", Message: message, ObservedAt: at}
	block, err := runner.LogDetails(context.Background(), selected)
	if err != nil || len(block) != 1502 {
		t.Fatalf("details: %d lines, err=%v", len(block), err)
	}
	if block[len(block)-1].Message != "Error Number:3980,State:1,Class:16" {
		t.Fatal("final SQL error properties were omitted")
	}
	selected.Message = "   at Frame1000()"
	block, err = runner.LogDetails(context.Background(), selected)
	if err != nil || len(block) != 1502 || block[0].Message != message {
		t.Fatalf("stack-frame selection did not find the whole exception: %d lines, err=%v", len(block), err)
	}
	selected.Message = "a rotated log"
	if _, err := runner.LogDetails(context.Background(), selected); err == nil {
		t.Fatal("rotated log was not reported")
	}
}

func TestReadLogLinesPreservesBlankLinesAndStopsOnRequest(t *testing.T) {
	var got []string
	err := readLogLines(strings.NewReader("first\r\n\nlast\nnot read"), func(line string) bool {
		got = append(got, line)
		return len(got) < 3
	})
	if err != nil || strings.Join(got, "|") != "first||last" {
		t.Fatalf("lines=%q, err=%v", got, err)
	}
}
