package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/fernandocpaz/tailg/internal/core"
)

type errorLogsState struct {
	open       bool
	pod        string
	text       string
	entries    int
	loading    bool
	notice     string
	offset     int
	generation int
	searches   *searchController
	blocks     [][]core.LogEvent
	layout     *errorLogsLayout
}

type errorLogsLayout struct {
	width int
	color bool
	day   string
	rows  []logTimelineRow
}

type errorLogsMsg struct {
	generation int
	pod        string
	blocks     [][]core.LogEvent
	err        error
}

func (m *model) openErrorLogs(pod string) tea.Cmd {
	if pod == "" {
		if m.state != nil {
			if record, ok := m.state.SelectedRecord(m.selected); ok {
				pod = record.Event.Pod
			}
		}
		if pod == "" && len(m.items) > 0 {
			pod = m.items[0].Pod
		}
	}
	m.closeErrorLogs()
	m.errorLogs.open = true
	m.errorLogs.pod = pod
	m.errorLogs.text = ""
	m.errorLogs.entries = 0
	m.errorLogs.offset = 0
	m.errorLogs.blocks = nil
	m.errorLogs.layout = &errorLogsLayout{}
	m.issueOpen = false
	return m.loadErrorLogs()
}

func (m *model) closeErrorLogs() {
	if m.errorLogs.searches != nil {
		m.errorLogs.searches.stop()
	}
	m.errorLogs.generation++
	m.errorLogs.open = false
	m.errorLogs.loading = false
	m.errorLogs.text = ""
	m.errorLogs.blocks = nil
	m.errorLogs.layout = nil
	m.errorLogs.entries = 0
	m.errorLogs.offset = 0
	m.errorLogs.notice = ""
}

func (m *model) loadErrorLogs() tea.Cmd {
	view := &m.errorLogs
	view.notice = ""
	if view.pod == "" || m.config.ErrorLogs == nil {
		view.notice = "No pod error-log source is available"
		return nil
	}
	if view.searches == nil {
		view.searches = &searchController{}
	}
	parent := m.ctx
	if parent == nil {
		parent = context.Background()
	}
	ctx := view.searches.start(parent)
	view.generation++
	view.loading = true
	generation, pod, load := view.generation, view.pod, m.config.ErrorLogs
	return func() tea.Msg {
		limited, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		blocks, err := load(limited, pod)
		if ctx.Err() != nil {
			return nil
		}
		return errorLogsMsg{generation: generation, pod: pod, blocks: blocks, err: err}
	}
}

func (m model) updateErrorLogs(msg errorLogsMsg) (tea.Model, tea.Cmd) {
	view := &m.errorLogs
	if !view.open || msg.generation != view.generation || msg.pod != view.pod {
		return m, nil
	}
	view.loading = false
	view.notice = ""
	if msg.err != nil {
		view.notice = "Could not load complete error logs: " + msg.err.Error() + " | R retries"
	}
	if msg.err == nil || len(msg.blocks) > 0 {
		var text strings.Builder
		entries := 0
		for _, block := range msg.blocks {
			if len(block) == 0 {
				continue
			}
			if entries > 0 {
				text.WriteString("\n\n")
			}
			text.WriteString(errorBlockText(block))
			entries++
		}
		view.text, view.entries = text.String(), entries
		view.blocks = msg.blocks
		view.layout = &errorLogsLayout{}
		view.offset = 0
		if entries == 0 {
			view.text = "No ERR or FTL entries in this pod's retained logs"
		}
	} else if view.text != "" {
		view.notice += " | Showing the previous snapshot"
	}
	return m, nil
}

func errorBlockText(block []core.LogEvent) string {
	root := block[0]
	var text strings.Builder
	fmt.Fprintf(&text, "[%s/%s]", root.Pod, root.Container)
	if !root.ObservedAt.IsZero() {
		fmt.Fprintf(&text, " %s", root.ObservedAt.Format(time.RFC3339Nano))
	}
	for _, event := range block {
		text.WriteByte('\n')
		text.WriteString(event.Message)
	}
	return text.String()
}

