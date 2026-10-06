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

func TestPodErrorLogsReadsAllRetainedContainersAndReportsPartialFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake kubectl uses a POSIX shell")
	}
	dir := t.TempDir()
	argsPath := filepath.Join(dir, "args")
	t.Setenv("TAILG_ERRORS_ARGS", argsPath)
	t.Setenv("TAILG_ERRORS_FAIL", "")
	script := `#!/bin/sh
printf '%s\n' "$*" >> "$TAILG_ERRORS_ARGS"
case "$*" in
  *"get pod/patient -o json"*) printf '%s\n' '{"metadata":{"name":"patient"},"spec":{"containers":[{"name":"api"},{"name":"sidecar"}],"initContainers":[{"name":"init"}],"ephemeralContainers":[{"name":"debug"}]}}' ;;
  *"logs pod/patient -c api"*)
    printf '%s\n' '2026-10-04T10:00:00Z [10:00:00 ERR] old error outside startup tail' '2026-10-04T10:00:00Z    at FullStack()' '2026-10-04T10:00:00Z Error Number:3980' '2026-10-06T10:00:00Z [10:00:00 INF] normal'
    ;;
  *"logs pod/patient -c sidecar"*)
    if [ -n "$TAILG_ERRORS_FAIL" ]; then echo 'permission denied' >&2; exit 1; fi
    printf '%s\n' '2026-10-05T10:00:00Z [10:00:00 FTL] fatal sidecar'
    ;;
  *"logs pod/patient -c init"*) printf '%s\n' '2026-10-03T10:00:00Z {"level":"Fatal","message":"init error","extra":"raw"}' ;;
  *"logs pod/patient -c debug"*) printf '%s\n' '2026-10-06T10:00:00Z FTL debug error' ;;
  *) echo "unexpected command $*" >&2; exit 1 ;;
esac
`
	binary := filepath.Join(dir, "kubectl")
	if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	runner := Runner{Binary: binary, Context: "tkgs-qa", Namespace: "apollo"}
	blocks, err := runner.PodErrorLogs(context.Background(), "patient")
	if err != nil || len(blocks) != 4 || blocks[0][0].Container != "init" || blocks[3][0].Container != "debug" || len(blocks[1]) != 3 {
		t.Fatalf("retained blocks missing, truncated, or unordered: %+v, %v", blocks, err)
	}
	args, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(args)), "\n") {
		if !strings.HasPrefix(line, "--context tkgs-qa -n apollo ") || strings.Contains(line, "--since") || strings.Contains(line, " -f") {
			t.Fatalf("wrong history scope: %s", line)
		}
		if strings.Contains(line, "logs ") && !strings.HasSuffix(line, "--tail -1") {
			t.Fatalf("error fetch must be unbounded by startup tail: %s", line)
		}
	}
	t.Setenv("TAILG_ERRORS_FAIL", "1")
	blocks, err = runner.PodErrorLogs(context.Background(), "patient")
	if len(blocks) != 3 || err == nil || !strings.Contains(err.Error(), "patient/sidecar") {
		t.Fatalf("partial results or failure source missing: %d, %v", len(blocks), err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := runner.PodErrorLogs(ctx, "patient"); err == nil {
		t.Fatal("cancelled retrieval must fail")
	}
}

func TestContainerErrorLogsPreservesLargeFatalAndPartialStdout(t *testing.T) {
	message := "[10:00:00 FTL] " + strings.Repeat("x", 5*1024*1024) + " LARGE_FATAL_END"
	logs := "2026-10-04T10:00:00Z " + message + "\n2026-10-04T10:00:00Z    at FinalFrame()\n" +
		strings.Repeat("2026-10-06T10:00:00Z [10:00:00 INF] ordinary traffic\n", 10000)
	runner := logFixtureRunner(t, logs)
	item := core.InventoryItem{Pod: "p", Container: "api"}
	blocks, err := runner.containerErrorLogs(context.Background(), item)
	if err != nil || len(blocks) != 1 || len(blocks[0]) != 2 || blocks[0][0].Message != message {
		t.Fatalf("large fatal was truncated or ordinary traffic retained: %d, %v", len(blocks), err)
	}
	if err := os.WriteFile(runner.Binary, []byte("#!/bin/sh\ncat \"$TAILG_DETAIL_LOGS\"\necho 'stream failed' >&2\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	blocks, err = runner.containerErrorLogs(context.Background(), item)
	if err == nil || !strings.Contains(err.Error(), "stream failed") || len(blocks) != 1 || blocks[0][0].Message != message {
		t.Fatalf("partial stdout was discarded on stream failure: %d, %v", len(blocks), err)
	}
}
