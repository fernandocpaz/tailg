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
	"github.com/fernandocpaz/tailg/internal/kube"
)

type cancelOnLog struct {
	bytes.Buffer
	cancel context.CancelFunc
}

func (w *cancelOnLog) Write(p []byte) (int, error) {
	n, err := w.Buffer.Write(p)
	if strings.Contains(string(p), "second message") {
		w.cancel()
	}
	return n, err
}

func TestPlainFollowReconnectsWithoutDuplicatingDeliveredLogs(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake kubectl uses shell")
	}
	dir := t.TempDir()
	marker := filepath.Join(dir, "marker")
	t.Setenv("TAILG_PLAIN_MARKER", marker)
	binary := filepath.Join(dir, "kubectl")
	script := `#!/bin/sh
echo '2026-10-14T12:00:00Z first message'
if [ -f "$TAILG_PLAIN_MARKER" ]; then
  echo '2026-10-14T12:00:01Z second message'
else
  touch "$TAILG_PLAIN_MARKER"
fi
`
	if err := os.WriteFile(binary, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	stdout := &cancelOnLog{cancel: cancel}
	var stderr bytes.Buffer
	items := []core.InventoryItem{{Pod: "api", Container: "app"}}
	code := followPlain(ctx, kube.Runner{Binary: binary}, items,
		func(context.Context) ([]core.InventoryItem, error) { return items, nil },
		Options{Tail: 10, RefreshInterval: 10 * time.Millisecond}, core.Formatter{}, stdout, &stderr)
	text := stdout.String()
	if code != 0 || strings.Count(text, "first message") != 1 || strings.Count(text, "second message") != 1 {
		t.Fatalf("plain reconnect failed: code=%d stdout=%q stderr=%q", code, text, stderr.String())
	}
}
