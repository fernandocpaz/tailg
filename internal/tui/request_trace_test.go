package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/fernandocpaz/tailg/internal/core"
)

const workflowTrace = "11d7729cbf34122f3af8d3c73a47213d"

func workflowModel() model {
	input := textinput.New()
	input.Focus()
	return model{ctx: context.Background(), input: input, state: core.NewFilterState(100), heartbeat: &core.HeartbeatAnalyzer{},
		width: 120, height: 12, followsLive: true, selected: -1,
		config: Config{Formatter: core.Formatter{ShowPod: true}, BufferLines: 100}}
}

func TestRequestInvestigationPreservesRawFieldsAndCorrelatesPods(t *testing.T) {
	m := workflowModel()
	now := time.Now().Add(-time.Second)
	events := []core.LogEvent{
		{Pod: "api-1", Container: "api", ObservedAt: now, Message: `[20:00:00 INF] [11d7729cbf34122f3af8d3c73a47213d] HTTP GET /orders responded 200 in 350.5 ms {"RequestId":"request-1"}`},
		{Pod: "patient-1", Container: "patient", ObservedAt: now.Add(10 * time.Millisecond), Message: `{"level":"INFO","message":"patient fetched","TraceId":"11d7729cbf34122f3af8d3c73a47213d"}`},
		{Pod: "worker-1", Container: "worker", ObservedAt: now.Add(20 * time.Millisecond), Message: `[20:00:00 ERR] listener failed {"traceparent":"00-11d7729cbf34122f3af8d3c73a47213d-b7ad6b7169203331-01"}`},
	}
	for _, event := range events {
		updated, _ := m.Update(logMsg(event))
		m = updated.(model)
	}
	if !strings.Contains(m.View(), "SLOW") {
		t.Fatal("slow successful request has no visible flag")
	}
	selected, _ := m.state.SelectedRecord(m.selected)
	if selected.Fields.TraceID != workflowTrace || !strings.Contains(selected.Event.Message, "traceparent") || strings.Contains(selected.Text, "traceparent") {
		t.Fatal("hidden raw trace metadata was lost or exposed in compact output")
	}
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(model)
	if !strings.Contains(m.detail, "traceparent") {
		t.Fatal("raw inspection omitted hidden properties")
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyF6})
	m = updated.(model)
	if !m.traceOpen || len(m.traceRows()) != 3 {
		t.Fatalf("request view did not collect related pods: open=%t rows=%d", m.traceOpen, len(m.traceRows()))
	}
	for i, row := range m.traceRows() {
		if row.Event.Pod != events[i].Pod {
			t.Fatalf("timeline ordering = %+v", m.traceRows())
		}
	}
	for _, width := range []int{60, 100, 160} {
		m.width = width
		for _, line := range strings.Split(m.renderTrace(), "\n") {
			if lipgloss.Width(line) > width {
				t.Fatalf("trace row exceeded width %d: %q", width, line)
			}
		}
	}
}

func TestTraceCompletionKeepsLocalAndInFlightLogsAndIgnoresClosedView(t *testing.T) {
	m := workflowModel()
	base := time.Now()
	event := core.LogEvent{Pod: "a", Container: "api", ObservedAt: base, Message: `[20:00:00 ERR] [11d7729cbf34122f3af8d3c73a47213d] failed`}
	updated, _ := m.Update(logMsg(event))
	m = updated.(model)
	m.config.Trace = func(context.Context, string) ([]core.LogRecord, error) {
		return nil, errors.New("one pod inaccessible")
	}
	command := m.openSelectedTrace()
	if command == nil {
		t.Fatal("request history was not scheduled")
	}
	late := event
	late.Pod, late.Container, late.ObservedAt = "b", "worker", base.Add(time.Second)
	late.Message = `[20:00:01 INF] [11d7729cbf34122f3af8d3c73a47213d] retrying`
	updated, _ = m.Update(logMsg(late))
	m = updated.(model)
	// A remote snapshot containing only the first row must preserve the new row.
	result := traceMsg{id: workflowTrace, generation: m.traceGeneration, records: core.RecordsForEvent(m.config.Formatter, event, true)}
	updated, _ = m.Update(result)
	m = updated.(model)
	if len(m.traceRows()) != 2 {
		t.Fatalf("snapshot replaced live trace rows: %+v", m.traceRows())
	}
	updated, _ = m.Update(command())
	m = updated.(model)
	if !strings.Contains(m.traceNotice, "Incomplete") || len(m.traceRows()) != 2 {
		t.Fatal("partial failure hid known trace rows or was not reported")
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(model)
	updated, _ = m.Update(result)
	m = updated.(model)
	if m.traceOpen {
		t.Fatal("late response reopened the closed request view")
	}
}

func TestStructuredFilterRejectsInvalidQueriesBeforeSearching(t *testing.T) {
	m := workflowModel()
	called := false
	m.config.SearchRecords = func(context.Context, string) ([]core.LogRecord, error) { called = true; return nil, nil }
	m.state.SetFilter("duration:bogus")
	m.searching = true
	if command := m.searchCommand(1, "duration:bogus"); command != nil || m.searching || called {
		t.Fatal("invalid query scheduled a history search")
	}
	if !strings.Contains(m.renderFooter(), "Invalid filter") {
		t.Fatal("query error is hidden")
	}
}

func TestIssueBaselineAndTraceShortcuts(t *testing.T) {
	m := workflowModel()
	m.issues = core.NewIssueRadar(10)
	now := time.Now()
	m.issues.SetBaseline(now.Add(-time.Minute))
	event := core.LogEvent{Pod: "a", Container: "api", ObservedAt: now, Message: `[20:00:00 ERR] [11d7729cbf34122f3af8d3c73a47213d] failed`}
	m.issues.Observe(event)
	m.state.AppendRecords(core.RecordsForEvent(m.config.Formatter, event, true)...)
	m.issueOpen = true
	if !strings.Contains(m.renderIssueRadar(), "NEW") {
		t.Fatal("new issue is not marked")
	}
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'b'}})
	m = updated.(model)
	if m.issueStats().New != 0 || !strings.Contains(m.renderIssueRadar(), "KNOWN") {
		t.Fatal("baseline did not mark current issues recurring")
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyF6})
	m = updated.(model)
	if !m.traceOpen || m.traceID != workflowTrace {
		t.Fatal("ordinary error could not open its request timeline")
	}
}
