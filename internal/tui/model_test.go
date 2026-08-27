package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/lipgloss"

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

func TestSearchCommandCancelsSupersededSearch(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan string, 2)
	m := model{
		ctx:      ctx,
		searches: &searchController{},
		config: Config{Search: func(searchCtx context.Context, query string) ([]string, error) {
			started <- query
			if query == "first" {
				<-searchCtx.Done()
				return nil, searchCtx.Err()
			}
			return []string{"second result"}, nil
		}},
	}

	first := m.searchCommand(1, "first")
	firstResult := make(chan any, 1)
	go func() { firstResult <- first() }()
	select {
	case query := <-started:
		if query != "first" {
			t.Fatalf("started query = %q", query)
		}
	case <-time.After(time.Second):
		t.Fatal("first search did not start")
	}

	second := m.searchCommand(2, "second")
	select {
	case result := <-firstResult:
		if result != nil {
			t.Fatalf("canceled search returned %#v", result)
		}
	case <-time.After(time.Second):
		t.Fatal("superseded search was not canceled")
	}
	result, ok := second().(searchMsg)
	if !ok || result.query != "second" || len(result.lines) != 1 {
		t.Fatalf("latest search result = %#v", result)
	}
}

func TestManageStreamsRestartsCompletedStream(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	events := make(chan core.LogEvent)
	started := make(chan int32, 2)
	var starts atomic.Int32
	config := Config{
		Items: []core.InventoryItem{{Pod: "pod-1", Container: "app"}},
		Stream: func(streamCtx context.Context, _ core.InventoryItem, _ chan<- core.LogEvent) error {
			count := starts.Add(1)
			started <- count
			if count == 1 {
				return errors.New("stream ended")
			}
			<-streamCtx.Done()
			return streamCtx.Err()
		},
	}
	closed := make(chan struct{})
	go func() {
		manageStreams(ctx, config, events)
		close(closed)
	}()

	for want := int32(1); want <= 2; want++ {
		select {
		case got := <-started:
			if got != want {
				t.Fatalf("stream start = %d, want %d", got, want)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("stream start %d did not occur", want)
		}
	}
	cancel()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("stream manager did not stop")
	}
}

func TestManageStreamsWaitsBeforeClosingEvents(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	events := make(chan core.LogEvent)
	started := make(chan struct{})
	release := make(chan struct{})
	config := Config{
		Items: []core.InventoryItem{{Pod: "pod-1", Container: "app"}},
		Stream: func(streamCtx context.Context, _ core.InventoryItem, _ chan<- core.LogEvent) error {
			close(started)
			<-streamCtx.Done()
			<-release
			return streamCtx.Err()
		},
	}
	closed := make(chan struct{})
	go func() {
		manageStreams(ctx, config, events)
		close(closed)
	}()
	<-started
	cancel()
	select {
	case <-closed:
		t.Fatal("events closed before the stream exited")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("events did not close after the stream exited")
	}
}

func TestParseLogColumns(t *testing.T) {
	columns := parseLogColumns("\x1b[90m[pod-7d9]\x1b[0m [14:22:10 ERR] request timeout", true)
	if columns.pod != "pod-7d9" || columns.time != "14:22:10" || columns.level != "ERR" || columns.message != "request timeout" {
		t.Fatalf("columns = %#v", columns)
	}

	columns = parseLogColumns("14:22:10.123 [WARNING] [Inventory] retrying", false)
	if columns.time != "14:22:10.123" || columns.level != "WRN" || columns.message != "[Inventory] retrying" {
		t.Fatalf("structured columns = %#v", columns)
	}
}

func TestViewRendersOperationsConsoleLayout(t *testing.T) {
	input := textinput.New()
	input.SetValue("timeout")
	input.Width = 24
	state := core.NewFilterState(20)
	state.Append("[pod-7d9] [14:22:10 ERR] request timeout after 3000 ms")
	state.SetFilter("timeout")
	m := model{
		config: Config{
			Namespace: "production",
			Formatter: core.Formatter{ShowPod: true, Color: false},
		},
		items:         []core.InventoryItem{{Pod: "checkout-7d9", Container: "checkout-api"}},
		state:         state,
		input:         input,
		width:         120,
		height:        12,
		selected:      0,
		followsLive:   true,
		searchMatches: 1,
		searchLines:   8,
	}
	view := m.View()
	for _, expected := range []string{"tailg", "checkout-api", "production", "1 pod", "LIVE", "FILTER", "[CONTEXT]", "1 matches • 8 lines", "14:22:10", "ERR", "pod-7d9", "request timeout"} {
		if !strings.Contains(view, expected) {
			t.Fatalf("view missing %q:\n%s", expected, view)
		}
	}
}

func TestRenderLogRowStaysWithinTerminalWidth(t *testing.T) {
	row := renderLogRow("[pod-7d9] [14:22:10 INF] "+strings.Repeat("message ", 30), "message", false, 72, true, false)
	if width := lipgloss.Width(row); width > 72 {
		t.Fatalf("row width = %d, want <= 72: %q", width, row)
	}
}
