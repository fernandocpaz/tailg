package app

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCommandAppendsFormattedSnapshotToFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake kubectl uses shell")
	}
	dir := t.TempDir()
	kubectl := `#!/bin/sh
case "$*" in
  *"config view --minify"*) printf '%s' '{"current-context":"test"}' ;;
  *"get pod/api-pod"*) printf '%s' '{"metadata":{"name":"api-pod"},"spec":{"containers":[{"name":"api"}]}}' ;;
  *"logs pod/api-pod -c api"*) printf '%s\n' '2026-10-14T12:00:00Z {"level":"Error","message":"request failed","exception":"stack trace"}' '2026-10-14T12:00:01Z INFO GET /health' ;;
  *) echo "unexpected kubectl args: $*" >&2; exit 2 ;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "kubectl"), []byte(kubectl), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	path := filepath.Join(dir, "my log.txt")
	if err := os.WriteFile(path, []byte("previous capture\n"), 0o600); err != nil {
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
	want := "previous capture\n[ERR] request failed\nstack trace\n"
	if string(content) != want {
		t.Fatalf("capture = %q, want %q", content, want)
	}
	if !strings.Contains(stdout.String(), "request failed") {
		t.Fatalf("console output missing log: %q", stdout.String())
	}
}
