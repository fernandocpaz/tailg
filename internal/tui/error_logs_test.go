package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/fernandocpaz/tailg/internal/core"
)

func errorTestBlock(pod, container, message string, at time.Time) []core.LogEvent {
	return []core.LogEvent{{Pod: pod, Container: container, Message: message, ObservedAt: at}}
}

func TestF3LoadsSelectedPodRetainedErrorsAndKeepsLiveFilter(t *testing.T) {
	m := workflowModel()
	m.items = []core.InventoryItem{{Pod: "first", Container: "api"}, {Pod: "selected", Container: "api"}}
	event := core.LogEvent{Pod: "selected", Container: "api", Message: "[10:00:00 INF] selected row"}
	m.state.AppendRecords(core.RecordsForEvent(m.config.Formatter, event, false)...)
	m.selected = 0
	m.input.SetValue("selected")
	m.state.SetFilter("selected")
	called := false
	m.config.ErrorLogs = func(_ context.Context, pod string) ([][]core.LogEvent, error) {
		called = true
		if pod != "selected" {
			t.Fatalf("F3 chose the wrong pod: %s", pod)
		}
		return [][]core.LogEvent{errorTestBlock(pod, "api", `[10:00:00 ERR] old health error {"full":"properties"}`, time.Now().AddDate(0, 0, -2))}, nil
	}
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyF3})
	m = updated.(model)
	if cmd == nil || called || !m.errorLogs.open || !m.errorLogs.loading || m.issueOpen {
		t.Fatal("F3 must schedule an asynchronous error fetch")
	}
	updated, _ = m.Update(cmd())
	m = updated.(model)
	if !called || m.errorLogs.loading || m.errorLogs.entries != 1 || !strings.Contains(m.errorLogs.text, `{"full":"properties"}`) {
		t.Fatal("raw retained error result was not loaded")
	}
	if m.input.Value() != "selected" || len(m.state.AllRecords()) != 1 || m.state.AllRecords()[0].Event.Message != event.Message || !m.followsLive {
		t.Fatal("F3 changed the live filter, buffer, or follow state")
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyF3})
	if updated.(model).errorLogs.open {
		t.Fatal("F3 must return to the live view")
	}
	updated, _ = updated.(model).Update(tea.KeyMsg{Type: tea.KeyF8})
	if !updated.(model).issueOpen {
		t.Fatal("Issue Radar must remain available on F8")
	}
}

func TestF3DateGroupsUseSameLabelsAndKeepStackUnderRootDate(t *testing.T) {
	location := time.FixedZone("UTC-4", -4*60*60)
	now := time.Date(2026, 10, 6, 0, 5, 0, 0, location)
	yesterday := time.Date(2026, 10, 5, 23, 59, 0, 0, location)
	m := workflowModel()
	m.errorLogs = errorLogsState{open: true, pod: "p", generation: 1, layout: &errorLogsLayout{}}
	blocks := [][]core.LogEvent{
		errorTestBlock("p", "api", "ERR oldest", now.AddDate(0, 0, -2)),
		errorTestBlock("p", "api", "FTL yesterday", yesterday),
		errorTestBlock("p", "api", "ERR today", now),
	}
	blocks[1] = append(blocks[1], core.LogEvent{Pod: "p", Container: "api", Message: "   at AfterMidnight()", ObservedAt: now})
	updated, _ := m.Update(errorLogsMsg{pod: "p", generation: 1, blocks: blocks})
	m = updated.(model)
	rows := m.errorLogRows(now)
	var text []string
	var labels []string
	for _, row := range rows {
		text = append(text, row.text)
		if row.separator {
			labels = append(labels, row.groupLabel)
		}
		if strings.Contains(row.text, "AfterMidnight") && row.groupLabel != logDateGroupLabel(yesterday, now) {
			t.Fatal("stack frame was separated from its error's date group")
		}
	}
	want := []string{logDateGroupLabel(now.AddDate(0, 0, -2), now), logDateGroupLabel(yesterday, now), logDateGroupLabel(now, now)}
	if strings.Join(labels, "|") != strings.Join(want, "|") {
		t.Fatalf("date groups = %q, want %q", labels, want)
	}
	for _, label := range want {
		if !strings.Contains(strings.Join(text, "\n"), label) {
			t.Fatalf("missing date separator: %s", label)
		}
	}
	// The cache must refresh relative labels when the terminal's date changes.
	rows = m.errorLogRows(now.AddDate(0, 0, 1))
	if rows[len(rows)-1].groupLabel != logDateGroupLabel(now, now.AddDate(0, 0, 1)) {
		t.Fatal("cached relative date label did not refresh on the next day")
	}
}

