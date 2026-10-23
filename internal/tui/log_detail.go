package tui

import (
	"context"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/fernandocpaz/tailg/internal/core"
)

type logDetailMsg struct {
	generation int
	events     []core.LogEvent
	err        error
}

// Wrapping a multi-megabyte entry on every key press makes scrolling unusable.
// Reuse the layout until the text, status or terminal width changes.
type detailLayout struct {
	text, status string
	width        int
	lines        []string
}

func (m *model) openRecordDetail(record core.LogRecord) tea.Cmd {
	block := []core.LogEvent{record.Event}
	if m.state != nil {
		block = m.state.SelectedBlock(record)
	}
	m.openDetail(logBlockDetails(block))
	m.detailRecord = &record
	return m.loadLogDetail()
}

func logBlockDetails(block []core.LogEvent) string {
	root := block[0]
	record := core.LogRecord{Event: root, Fields: core.ParseLogFields(root)}
	messages := make([]string, len(block))
	for i, event := range block {
		messages[i] = event.Message
	}
	record.Event.Message = strings.Join(messages, "\n")
	return recordDetails(record)
}

func (m *model) loadLogDetail() tea.Cmd {
	if m.detailRecord == nil || m.config.LogDetails == nil || m.detailRecord.Event.Pod == "" {
		return nil
	}
	if m.detailSearches == nil {
		m.detailSearches = &searchController{}
	}
	parent := m.ctx
	if parent == nil {
		parent = context.Background()
	}
	ctx := m.detailSearches.start(parent)
	m.detailGen++
	generation, selected := m.detailGen, m.detailRecord.Event
	m.detailLoading, m.detailNotice = true, ""
	load := m.config.LogDetails
	return func() tea.Msg {
		limited, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		events, err := load(limited, selected)
		if ctx.Err() != nil {
			return nil
		}
		return logDetailMsg{generation: generation, events: events, err: err}
	}
}

func (m model) updateLogDetail(msg logDetailMsg) (tea.Model, tea.Cmd) {
	if m.detail == "" || msg.generation != m.detailGen {
		return m, nil
	}
	m.detailLoading = false
	if msg.err != nil {
		m.detailNotice = "Could not load the full entry: " + msg.err.Error() + ". Showing buffered text; R retries."
		return m, nil
	}
	if len(msg.events) == 0 {
		m.detailNotice = "No retained entry returned. Showing buffered text; R retries."
		return m, nil
	}
	m.detailNotice = ""
	m.detail = logBlockDetails(msg.events)
	root := msg.events[0]
	m.detailRecord = &core.LogRecord{Event: root, Fields: core.ParseLogFields(root)}
	return m, nil
}

func (m model) updateLogDetailKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "f6":
		if m.traceOpen {
			m.closeDetail()
			m.closeTrace()
			return m, nil
		}
		cmd := m.openSelectedTrace()
		return m, cmd
	case "r":
		if m.aiView { return m, nil }
		cmd := m.loadLogDetail()
		return m, cmd
	case "a", "A":
		return m.toggleAIExplanation()
	case "s", "S":
		return m.openAIWizard("")
	case "p", "P":
		if m.config.AI==nil{return m.openAIWizard("Configure an AI provider before mapping repositories.")}
		service:=m.selectedAIService()
		if service=="" {m.notice="Cannot identify a service for this log entry";return m,nil}
		return m.openServiceRepoWizard(service,"Choose or change this service's source repository.")
	case "enter":
		if m.aiView {
			if !m.aiLoading {
				m.notice = copyText(m.aiText)
				m.closeDetail()
			}
		} else if !m.detailLoading {
			m.notice = copyText(m.detail)
			m.closeDetail()
		}
	default:
		m.updateDetailScroll(key)
	}
	return m, nil
}

func (m model) wrappedDetailLines() []string {
	if m.aiView {
		status := ""
		if m.aiLoading { status = "Analyzing selected error with the configured AI provider..." }
		text := m.aiText
		if text == "" { text = "AI analysis requested; only redacted error text and matched excerpts from configured repositories are sent." }
		return wrapDetailText(m.detailLayout, text, status, m.width)
	}
	status := m.detailNotice
	if m.detailLoading {
		status = "Loading the full entry from this pod..."
	}
	return wrapDetailText(m.detailLayout, m.detail, status, m.width)
}

func wrapDetailText(cache *detailLayout, text, status string, width int) []string {
	width = max(1, width)
	if cache == nil {
		cache = &detailLayout{}
	}
	if cache.text != text || cache.width != width || cache.status != status {
		value := text
		if status != "" {
			value = status + "\n\n" + value
		}
		// Tabs must occupy known cells before wrapping; keep the copied original
		// untouched. Hard wrapping is the final guard for long tokens/Unicode.
		value = strings.ReplaceAll(value, "\t", "    ")
		wrapped := ansi.Hardwrap(ansi.Wrap(value, width, ""), width, true)
		cache.text, cache.status, cache.width = text, status, width
		cache.lines = strings.Split(wrapped, "\n")
	}
	return cache.lines
}
