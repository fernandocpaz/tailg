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

	initial := readShared(path)
	revision := nextSharedRevision(initial.textRevision)
	if err := writeSharedText(path, "timeout | retry", revision); err != nil {
		t.Fatal(err)
	}
	if err := writeSharedMode(path, true, revision); err != nil {
		t.Fatal(err)
	}
	shared := readShared(path)
	if !shared.textValid || shared.text != "timeout | retry" || !shared.modeValid || !shared.mode {
		t.Fatalf("shared=%+v", shared)
	}

	clearRevision := nextSharedRevision(shared.textRevision)
	if err := writeSharedText(path, "", clearRevision); err != nil {
		t.Fatal(err)
	}
	shared = readShared(path)
	if !shared.textValid || shared.text != "" || !shared.modeValid || !shared.mode {
		t.Fatalf("cleared shared=%+v", shared)
	}
}

func TestSharedFilterTickIgnoresPartialWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "filter")
	if err := InitializeSharedFilter(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(sharedFilterPrefix+"123:456:cGFydGlhbA=="), 0o600); err != nil {
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
		lastTextRev:    nextSharedRevision(sharedRevision{}),
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
	if err := os.WriteFile(path+".mode", []byte(sharedModePrefix+"123:456:mat"), 0o600); err != nil {
		t.Fatal(err)
	}

	if readShared(path).modeValid {
		t.Fatal("partial mode write should not be accepted")
	}
}

func TestSharedFilterTickIgnoresOlderEmptyValue(t *testing.T) {
	path := filepath.Join(t.TempDir(), "filter")
	if err := InitializeSharedFilter(path); err != nil {
		t.Fatal(err)
	}
	initial := readShared(path)
	currentRevision := nextSharedRevision(initial.textRevision)
	if err := writeSharedText(path, "timeout", currentRevision); err != nil {
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
		lastTextRev:    currentRevision,
		followsLive:    true,
	}

	if err := os.WriteFile(path, encodeSharedText("", initial.textRevision), 0o600); err != nil {
		t.Fatal(err)
	}
	updated, _ := m.Update(sharedFilterTick(time.Now()))
	got := updated.(model)
	if got.input.Value() != "timeout" {
		t.Fatalf("filter was cleared by an older shared value: %q", got.input.Value())
	}
}
