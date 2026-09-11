package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/fernandocpaz/tailg/internal/core"
)

type traceMsg struct {
	generation int
	id         string
	records    []core.LogRecord
	err        error
}

func (m model) hasSearch() bool { return m.config.Search != nil || m.config.SearchRecords != nil }

func (m *model) openSelectedTrace() tea.Cmd {
	record, ok := m.state.SelectedRecord(m.selected)
	if !ok || record.Fields.TraceID == "" {
		m.notice = "Selected log has no valid trace ID"
		return nil
	}
	return m.openTrace(record.Fields.TraceID)
}

func (m *model) openTrace(id string) tea.Cmd {
	m.closeTrace()
	m.traceOpen = true
	m.traceID = id
	m.traceIndex = 0
	m.detail = ""
	m.issueOpen = false
	m.traceNotice = ""
	limit := m.config.BufferLines
	if limit <= 0 {
		limit = core.DefaultBufferLines
	}
	m.traceState = core.NewFilterState(limit)
	m.traceState.SetFilter("trace:" + id)
	m.traceState.SetMatchesOnly(true)
	// Reconcile live and visible history by occurrence, preserving repeated
	// messages and rows whose display text hides different trace metadata.
	counts := make(map[traceOccurrence]int)
	for _, record := range m.state.AllRecords() {
		if record.Fields.TraceID == id {
			m.traceState.AppendRecords(record)
			counts[traceKey(record)]++
		}
	}
	for _, record := range m.state.Records() {
		if record.Fields.TraceID != id {
			continue
		}
		key := traceKey(record)
		if counts[key] > 0 {
			counts[key]--
		} else {
			m.traceState.AppendRecords(record)
		}
	}
	return m.loadTrace()
}

func (m *model) closeTrace() {
	if m.traceSearches != nil {
		m.traceSearches.stop()
	}
	m.traceGeneration++
	m.traceOpen = false
	m.traceLoading = false
}

func (m *model) loadTrace() tea.Cmd {
	if m.config.Trace == nil {
		m.traceNotice = "Showing logs already in this view"
		return nil
	}
	if m.traceSearches == nil {
		m.traceSearches = &searchController{}
	}
	parent := m.ctx
	if parent == nil {
		parent = context.Background()
	}
	ctx := m.traceSearches.start(parent)
	m.traceGeneration++
	generation, id := m.traceGeneration, m.traceID
	m.traceLoading = true
	return func() tea.Msg {
		limited, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		records, err := m.config.Trace(limited, id)
		if ctx.Err() != nil {
			return nil
		}
		return traceMsg{generation: generation, id: id, records: records, err: err}
	}
}

func (m model) updateTraceResult(msg traceMsg) (tea.Model, tea.Cmd) {
	if !m.traceOpen || msg.generation != m.traceGeneration || msg.id != m.traceID {
		return m, nil
	}
	m.traceLoading = false
	m.traceNotice = ""
	if msg.err != nil {
		m.traceNotice = "Incomplete results: " + msg.err.Error()
	}
	if msg.err == nil || len(msg.records) > 0 {
		m.traceState.SetSearchRecords("trace:"+m.traceID, msg.records)
	}
	m.traceIndex = min(m.traceIndex, max(0, len(m.traceRows())-1))
	return m, nil
}

func (m model) traceRows() []core.LogRecord {
	if m.traceState == nil {
		return nil
	}
	rows := m.traceState.Records()
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i].Event.ObservedAt, rows[j].Event.ObservedAt
		if a.IsZero() != b.IsZero() {
			return !a.IsZero()
		}
		if a.Equal(b) {
			return rows[i].ID < rows[j].ID
		}
		return a.Before(b)
	})
	return rows
}

