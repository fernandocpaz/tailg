package kube

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestNamespacesUsesSelectedContextWithoutNamespaceOverride(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX kubectl fixture")
	}
	path := filepath.Join(t.TempDir(), "kubectl")
	script := `#!/bin/sh
if [ "$#" -ne 6 ] || [ "$1" != "--context" ] || [ "$2" != "tkgs-qa" ] || [ "$3" != "get" ] || [ "$4" != "namespaces" ] || [ "$5" != "-o" ] || [ "$6" != "json" ]; then
  echo "unexpected args: $*" >&2
  exit 1
fi
echo '{"items":[{"metadata":{"name":"apollo"}},{"metadata":{"name":"lrjob"}}]}'
`
	if err := os.WriteFile(path, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	runner := Runner{Binary: path, Context: "tkgs-qa", Namespace: "ui"}
	namespaces, err := runner.Namespaces(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(namespaces) != 2 || namespaces[0] != "apollo" || namespaces[1] != "lrjob" {
		t.Fatalf("unexpected namespaces: %v", namespaces)
	}
}
