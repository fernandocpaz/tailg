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

func TestErrorInspectorLoadsAndCopiesEntireBlock(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake clipboard uses a POSIX shell")
	}
	m := workflowModel()
	m.width, m.height = 60, 12
	root := core.LogEvent{Pod: "patient", Container: "api", ObservedAt: time.Now(), Message: "[10:06:54 ERR] DB query failed"}
	block := []core.LogEvent{root}
	for i := 0; i < 1200; i++ {
		line := root
		line.Message = fmt.Sprintf("   at Frame%d()", i)
		block = append(block, line)
	}
	last := root
	last.Message = "Error Number:3980,State:1,Class:16"
	block = append(block, last)
	m.config.LogDetails = func(_ context.Context, selected core.LogEvent) ([]core.LogEvent, error) {
		if selected.Message != root.Message || selected.Pod != root.Pod || selected.Container != root.Container {
			t.Fatal("details loaded from the wrong source")
		}
		return block, nil
	}
	updated, _ := m.Update(logMsg(root))
	m = updated.(model)
	m.state.SetFilter("level:error")
	m.state.SetMatchesOnly(true)
	m.selected = 0
	updated, command := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(model)
	if command == nil || !m.detailLoading {
		t.Fatal("opening an error did not schedule full source retrieval")
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(model)
	if m.detail == "" {
		t.Fatal("copy closed the inspector before the full error loaded")
	}
	updated, _ = m.Update(command())
	m = updated.(model)
	if m.detailLoading || !strings.Contains(m.detail, "Frame1199()") || !strings.Contains(m.detail, last.Message) {
		t.Fatal("full stack trace was not retained")
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnd})
	m = updated.(model)
	if !strings.Contains(m.View(), last.Message) || m.detailOffset == 0 {
		t.Fatal("final error details are not reachable with End")
	}
	updated, _ = m.Update(tea.WindowSizeMsg{Width: 44, Height: 10})
	m = updated.(model)
	for _, row := range strings.Split(m.View(), "\n") {
		if lipgloss.Width(row) > m.width {
			t.Fatalf("detail overflow after resize: %q", row)
		}
	}

	dir := t.TempDir()
	clipboard := filepath.Join(dir, "clipboard")
	t.Setenv("TAILG_TEST_CLIPBOARD", clipboard)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	name := "wl-copy"
	if runtime.GOOS == "darwin" {
		name = "pbcopy"
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\ncat > \"$TAILG_TEST_CLIPBOARD\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	want := m.detail
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(model)
	got, err := os.ReadFile(clipboard)
	if err != nil || string(got) != want || !strings.Contains(string(got), "Frame0()") || !strings.Contains(string(got), last.Message) {
		t.Fatalf("copy lost part of the error: bytes=%d, want=%d, err=%v", len(got), len(want), err)
	}
}

func TestErrorInspectorPreservesBufferedTextOnFailureAndRejectsStaleResult(t *testing.T) {
	m := workflowModel()
	record := core.LogRecord{Event: core.LogEvent{Pod: "p", Container: "c", Message: "[10:00:00 ERR] first"}}
	m.config.LogDetails = func(context.Context, core.LogEvent) ([]core.LogEvent, error) {
		return nil, errors.New("logs rotated")
	}
	cmd := m.openRecordDetail(record)
	oldGeneration := m.detailGen
	updated, _ := m.Update(cmd())
	m = updated.(model)
	if !strings.Contains(m.detail, record.Event.Message) || !strings.Contains(m.detailNotice, "logs rotated") || m.detailLoading {
		t.Fatal("failed retrieval hid buffered text or its warning")
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(model)
	stale := logDetailMsg{generation: oldGeneration, events: []core.LogEvent{record.Event}}
	updated, _ = m.Update(stale)
	m = updated.(model)
	if m.detail != "" {
		t.Fatal("late result reopened a closed inspector")
	}
	record.Event.Message = "[10:00:01 ERR] second"
	m.openRecordDetail(record)
	updated, _ = m.Update(stale)
	m = updated.(model)
	if !strings.Contains(m.detail, "second") || strings.Contains(m.detail, "first") {
		t.Fatal("stale result replaced a different selected error")
	}
	m.closeDetail()
}

func TestErrorInspectorKeepsItsTraceWhileLiveSelectionMoves(t *testing.T) {
	m := workflowModel()
	record := core.RecordsForEvent(m.config.Formatter, core.LogEvent{Pod: "p", Container: "c", Message: "[10:00:00 ERR] [" + workflowTrace + "] first"}, false)[0]
	m.state.AppendRecords(record)
	m.openRecordDetail(record)
	updated, _ := m.Update(logMsg(core.LogEvent{Pod: "other", Message: "[10:00:01 INF] unrelated"}))
	m = updated.(model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyF6})
	m = updated.(model)
	if !m.traceOpen || m.traceID != workflowTrace {
		t.Fatal("inspected error's trace changed with live selection")
	}
}

func TestDetailLayoutPreservesHugeUnbrokenTextAndCachesWrapping(t *testing.T) {
	m := workflowModel()
	m.width, m.height = 57, 10
	message := strings.Repeat("x", 5*1024*1024) + "END_OF_ERROR"
	m.openDetail(message)
	lines := m.detailLines()
	if strings.Join(lines, "") != message {
		t.Fatal("wrapping lost content from a multi-megabyte unbroken message")
	}
	if &m.detailLines()[0] != &lines[0] {
		t.Fatal("scrolling rewrapped the entire large entry")
	}
	m.updateDetailScroll("end")
	if !strings.Contains(m.View(), "END_OF_ERROR") {
		t.Fatal("end of multi-megabyte error is not reachable")
	}
	// Exercise widths measured in terminal cells, including ANSI and tabs.
	m.openDetail("\x1b[31m" + strings.Repeat("例外🙂\t", 120) + "\x1b[0m")
	for _, width := range []int{10, 31, 80} {
		m.width = width
		for _, line := range m.detailLines() {
			if lipgloss.Width(line) > width {
				t.Fatalf("wrapped Unicode exceeds %d cells: %q", width, line)
			}
		}
	}
}
