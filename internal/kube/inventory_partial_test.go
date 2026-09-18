package kube

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestInventoryForPodsReturnsSurvivorsWithMissingPodError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake kubectl is a shell script")
	}
	path := filepath.Join(t.TempDir(), "kubectl")
	script := `#!/bin/sh
case "$*" in
  *"get pod/gone -o json"*) echo 'pods "gone" not found' >&2; exit 1 ;;
  *"get pod/live -o json"*) printf '%s' '{"metadata":{"name":"live"},"spec":{"containers":[{"name":"api"}]}}' ;;
  *) echo "unexpected kubectl args: $*" >&2; exit 2 ;;
esac
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	items, err := (Runner{Namespace: "default", Binary: path}).InventoryForPods(context.Background(), []string{"gone", "live"})
	if len(items) != 1 || items[0].Pod != "live" || items[0].Container != "api" {
		t.Fatalf("surviving inventory = %+v", items)
	}
	if err == nil || !strings.Contains(err.Error(), "pod/gone") {
		t.Fatalf("missing-pod error = %v", err)
	}
}
