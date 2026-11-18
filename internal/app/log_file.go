package app

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/fernandocpaz/tailg/internal/agent"
	"github.com/fernandocpaz/tailg/internal/core"
	"github.com/fernandocpaz/tailg/internal/tui"
)

func pickerLogFilename(selection tui.PickerResult, at time.Time) string {
	label := "selection"
	switch {
	case len(selection.Pods) == 1:
		label = selection.Pods[0]
	case len(selection.Apps) == 1:
		label = selection.Apps[0].Name
	}
	label = regexp.MustCompile(`[^a-zA-Z0-9._-]+`).ReplaceAllString(label, "-")
	label = strings.Trim(label, ".-")
	if len(label) > 80 {
		label = label[:80]
	}
	if label == "" {
		label = "selection"
	}
	return fmt.Sprintf("tailg-%s_%s.ndjson", label, at.Local().Format("20060102_150405.000000000"))
}

// Live captures use exclusive creation; CLI --file retains its append behavior.
// The closer is idempotent so each picker session can close before returning to
// the picker while still being protected by the caller's deferred cleanup.
func openLogCapture(path, kubeContext, namespace string, since time.Time) (func([][]core.LogRecord) error, func() error, error) {
	if path == "" {
		return nil, func() error { return nil }, nil
	}
	flags := os.O_WRONLY | os.O_CREATE | os.O_APPEND
	if !since.IsZero() {
		flags = os.O_WRONLY | os.O_CREATE | os.O_EXCL
	}
	file, err := os.OpenFile(path, flags, 0o600)
	if err != nil {
		return nil, nil, err
	}
	var once sync.Once
	var closeErr error
	closeCapture := func() error {
		once.Do(func() { closeErr = file.Close() })
		return closeErr
	}
	capture := agent.NewLiveLogCapture(file, kubeContext, namespace, since)
	return capture.WriteBatch, closeCapture, nil
}
