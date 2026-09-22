package kube

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestContextsReadsAllKubeconfigContextsWithoutSessionOverrides(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX kubectl fixture")
	}
	path := filepath.Join(t.TempDir(), "kubectl")
	script := `#!/bin/sh
if [ "$#" -ne 4 ] || [ "$1" != "config" ] || [ "$2" != "view" ] || [ "$3" != "-o" ] || [ "$4" != "json" ]; then
  echo "unexpected args: $*" >&2
  exit 1
fi
cat <<'JSON'
{"current-context":"dev","contexts":[{"name":"dev","context":{"namespace":"apollo"}},{"name":"qa","context":{"namespace":"api"}},{"name":"local","context":{}}]}
JSON
`
	if err := os.WriteFile(path, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	runner := Runner{Binary: path, Context: "qa", Namespace: "lrjob"}
	contexts, err := runner.Contexts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(contexts) != 3 {
		t.Fatalf("got %d contexts, want 3", len(contexts))
	}
	if contexts[0] != (ContextInfo{Name: "dev", Namespace: "apollo", Current: true}) ||
		contexts[1] != (ContextInfo{Name: "qa", Namespace: "api"}) ||
		contexts[2] != (ContextInfo{Name: "local", Namespace: "default"}) {
		t.Fatalf("unexpected contexts: %+v", contexts)
	}
}