// Cache wrapped raw text so scrolling large errors does not rewrap the entire
// history. Date labels refresh when the local calendar day changes.
func (m model) errorLogRows(now time.Time) []logTimelineRow {
	view := m.errorLogs
	cache := view.layout
	if cache == nil {
		cache = &errorLogsLayout{}
	}
	width, color, day := max(1, m.width), m.config.Formatter.Color, localDateKey(now, now.Location())
	if cache.rows == nil || cache.width != width || cache.color != color || cache.day != day {
		var rows []logTimelineRow
		lastDate := ""
		for _, block := range view.blocks {
			if len(block) == 0 {
				continue
			}
			root := block[0]
			label := logDateGroupLabel(root.ObservedAt, now)
			date := localDateKey(root.ObservedAt, now.Location())
			if label != "" && date != lastDate {
				separator := renderLogDateGroupRow(label, width, color)
				for _, line := range strings.Split(ansi.Hardwrap(separator, width, true), "\n") {
					rows = append(rows, logTimelineRow{text: line, groupLabel: label, separator: true})
				}
				lastDate = date
			}
			text := errorBlockText(block)
			for _, line := range wrapDetailText(nil, text, "", width) {
				rows = append(rows, logTimelineRow{text: line, groupLabel: label})
			}
			rows = append(rows, logTimelineRow{groupLabel: label})
		}
		if len(rows) > 0 {
			rows = rows[:len(rows)-1] // remove the extra gap after the final entry
		}
		if len(rows) == 0 {
			for _, line := range wrapDetailText(nil, view.text, "", width) {
				rows = append(rows, logTimelineRow{text: line})
			}
		}
		cache.width, cache.color, cache.day, cache.rows = width, color, day, rows
	}
	status := view.notice
	if view.loading {
		status = "Loading all retained ERR / FTL logs from this pod..."
	}
	if status == "" {
		return cache.rows
	}
	var rows []logTimelineRow
	for _, line := range wrapDetailText(nil, status, "", width) {
		rows = append(rows, logTimelineRow{text: line})
	}
	rows = append(rows, logTimelineRow{})
	return append(rows, cache.rows...)
}

func (m model) stickyErrorDate(rows []logTimelineRow, start, height int) []string {
	if height <= 1 || start <= 0 || rows[start].separator || rows[start].groupLabel == "" {
		return nil
	}
	label := renderLogDateGroupRow(rows[start].groupLabel, m.width, m.config.Formatter.Color)
	sticky := strings.Split(ansi.Hardwrap(label, max(1, m.width), true), "\n")
	return sticky[:min(len(sticky), height-1)]
}

func (m model) errorLogsScrollLimit(rows []logTimelineRow) int {
	height := m.errorLogsHeight()
	start := max(0, len(rows)-height)
	for start < len(rows)-1 && start+height-len(m.stickyErrorDate(rows, start, height)) < len(rows) {
		start++
	}
	return start
}

func (m model) errorLogsFooter() []string {
	footer := "Up/Down PgUp/PgDn Home/End scroll | R reload | Enter copy all | F3/Esc close"
	return strings.Split(ansi.Hardwrap(ansi.Wrap(footer, max(1, m.width), ""), max(1, m.width), true), "\n")
}

func (m model) errorLogsHeight() int {
	return max(1, m.height-2-min(len(m.errorLogsFooter()), max(1, m.height-3)))
}

func (m model) renderErrorLogs() string {
	view := m.errorLogs
	rows := m.errorLogRows(time.Now())
	height := m.errorLogsHeight()
	start := min(max(0, view.offset), m.errorLogsScrollLimit(rows))
	var lines []string
	available := height
	sticky := m.stickyErrorDate(rows, start, height)
	lines = append(lines, sticky...)
	available -= len(sticky)
	end := min(len(rows), start+available)
	for _, row := range rows[start:end] {
		lines = append(lines, row.text)
	}
	for len(lines) < height {
		lines = append(lines, "")
	}
	header := fmt.Sprintf("Error logs | %s | %d entries | %d-%d/%d lines", view.pod, view.entries, start+1, end, len(rows))
	footer := m.errorLogsFooter()
	footer = footer[:min(len(footer), max(1, m.height-3))]
	return strings.Join([]string{
		truncate(renderWithColor(headerStyle, header, m.config.Formatter.Color), m.width),
		renderRule(m.width, m.config.Formatter.Color),
		strings.Join(lines, "\n"),
		renderWithColor(dimStyle, strings.Join(footer, "\n"), m.config.Formatter.Color),
	}, "\n")
}

func (m model) updateErrorLogsKey(key string) (tea.Model, tea.Cmd) {
	view := &m.errorLogs
	limit := m.errorLogsScrollLimit(m.errorLogRows(time.Now()))
	step := max(1, m.errorLogsHeight()-1)
	switch key {
	case "f3", "esc":
		m.closeErrorLogs()
	case "r":
		return m, m.loadErrorLogs()
	case "enter":
		if !view.loading && view.text != "" {
			view.notice = copyText(view.text)
		}
	case "up", "k":
		view.offset--
	case "down", "j":
		view.offset++
	case "pgup":
		view.offset -= step
	case "pgdown":
		view.offset += step
	case "home":
		view.offset = 0
	case "end":
		view.offset = limit
	}
	view.offset = min(max(0, view.offset), limit)
	return m, nil
}
