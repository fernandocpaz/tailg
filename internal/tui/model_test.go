package tui

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/textinput"

	"github.com/fernandocpaz/tailg/internal/core"
)

func TestSharedFilterRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "filter")
	if err := InitializeSharedFilter(path); err != nil {
		t.Fatal(err)
	}

	writeSharedText(path, "timeout | retry")
	writeSharedMode(path, true)
	text, mode, valid := readShared(path)
	if !valid || text != "timeout | retry" || !mode {
		t.Fatalf("text=%q mode=%v valid=%v", text, mode, valid)
	}

	writeSharedText(path, "")
	text, mode, valid = readShared(path)
	if !valid || text != "" || !mode {
		t.Fatalf("cleared text=%q mode=%v valid=%v", text, mode, valid)
	}
}

func TestSharedFilterTickIgnoresPartialWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "filter")
	if err := InitializeSharedFilter(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(sharedFilterPrefix+"cGFydGlhbA=="), 0o600); err != nil {
		t.Fatal(err)
	}

	input := textinput.New()
	input.SetValue("timeout")
	state := core.NewFilterState(10)
	state.SetFilter("timeout")
	m := model{
		config:         Config{FilterFile: path},
		input:          input,
		state:          state,
		lastSharedText: "timeout",
		followsLive:    true,
	}

	updated, _ := m.Update(sharedFilterTick(time.Now()))
	got := updated.(model)
	if got.input.Value() != "timeout" {
		t.Fatalf("filter was cleared by a partial shared-file read: %q", got.input.Value())
	}
}

func TestSharedFilterRejectsPartialModeWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "filter")
	if err := InitializeSharedFilter(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+".mode", []byte(sharedModePrefix+"mat"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, _, valid := readShared(path)
	if valid {
		t.Fatal("partial mode write should not be accepted")
	}
}
