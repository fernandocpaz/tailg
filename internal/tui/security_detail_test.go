package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/fernandocpaz/tailg/internal/core"
)

func securityKey(key rune) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{key}}
}

func TestSecurityShortcutOpensSelectedLogWithoutChangingOriginal(t *testing.T) {
	m := workflowModel()
	event := core.LogEvent{Pod: "api-pod", Container: "api", Message: "GET /.env returned 404"}
	record := core.LogRecord{Event: event, Fields: core.LogFields{Method: "GET", Path: "/.env", StatusCode: 404}}
	m.state.AppendRecords(record)
	m.selected = 0
	updated, _ := m.Update(securityKey('S'))
	m = updated.(model)
	if !m.securityView || !strings.Contains(m.securityText, "Sensitive endpoint reconnaissance") {
		t.Fatalf("S did not open security assessment: %q", m.securityText)
	}
	if !strings.Contains(m.View(), "Security assessment") {
		t.Fatal("security view is not rendered")
	}
	if !strings.Contains(m.detail, event.Message) {
		t.Fatal("inspector replaced the underlying original log")
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(model)
	if m.securityView || m.detail == "" {
		t.Fatal("first Esc should return to original log detail")
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(model)
	if m.detail != "" {
		t.Fatal("second Esc should close original detail")
	}
}

func TestSecurityShortcutInsideLogDetail(t *testing.T) {
	m := workflowModel()
	m.openRecordDetail(core.LogRecord{
		Event: core.LogEvent{Pod: "api", Container: "api", Message: "GET /files/../secret 404"},
		Fields: core.LogFields{Method: "GET", Path: "/files/../secret", StatusCode: 404},
	})
	updated, _ := m.Update(securityKey('s'))
	m = updated.(model)
	if !m.securityView || !strings.Contains(m.securityText, "Path traversal") {
		t.Fatalf("detail S failed: %q", m.securityText)
	}
	updated, _ = m.Update(securityKey('S'))
	m = updated.(model)
	if m.securityView || m.detail == "" {
		t.Fatal("S in security view should return to original log")
	}
}
