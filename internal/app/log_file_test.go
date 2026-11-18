package app

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/fernandocpaz/tailg/internal/core"
	"github.com/fernandocpaz/tailg/internal/tui"
)

func TestCommandAppendsStructuredSnapshotToFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake kubectl uses shell")
	}
	dir := t.TempDir()
	kubectl := `#!/bin/sh
case "$*" in
  *"config view --minify"*) printf '%s' '{"current-context":"test","contexts":[{"name":"test","context":{"namespace":"apollo"}}]}' ;;
  *"get pod/api-pod"*) printf '%s' '{"metadata":{"name":"api-pod"},"spec":{"containers":[{"name":"api"}]}}' ;;
  *"logs pod/api-pod -c api"*) printf '%s\n' '2026-10-14T12:00:00.123456789Z {"level":"Error","message":"request failed","exception":"stack trace\nnext frame","requestId":"req-123","traceId":"0123456789abcdef0123456789abcdef","RequestMethod":"GET","RequestPath":"/encounter","StatusCode":500,"Elapsed":451.25,"token":"test-token","Extra":"source detail"}' '2026-10-14T12:00:01Z INFO GET /health' ;;
  *) echo "unexpected kubectl args: $*" >&2; exit 2 ;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "kubectl"), []byte(kubectl), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	path := filepath.Join(dir, "my log.txt")
	const previous = "{\"schemaVersion\":\"tailg.log/v1\",\"message\":\"previous capture\"}\n"
	if err := os.WriteFile(path, []byte(previous), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	command := NewCommand(context.Background(), strings.NewReader(""), &stdout, &stderr)
	command.SetArgs([]string{"api-pod", "-f", path, "--no-follow"})
	if err := command.Execute(); err != nil {
		t.Fatalf("execute: %v; stderr=%s", err, stderr.String())
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(content, []byte(previous)) || bytes.Count(content, []byte("\n")) != 2 {
		t.Fatalf("expected existing entry and one new JSON line, got %q", content)
	}
	var record map[string]any
	if err := json.Unmarshal(content[len(previous):], &record); err != nil {
		t.Fatal(err)
	}
	for key, expected := range map[string]any{
		"schemaVersion": "tailg.log/v1", "context": "test", "namespace": "apollo",
		"pod": "api-pod", "container": "api", "timestamp": "2026-10-14T12:00:00.123456789Z",
		"level": "ERR", "message": "[ERR] request failed\nstack trace\nnext frame",
		"requestId": "req-123", "traceId": "0123456789abcdef0123456789abcdef",
		"method": "GET", "path": "/encounter", "statusCode": float64(500), "durationMs": 451.25, "slow": true,
	} {
		if record[key] != expected {
			t.Errorf("%s = %v, want %v", key, record[key], expected)
		}
	}
	raw, _ := record["raw"].(string)
	if !strings.Contains(raw, "source detail") || !strings.Contains(raw, "[REDACTED]") ||
		bytes.Contains(content, []byte("test-token")) || bytes.Contains(content, []byte(`\u001b`)) {
		t.Fatalf("unexpected raw capture: %q", raw)
	}
	if !strings.Contains(stdout.String(), "request failed") {
		t.Fatalf("console output missing log: %q", stdout.String())
	}
}

func TestLiveCaptureExcludesBackfillAndClosesBetweenSessions(t *testing.T) {
	start := time.Date(2026, 11, 18, 10, 30, 0, 123456789, time.UTC)
	selection := tui.PickerResult{Pods: []string{"api-1", "api-2"}, LogToFile: true}
	name := pickerLogFilename(selection, start)
	if !strings.HasPrefix(name, "tailg-selection_") || !strings.HasSuffix(name, ".ndjson") || strings.ContainsAny(name, `:/\\`) {
		t.Fatalf("unsafe filename: %q", name)
	}
	path := filepath.Join(t.TempDir(), name)
	write, closeCapture, err := openLogCapture(path, "qa", "apollo", start)
	if err != nil {
		t.Fatal(err)
	}
	defer closeCapture()
	var events [][]core.LogRecord
	for _, event := range []core.LogEvent{
		{ObservedAt: start.Add(-time.Nanosecond), Message: "old backfill"},
		{Message: "unknown timestamp"},
		{ObservedAt: start, Message: "at keypress"},
		{ObservedAt: start.Add(time.Second), Message: "new data"},
		{ObservedAt: start.Add(time.Second), Message: "replay", Replayed: true},
	} {
		event.Pod, event.Container = "api-1", "api"
		events = append(events, []core.LogRecord{{Event: event, Text: event.Message}})
	}
	if err := write(events); err != nil {
		t.Fatal(err)
	}
	if err := closeCapture(); err != nil {
		t.Fatal(err)
	}
	if err := closeCapture(); err != nil {
		t.Fatalf("second close: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Count(data, []byte("\n")) != 2 || !bytes.Contains(data, []byte("at keypress")) || !bytes.Contains(data, []byte("new data")) || bytes.Contains(data, []byte("backfill")) {
		t.Fatalf("unexpected live capture: %s", data)
	}
	if _, _, err := openLogCapture(path, "qa", "apollo", start); !os.IsExist(err) {
		t.Fatalf("live capture must not overwrite existing file: %v", err)
	}
	if pickerSelectionOpensPanes(selection, "windows") {
		t.Fatal("file capture must stay in one terminal")
	}
}
