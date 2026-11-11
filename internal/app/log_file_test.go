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
