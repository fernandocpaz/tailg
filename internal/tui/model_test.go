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
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

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
		Stream: func(streamCtx context.Context, _ core.InventoryItem, _ *core.LogCursor, _ chan<- core.LogEvent) error {
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
		manageStreams(ctx, config, events, nil)
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
		Stream: func(streamCtx context.Context, _ core.InventoryItem, _ *core.LogCursor, _ chan<- core.LogEvent) error {
			close(started)
			<-streamCtx.Done()
			<-release
			return streamCtx.Err()
		},
	}
	closed := make(chan struct{})
	go func() {
		manageStreams(ctx, config, events, nil)
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
	m.recordFreshness(core.LogEvent{Pod: "checkout-7d9", Container: "checkout-api", ReceivedAt: time.Now(), ObservedAt: time.Now()})
	view := m.View()
	for _, expected := range []string{"tailg", "checkout-api", "production", "1 pod", "LIVE", "FILTER", "[CONTEXT]", "1 matches • 8 lines", "14:22:10", "ERR", "pod-7d9", "request timeout"} {
		if !strings.Contains(view, expected) {
			t.Fatalf("view missing %q:\n%s", expected, view)
		}
	}
}

func TestNarrowLogWindowHeaderShowsContext(t *testing.T) {
	m := model{
		config: Config{
			KubeContext: "tkgs-qa",
			Namespace:   "apollo",
			Formatter:   core.Formatter{Color: false},
		},
		items:       []core.InventoryItem{{Pod: "checkout-7d9", Container: "checkout-api"}},
		width:       48,
		followsLive: true,
	}

	header := m.renderHeader()
	for _, expected := range []string{"tailg", "context tkgs-qa", "LIVE"} {
		if !strings.Contains(header, expected) {
			t.Fatalf("narrow header missing %q: %q", expected, header)
		}
	}
	if width := lipgloss.Width(header); width > m.width {
		t.Fatalf("header width = %d, want <= %d: %q", width, m.width, header)
	}
}

func TestInitIncludesWindowTitleCommand(t *testing.T) {
	m := model{
		config:    Config{Title: "tailg | context=tkgs-qa"},
		events:    make(chan core.LogEvent),
		inventory: make(chan inventoryMsg),
		state:     core.NewFilterState(10),
		input:     textinput.New(),
	}
	command := m.Init()
	batch, ok := command().(tea.BatchMsg)
	if !ok {
		t.Fatalf("Init command returned %T, want tea.BatchMsg", command())
	}
	if len(batch) != 4 {
		t.Fatalf("Init batch has %d commands, want 4 including the window title", len(batch))
	}
}

func TestRenderLogRowStaysWithinTerminalWidth(t *testing.T) {
	row := renderLogRow("[pod-7d9] [14:22:10 INF] "+strings.Repeat("message ", 30), "message", false, 72, true, false)
	if width := lipgloss.Width(row); width > 72 {
		t.Fatalf("row width = %d, want <= 72: %q", width, row)
	}
}

func TestRenderErrorLevelColorProfiles(t *testing.T) {
	previousProfile := lipgloss.ColorProfile()
	t.Cleanup(func() { lipgloss.SetColorProfile(previousProfile) })

	for _, tc := range []struct {
		name    string
		profile termenv.Profile
		color   string
	}{
		{"truecolor", termenv.TrueColor, "38;2;255;107;107mERR"},
		{"256 colors", termenv.ANSI256, "38;5;203mERR"},
		{"16 colors", termenv.ANSI, "31mERR"},
		{"plain terminal", termenv.Ascii, "ERR"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lipgloss.SetColorProfile(tc.profile)
			formatter := core.Formatter{Color: true}
			for _, message := range []string{
				"[16:16:48 ERR] [] [pid:55] request failed",
				`{"ts":"2026-09-08T16:16:48Z","level":"ERROR","message":"request failed"}`,
			} {
				line := formatter.Format("api-pod", "api", message, false)[0]
				row := renderLogRow(line, "", false, 100, false, true)
				if !strings.Contains(row, tc.color) {
					t.Fatalf("error level missing readable color %q: %q", tc.color, row)
				}
				for _, selected := range []bool{false, true} {
					for _, color := range []bool{false, true} {
						row := renderLogRow(line, "", selected, 100, false, color)
						plain := core.StripANSI(row)
						if !strings.Contains(plain, "ERR") || !strings.Contains(plain, "request failed") {
							t.Fatalf("error text missing (selected=%t, color=%t): %q", selected, color, row)
						}
						if !color && row != plain {
							t.Fatalf("color disabled but row contains ANSI: %q", row)
						}
					}
				}
			}
		})
	}
}

