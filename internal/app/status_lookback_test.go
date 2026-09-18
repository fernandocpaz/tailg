package app

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/fernandocpaz/tailg/internal/core"
)

func TestCommandAcceptsStatusLookbackMinutes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake kubectl is a shell script")
	}
	directory := t.TempDir()
	kubectl := filepath.Join(directory, "kubectl")
	script := `#!/bin/sh
case "$*" in
  *"get pods -o json"*) printf '%s' '{"items":[{"metadata":{"name":"api-pod"},"spec":{"containers":[{"name":"api"}]},"status":{"phase":"Running","containerStatuses":[{"name":"api","ready":true,"state":{"running":{}}}]}}]}' ;;
  *"logs pod/api-pod -c api"*"--since 20m"*) printf '%s\n' '2026-09-11T12:00:00Z [12:00:00 ERR] request failed' ;;
  *) echo "unexpected kubectl args: $*" >&2; exit 2 ;;
esac
`
	if err := os.WriteFile(kubectl, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", directory+string(os.PathListSeparator)+os.Getenv("PATH"))
	var stdout, stderr bytes.Buffer
	command := NewCommand(context.Background(), strings.NewReader(""), &stdout, &stderr)
	command.SetArgs([]string{"--status", "20", "--namespace", "default"})
	if err := command.Execute(); err != nil {
		t.Fatalf("execute: %v; stderr=%s", err, stderr.String())
	}
	for _, expected := range []string{"all pods healthy", "lookback=20m", "request failed"} {
		if !strings.Contains(stdout.String(), expected) {
			t.Fatalf("output missing %q: %s", expected, stdout.String())
		}
	}
}

func TestStatusLookbackFromArgs(t *testing.T) {
	lookback, args, err := statusLookbackFromArgs(nil, core.DefaultStatusLookback)
	if err != nil || lookback != 2*time.Hour || len(args) != 0 {
		t.Fatalf("default lookback = %v, %v, %v", lookback, args, err)
	}
	lookback, args, err = statusLookbackFromArgs([]string{"20"}, core.DefaultStatusLookback)
	if err != nil || lookback != 20*time.Minute || len(args) != 0 {
		t.Fatalf("20 minute lookback = %v, %v, %v", lookback, args, err)
	}
}

func TestStatusLookbackRejectsInvalidArguments(t *testing.T) {
	for _, args := range [][]string{{"0"}, {"-1"}, {"1.5"}, {"abc"}, {"999999999999999999"}, {"20", "extra"}} {
		if _, _, err := statusLookbackFromArgs(args, core.DefaultStatusLookback); err == nil {
			t.Errorf("args %v accepted", args)
		}
	}
}
