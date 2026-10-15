package app

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/fernandocpaz/tailg/internal/kube"
)

func TestNetworkFlagAndConflictingModes(t *testing.T) {
	var output bytes.Buffer
	cmd := NewCommand(context.Background(), strings.NewReader(""), &output, &output)
	if flag := cmd.Flags().Lookup("network"); flag == nil || flag.Value.Type() != "bool" {
		t.Fatal("missing --network flag")
	}
	for _, o := range []Options{{Target: "api"}, {Status: true}, {Versions: true}, {SplitPanes: true}, {DumpRequested: true}, {Selector: "app=api"}} {
		o.Network = true
		o.BufferLines = 100
		if code := Run(context.Background(), o, strings.NewReader(""), &output, &output); code != 2 {
			t.Fatalf("conflicting mode accepted: %+v", o)
		}
	}
}

func TestNetworkPlainSnapshotAndPartialExitStatus(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX process fixture; collector and diagnostics run natively on Windows")
	}
	path := filepath.Join(t.TempDir(), "kubectl")
	script := `#!/bin/sh
case "$*" in
 *httpproxies.projectcontour.io*) if [ "$TAILG_TEST_NETWORK_FAIL" = "1" ]; then echo Forbidden >&2; exit 1; fi ;;
esac
echo '{"items":[]}'
`
	if err := os.WriteFile(path, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	r := kube.Runner{Binary: path, Context: "tkgs-qa", Namespace: "apollo"}
	var output bytes.Buffer
	if code := runNetwork(context.Background(), r, Options{Network: true}, strings.NewReader(""), &output, &output); code != 0 || !strings.Contains(output.String(), "Namespace: apollo") || strings.Contains(output.String(), "\x1b[") {
		t.Fatalf("plain output: %d %s", code, output.String())
	}
	t.Setenv("TAILG_TEST_NETWORK_FAIL", "1")
	output.Reset()
	if code := runNetwork(context.Background(), r, Options{Network: true}, strings.NewReader(""), &output, &output); code != 1 || !strings.Contains(output.String(), "INCOMPLETE") {
		t.Fatalf("partial exit: %d %s", code, output.String())
	}
}