func (m model) renderTrace() string {
	rows := m.traceRows()
	services := make(map[string]bool)
	for _, record := range rows {
		services[record.Fields.Service] = true
	}
	header := fmt.Sprintf("Request %s | %d rows, %d services", m.traceID, len(rows), len(services))
	status := "Timeline of retained logs from the selected workloads"
	if m.traceLoading {
		status = "Loading related logs across selected pods..."
	} else if m.traceNotice != "" {
		status = m.traceNotice
	}
	height := max(1, m.height-4)
	selected := max(0, min(m.traceIndex, len(rows)-1))
	start := max(0, selected-height+1)
	lines := []string{truncatePlain(status, m.width)}
	for i := start; i < min(len(rows), start+height); i++ {
		record := rows[i]
		marker := "  "
		if i == selected {
			marker = "> "
		}
		at := "--:--:--.---"
		if !record.Event.ObservedAt.IsZero() {
			at = record.Event.ObservedAt.Local().Format("15:04:05.000")
		}
		level := record.Fields.Level
		if core.IsSlowRequest(record.Fields) {
			level += "/SLOW"
		}
		source := record.Event.Pod + "/" + record.Event.Container
		prefix := marker + at + " " + level + " " + truncatePlain(source, max(12, m.width/3)) + " "
		message := parseLogColumns(record.Text, m.config.Formatter.ShowPod).message
		line := prefix + message
		if core.IsSlowRequest(record.Fields) {
			line += fmt.Sprintf(" (%s)", record.Fields.Duration)
		}
		lines = append(lines, truncatePlain(line, m.width))
	}
	if len(rows) == 0 {
		lines = append(lines, "No matching logs available yet")
	}
	return m.panel(header, strings.Join(lines, "\n"), "Up/Down select | Enter raw details | R reload | F6/Esc closes")
}

func (m model) updateTraceKey(key string) (tea.Model, tea.Cmd) {
	if m.detail != "" {
		if key == "enter" {
			m.notice = copyText(m.detail)
		} else if key == "f6" {
			m.detail = ""
			m.closeTrace()
		}
		return m, nil
	}
	rows := m.traceRows()
	switch key {
	case "f6":
		m.closeTrace()
	case "up":
		m.traceIndex--
	case "down":
		m.traceIndex++
	case "pgup":
		m.traceIndex -= max(1, m.height-4)
	case "pgdown":
		m.traceIndex += max(1, m.height-4)
	case "r":
		return m, m.loadTrace()
	case "enter":
		if len(rows) > 0 {
			m.detail = recordDetails(rows[max(0, min(m.traceIndex, len(rows)-1))])
		}
	}
	m.traceIndex = max(0, min(m.traceIndex, len(rows)-1))
	return m, nil
}

func recordDetails(record core.LogRecord) string {
	fields := record.Fields
	lines := []string{"Pod: " + record.Event.Pod + " | Container: " + record.Event.Container}
	if !record.Event.ObservedAt.IsZero() {
		lines = append(lines, "Timestamp: "+record.Event.ObservedAt.Format(time.RFC3339Nano))
	}
	if fields.TraceID != "" {
		lines = append(lines, "Trace: "+fields.TraceID+" | Span: "+fields.SpanID)
	}
	if fields.HasDuration {
		lines = append(lines, "Duration: "+fields.Duration.String())
	}
	lines = append(lines, "", "Original log:", record.Event.Message)
	return strings.Join(lines, "\n")
}

func renderRecordRow(record core.LogRecord, query string, selected bool, width int, showPod, color bool) string {
	if core.IsSlowRequest(record.Fields) && width >= 30 {
		row := renderLogRow(record.Text, query, selected, width-6, showPod, color)
		return truncate(row+renderWithColor(warnStyle, " SLOW", color), width)
	}
	return renderLogRow(record.Text, query, selected, width, showPod, color)
}

type traceOccurrence struct {
	pod, container, raw, text string
	at                        time.Time
}

func traceKey(record core.LogRecord) traceOccurrence {
	return traceOccurrence{record.Event.Pod, record.Event.Container, record.Event.Message, record.Text, record.Event.ObservedAt.UTC()}
}
