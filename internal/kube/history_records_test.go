package kube

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/fernandocpaz/tailg/internal/core"
)

const historyTraceID = "0123456789abcdef0123456789abcdef"

// Re-execute this test binary as kubectl, avoiding POSIX shell fixtures on Windows.
func TestMain(m *testing.M) {
	if os.Getenv("TAILG_HISTORY_HELPER") == "1" {
		args := strings.Join(os.Args[1:], " ")
		emit := func(timestamp, message, trace string) {
			fmt.Printf("2026-09-10T12:00:%sZ {\"trace_id\":\"%s\",\"msg\":\"%s\"}\n", timestamp, trace, message)
		}
		switch {
		case strings.Contains(args, "pod/fail"):
			fmt.Fprintln(os.Stderr, "pod unavailable")
			os.Exit(7)
		case strings.Contains(args, "pod/one"):
			emit("02", "one-late", historyTraceID)
			emit("04", "one-last", historyTraceID)
		case strings.Contains(args, "pod/two"):
			emit("01", "two-first", historyTraceID)
			emit("03", "other", "ffffffffffffffffffffffffffffffff")
		case strings.Contains(args, "pod/ready"):
			emit("02", "ready", historyTraceID)
		case strings.Contains(args, "pod/first"):
			emit("03", "first", historyTraceID)
		case strings.Contains(args, "pod/second"):
			emit("01", "second", historyTraceID)
		case strings.Contains(args, "pod/slow"):
			time.Sleep(5 * time.Second)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func fakeKubectl(t *testing.T) string {
	t.Helper()
	t.Setenv("TAILG_HISTORY_HELPER", "1")
	// Race-instrumented helper processes otherwise sleep one second on exit,
	// consuming the collection timeout before the deliberately slow stream.
	t.Setenv("GORACE", os.Getenv("GORACE")+" atexit_sleep_ms=0")
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCompleteRecordsSearchesEveryStreamBeforeLimit(t *testing.T) {
	runner := NewRunner("default", "")
	runner.Binary = fakeKubectl(t)
	items := []core.InventoryItem{{Pod: "one", Container: "app"}, {Pod: "two", Container: "app"}}
	query := "trace:" + historyTraceID
	records, err := runner.CompleteRecords(context.Background(), items, "", core.Formatter{}, query, 6)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 4 {
		t.Fatalf("records = %d, want complete context from both streams", len(records))
	}
	if !strings.Contains(records[len(records)-1].Text, "one-last") {
		t.Fatalf("last record = %q, expected the second pod's history to be searched", records[len(records)-1].Text)
	}
	if records[0].ID == 0 || records[0].Fields.TraceID != historyTraceID {
		t.Fatalf("record metadata was not preserved: %#v", records[0])
	}
	lines, err := runner.CompleteHistory(context.Background(), items, "", core.Formatter{}, query, 6)
	if err != nil || len(lines) != len(records) {
		t.Fatalf("CompleteHistory = %d, %v; want text compatibility", len(lines), err)
	}
}

func TestCompleteTraceSortsAcrossPodsAndReportsPartialFailure(t *testing.T) {
	runner := NewRunner("default", "")
	runner.Binary = fakeKubectl(t)
	items := []core.InventoryItem{{Pod: "one", Container: "app"}, {Pod: "fail", Container: "app"}, {Pod: "two", Container: "app"}}
	records, err := runner.CompleteTrace(context.Background(), items, "", core.Formatter{}, strings.ToUpper(historyTraceID), 2)
	if err == nil || !strings.Contains(err.Error(), "incomplete") {
		t.Fatalf("trace error = %v, want an explicit partial-history error", err)
	}
	if len(records) != 2 {
		t.Fatalf("records = %d, want maxLines 2", len(records))
	}
	if !strings.Contains(records[0].Text, "two-first") || !strings.Contains(records[1].Text, "one-late") {
		t.Fatalf("trace order = %#v, want Kubernetes timestamp order", records)
	}
	if records[0].Event.Pod != "two" || records[1].Event.Pod != "one" {
		t.Fatalf("trace identities were not preserved: %#v", records)
	}
}

func TestCompleteTraceReportsTruncation(t *testing.T) {
	runner := NewRunner("default", "")
	runner.Binary = fakeKubectl(t)
	items := []core.InventoryItem{{Pod: "one", Container: "app"}, {Pod: "two", Container: "app"}}
	records, err := runner.CompleteTrace(context.Background(), items, "", core.Formatter{}, historyTraceID, 1)
	if err == nil || !strings.Contains(err.Error(), "truncated") {
		t.Fatalf("trace error = %v, want a truncation warning", err)
	}
	if len(records) != 1 || !strings.Contains(records[0].Text, "two-first") {
		t.Fatalf("records = %#v, want capped earliest row", records)
	}
}

func TestCompleteTraceRejectsInvalidID(t *testing.T) {
	runner := NewRunner("default", "")
	if _, err := runner.CompleteTrace(context.Background(), nil, "", core.Formatter{}, "not-a-trace", 10); err == nil {
		t.Fatal("invalid trace ID was accepted")
	}
}

func TestCompleteTraceRetainsRecordsOnTimeout(t *testing.T) {
	path := fakeKubectl(t)
	runner := NewRunner("default", "")
	runner.Binary = path
	items := []core.InventoryItem{{Pod: "ready", Container: "app"}, {Pod: "slow", Container: "app"}}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	records, err := runner.CompleteTrace(ctx, items, "", core.Formatter{}, historyTraceID, 10)
	if err == nil || !strings.Contains(err.Error(), "incomplete") {
		t.Fatalf("trace error = %v, want an explicit partial-history error", err)
	}
	if len(records) != 1 || !strings.Contains(records[0].Text, "ready") {
		t.Fatalf("records = %#v, want the successfully collected stream", records)
	}
}

func TestCompleteTraceSortsBeforeCappingTimedOutCollection(t *testing.T) {
	path := fakeKubectl(t)
	runner := NewRunner("default", "")
	runner.Binary = path
	items := []core.InventoryItem{
		{Pod: "first", Container: "app"},
		{Pod: "second", Container: "app"},
		{Pod: "slow", Container: "app"},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	records, err := runner.CompleteTrace(ctx, items, "", core.Formatter{}, historyTraceID, 1)
	if err == nil || !strings.Contains(err.Error(), "1 of 3 streams failed") {
		t.Fatalf("trace error = %v, want one timed out stream", err)
	}
	if len(records) != 1 || !strings.Contains(records[0].Text, "second") {
		t.Fatalf("records = %#v, want earliest timestamp after sorting and capping", records)
	}
}
