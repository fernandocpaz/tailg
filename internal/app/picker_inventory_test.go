package app

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/fernandocpaz/tailg/internal/kube"
)

func TestSelectedInventoryKeepsRemainingPodsWhenOneDisappears(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX kubectl fixture")
	}
	path := filepath.Join(t.TempDir(), "kubectl")
	script := `#!/bin/sh
case "$*" in
  *pod/healthy*) echo '{"metadata":{"name":"healthy"},"spec":{"containers":[{"name":"api"}]},"status":{"phase":"Running"}}' ;;
  *) echo 'pod not found' >&2; exit 1 ;;
esac
`
	if err := os.WriteFile(path, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	runner := kube.Runner{Binary: path, Namespace: "apollo", Context: "tkgs-dev"}
	items, err := selectedInventory(context.Background(), runner, []string{"healthy", "deleted"}, nil)
	if err != nil || len(items) != 1 || items[0].Pod != "healthy" {
		t.Fatalf("remaining inventory = %+v, error = %v", items, err)
	}
}
