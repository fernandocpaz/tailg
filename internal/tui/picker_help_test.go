package tui

import (
	"strings"
	"testing"

	"github.com/fernandocpaz/tailg/internal/core"
)

func TestRenderPickerShortcutsKeepsKeysAndLabelsScannable(t *testing.T) {
	rendered := renderPickerShortcuts(
		shortcut("SPACE", "Select"),
		shortcut("ENTER", "Open"),
	)
	plain := core.StripANSI(rendered)
	words := strings.Join(strings.Fields(plain), " ")
	if !strings.Contains(words, "SPACE Select") || !strings.Contains(words, "ENTER Open") {
		t.Fatalf("shortcut help lost key labels: %q", plain)
	}
	if !strings.Contains(plain, "·") {
		t.Fatalf("shortcut help lost its visual separator: %q", plain)
	}
}