func TestHighlightTextUsesOriginalUnicodeOffsets(t *testing.T) {
	rendered := highlightText("Ⱥa", "a", true, false)
	if plain := core.StripANSI(rendered); plain != "Ⱥa" {
		t.Fatalf("highlighted text = %q, want %q", plain, "Ⱥa")
	}
	if !strings.Contains(rendered, matchStyle.Render("a")) {
		t.Fatalf("highlight did not select the matching original text: %q", rendered)
	}
}

func TestReconnectStateIsTrackedPerStream(t *testing.T) {
	m := model{
		config:      Config{Formatter: core.Formatter{}},
		state:       core.NewFilterState(10),
		heartbeat:   &core.HeartbeatAnalyzer{},
		width:       100,
		height:      10,
		followsLive: true,
	}

	updated, _ := m.Update(logMsg(core.LogEvent{Pod: "pod-a", Container: "app", Closed: true, Err: errors.New("stream failed")}))
	m = updated.(model)
	updated, _ = m.Update(logMsg(core.LogEvent{Pod: "pod-b", Container: "app", Message: "healthy"}))
	m = updated.(model)
	if !m.isReconnecting() || !strings.Contains(m.renderHeader(), "RECONNECTING") {
		t.Fatalf("healthy traffic from another stream cleared reconnect state: %q", m.renderHeader())
	}

	updated, _ = m.Update(logMsg(core.LogEvent{Pod: "pod-a", Container: "app", Message: "recovered"}))
	m = updated.(model)
	if m.isReconnecting() || !strings.Contains(m.renderHeader(), "LIVE") {
		t.Fatalf("recovered stream did not clear reconnect state: %q", m.renderHeader())
	}
}

func TestIssueRadarRendersGroupedIssuesAndHeaderBadge(t *testing.T) {
	now := time.Now()
	radar := core.NewIssueRadar(20)
	radar.Observe(core.LogEvent{Pod: "checkout-7d9", Container: "checkout-api", Message: "request timed out after 3000 ms", ObservedAt: now})
	m := model{
		config:      Config{Namespace: "production", Formatter: core.Formatter{Color: false}},
		items:       []core.InventoryItem{{Pod: "checkout-7d9", Container: "checkout-api"}},
		state:       core.NewFilterState(20),
		input:       textinput.New(),
		issues:      radar,
		issueOpen:   true,
		width:       120,
		height:      12,
		followsLive: true,
	}

	view := m.View()
	for _, expected := range []string{"Issue radar", "1 active • 1 events", "ERR", "checkout-api", "request timed out", "Enter loads context", "F3/Esc closes"} {
		if !strings.Contains(view, expected) {
			t.Fatalf("issue radar missing %q:\n%s", expected, view)
		}
	}
	m.issueOpen = false
	if header := m.renderHeader(); !strings.Contains(header, "⚠ 1 ISSUE") {
		t.Fatalf("header missing issue badge: %q", header)
	}
}

func TestIssueRadarEnterLoadsCompleteHistoryContext(t *testing.T) {
	radar := core.NewIssueRadar(20)
	radar.Observe(core.LogEvent{Pod: "api-a", Container: "api", Message: "database timeout after 3000 ms", ObservedAt: time.Now()})
	state := core.NewFilterState(20)
	state.Append("[api-a] [12:00:00 INF] before", "[api-a] [12:00:01 ERR] database timeout after 3000 ms", "[api-a] [12:00:02 INF] after")
	state.SetMatchesOnly(true)
	m := model{
		ctx:       context.Background(),
		config:    Config{Search: func(context.Context, string) ([]string, error) { return nil, nil }},
		state:     state,
		input:     textinput.New(),
		issues:    radar,
		issueOpen: true,
		searches:  &searchController{},
	}

	updated, command := m.updateIssueKey("enter")
	got := updated.(model)
	if command == nil || got.issueOpen || got.input.Value() != "timeout" || got.state.MatchesOnly() || got.followsLive {
		t.Fatalf("issue context state = open:%t filter:%q matchesOnly:%t followsLive:%t command:%v", got.issueOpen, got.input.Value(), got.state.MatchesOnly(), got.followsLive, command)
	}
}
