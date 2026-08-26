package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
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
	if _, err := writeSharedText(path, "timeout | retry", initial.textRevision); err != nil {
		t.Fatal(err)
	}
	if _, err := writeSharedMode(path, true, initial.modeRevision); err != nil {
		t.Fatal(err)
	}
	shared := readShared(path)
	if !shared.textValid || shared.text != "timeout | retry" || !shared.modeValid || !shared.mode {
		t.Fatalf("shared=%+v", shared)
	}

	if _, err := writeSharedText(path, "", shared.textRevision); err != nil {
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
	currentRevision, err := writeSharedText(path, "timeout", initial.textRevision)
	if err != nil {
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

func TestSharedFilterConcurrentWritesRemainOrdered(t *testing.T) {
	path := filepath.Join(t.TempDir(), "filter")
	if err := InitializeSharedFilter(path); err != nil {
		t.Fatal(err)
	}

	const writers = 24
	revisions := make(chan sharedRevision, writers)
	errors := make(chan error, writers)
	var wait sync.WaitGroup
	for index := 0; index < writers; index++ {
		wait.Add(1)
		go func(value string) {
			defer wait.Done()
			revision, err := writeSharedText(path, value, sharedRevision{})
			if err != nil {
				errors <- err
				return
			}
			revisions <- revision
		}(fmt.Sprintf("filter-%d", index))
	}
	wait.Wait()
	close(revisions)
	close(errors)
	for err := range errors {
		t.Fatal(err)
	}

	var newest sharedRevision
	for revision := range revisions {
		if revision.newerThan(newest) {
			newest = revision
		}
	}
	shared := readShared(path)
	if !shared.textValid {
		t.Fatal("final shared filter is invalid")
	}
	if shared.textRevision != newest {
		t.Fatalf("final revision=%+v, want newest=%+v", shared.textRevision, newest)
	}
}