func TestF3FullStackScrollResizeAndStickyDate(t *testing.T) {
	m := workflowModel()
	m.errorLogs = errorLogsState{open: true, pod: "p", generation: 1, layout: &errorLogsLayout{}}
	block := errorTestBlock("p", "api", "ERR "+strings.Repeat("x", 65*1024)+" LARGE_PAYLOAD_END", time.Now())
	for i := 0; i < 1200; i++ {
		block = append(block, core.LogEvent{Pod: "p", Container: "api", Message: fmt.Sprintf("   at Frame%d()", i)})
	}
	block = append(block, core.LogEvent{Pod: "p", Container: "api", Message: "SQL_END_3980"})
	updated, _ := m.Update(errorLogsMsg{pod: "p", generation: 1, blocks: [][]core.LogEvent{block}})
	m = updated.(model)
	if !strings.Contains(m.errorLogs.text, "LARGE_PAYLOAD_END") || !strings.Contains(m.errorLogs.text, "Frame1199()") {
		t.Fatal("error history was truncated")
	}
	for _, width := range []int{120, 44, 20} {
		updated, _ = m.Update(tea.WindowSizeMsg{Width: width, Height: 12})
		m = updated.(model)
		updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnd})
		m = updated.(model)
		view := m.View()
		if !strings.Contains(view, "SQL_END_3980") || !strings.Contains(view, "Today") {
			t.Fatalf("last error line or sticky date inaccessible at width %d:\n%s", width, view)
		}
		for _, line := range strings.Split(view, "\n") {
			if lipgloss.Width(line) > width {
				t.Fatalf("width %d overflow: %q", width, line)
			}
		}
		if len(strings.Split(view, "\n")) > m.height {
			t.Fatal("full error view exceeds terminal height")
		}
	}
}

func TestF3CancelsReloadAndIgnoresLateResults(t *testing.T) {
	m := workflowModel()
	m.items = []core.InventoryItem{{Pod: "p", Container: "api"}}
	var captured context.Context
	m.config.ErrorLogs = func(ctx context.Context, _ string) ([][]core.LogEvent, error) {
		captured = ctx
		return nil, nil
	}
	cmd := m.openErrorLogs("")
	msg := cmd().(errorLogsMsg)
	oldGeneration := msg.generation
	// Test reload replaces the active request and keeps the existing raw text.
	updated, _ := m.Update(errorLogsMsg{pod: "p", generation: oldGeneration, blocks: [][]core.LogEvent{errorTestBlock("p", "api", "ERR saved", time.Now())}})
	m = updated.(model)
	updated, reload := m.Update(monitorKey('r'))
	m = updated.(model)
	if captured.Err() == nil || !m.errorLogs.loading || !strings.Contains(m.errorLogs.text, "ERR saved") {
		t.Fatal("reload must cancel old request and retain its displayed snapshot")
	}
	updated, _ = m.Update(errorLogsMsg{pod: "p", generation: oldGeneration, blocks: [][]core.LogEvent{errorTestBlock("p", "api", "ERR stale", time.Now())}})
	m = updated.(model)
	if strings.Contains(m.errorLogs.text, "stale") || !m.errorLogs.loading {
		t.Fatal("late result replaced the new reload")
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(model)
	if m.errorLogs.open || reload() != nil {
		t.Fatal("closing error history must cancel its pending fetch")
	}
	updated, _ = m.Update(msg)
	if updated.(model).errorLogs.open {
		t.Fatal("closed error history reopened on a late result")
	}
}

func TestF3ReportsPartialFailedAndEmptyResults(t *testing.T) {
	m := workflowModel()
	m.errorLogs = errorLogsState{open: true, pod: "p", generation: 1, layout: &errorLogsLayout{}}
	updated, _ := m.Update(errorLogsMsg{pod: "p", generation: 1, blocks: [][]core.LogEvent{errorTestBlock("p", "api", "ERR partial", time.Now())}, err: errors.New("sidecar denied")})
	m = updated.(model)
	if !strings.Contains(m.errorLogs.text, "partial") || !strings.Contains(m.errorLogs.notice, "sidecar denied") {
		t.Fatal("partial errors and failed collection must both stay visible")
	}
	updated, _ = m.Update(errorLogsMsg{pod: "p", generation: 1, err: errors.New("all denied")})
	m = updated.(model)
	if !strings.Contains(m.errorLogs.notice, "previous snapshot") || !strings.Contains(m.errorLogs.text, "partial") {
		t.Fatal("failed reload must identify and preserve its previous snapshot")
	}
	updated, _ = m.Update(errorLogsMsg{pod: "p", generation: 1})
	m = updated.(model)
	if m.errorLogs.entries != 0 || !strings.Contains(m.errorLogs.text, "No ERR or FTL") || m.errorLogs.notice != "" {
		t.Fatal("successful empty result must clear stale errors and failure status")
	}
	noSource := workflowModel()
	if cmd := noSource.openErrorLogs(""); cmd != nil || !strings.Contains(noSource.errorLogs.notice, "No pod") {
		t.Fatal("unavailable source must be explicit")
	}
}

func TestF3ClosingCancelsActiveFetch(t *testing.T) {
	m := workflowModel()
	m.items = []core.InventoryItem{{Pod: "p", Container: "api"}}
	started := make(chan struct{})
	m.config.ErrorLogs = func(ctx context.Context, _ string) ([][]core.LogEvent, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	cmd := m.openErrorLogs("")
	finished := make(chan tea.Msg, 1)
	go func() { finished <- cmd() }()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("error fetch never started")
	}
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if updated.(model).errorLogs.open {
		t.Fatal("Esc did not close error view")
	}
	select {
	case msg := <-finished:
		if msg != nil {
			t.Fatal("cancelled fetch must not deliver results")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("closing view did not cancel active fetch")
	}
}

func TestF3CopiesEveryOffscreenError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake clipboard uses a POSIX shell")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "clipboard")
	t.Setenv("TAILG_ERRORS_CLIPBOARD", path)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	name := "wl-copy"
	if runtime.GOOS == "darwin" {
		name = "pbcopy"
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\ncat > \"$TAILG_ERRORS_CLIPBOARD\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	m := workflowModel()
	m.errorLogs = errorLogsState{open: true, pod: "p", generation: 1, layout: &errorLogsLayout{}}
	var blocks [][]core.LogEvent
	for i := 0; i < 2000; i++ {
		blocks = append(blocks, errorTestBlock("p", "api", fmt.Sprintf("ERR error_%d", i), time.Now()))
	}
	updated, _ := m.Update(errorLogsMsg{pod: "p", generation: 1, blocks: blocks})
	m = updated.(model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !updated.(model).errorLogs.open {
		t.Fatal("copy should keep the error results open")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != m.errorLogs.text || !strings.Contains(string(data), "ERR error_1999") || !strings.Contains(string(data), "\n\n[p/api]") {
		t.Fatalf("copy lost offscreen errors or entry separation: %v", err)
	}
}
