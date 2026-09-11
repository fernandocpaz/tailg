package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/fernandocpaz/tailg/internal/core"
)

func TestIssueRowWrapsCompleteTextAndSource(t *testing.T) {
	summary := "request failed " + strings.Repeat("詳細 context ", 20) + strings.Repeat("a", 160) + " END_OF_ISSUE"
	source := "a-service-name-that-is-longer-than-the-old-column-width"
	for _, width := range []int{30, 60, 100, 160} {
		for _, color := range []bool{false, true} {
			row := renderIssueRow(core.Issue{Severity: core.IssueError, Service: source, Count: 12345678, Summary: "short…", FullSummary: summary, MaxDuration: time.Second, LastSeen: time.Now()}, true, width, color, time.Now())
			var text strings.Builder
			for _, line := range strings.Split(row, "\n") {
				if lipgloss.Width(line) > width {
					t.Fatalf("width %d exceeded: %q", width, line)
				}
				plain := ansi.Strip(line)
				plain = strings.TrimPrefix(strings.TrimPrefix(plain, "> "), "▌ ")
				text.WriteString(plain)
			}
			compact := strings.Join(strings.Fields(text.String()), "")
			if !strings.Contains(compact, strings.Join(strings.Fields(summary), "")) || !strings.Contains(compact, source) || !strings.Contains(compact, "12345678×") || !strings.Contains(compact, "max1s") {
				t.Fatalf("wrapped row lost text at width %d: %s", width, row)
			}
		}
	}
}

func TestIssueRadarPagesThroughAnIssueLongerThanScreen(t *testing.T) {
	m := workflowModel()
	m.width, m.height = 60, 12
	m.issues = core.NewIssueRadar(10)
	m.issueOpen = true
	var message strings.Builder
	message.WriteString("ERROR BEGIN_OF_ISSUE ")
	for i := 0; i < 100; i++ {
		fmt.Fprintf(&message, "context%03d ", i)
	}
	message.WriteString("END_OF_ISSUE")
	event := core.LogEvent{Container: "service", Message: message.String(), ObservedAt: time.Now()}
	m.issues.Observe(event)
	m.issues.Observe(event)
	m.issues.Observe(core.LogEvent{Container: "other", Message: "ERROR second issue", ObservedAt: time.Now()})
	seenEnd := false
	for i := 0; i < 100; i++ {
		view := m.View()
		lines := strings.Split(view, "\n")
		if len(lines) != m.height {
			t.Fatalf("radar height=%d, want %d", len(lines), m.height)
		}
		for _, line := range lines {
			if lipgloss.Width(line) > m.width {
				t.Fatalf("overwide radar line: %q", line)
			}
		}
		if strings.Contains(view, "END_OF_ISSUE") {
			seenEnd = true
			break
		}
		updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
		m = updated.(model)
	}
	if !seenEnd {
		t.Fatal("could not scroll to full issue suffix")
	}
	// Resizing keeps the selected issue readable; navigation resets its line offset.
	m.width = 100
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(model)
	if m.issueLineOffset != 0 || !strings.Contains(m.View(), "second issue") {
		t.Fatal("next issue is not visible after paging")
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m = updated.(model)
	if !strings.Contains(m.View(), "BEGIN_OF_ISSUE") {
		t.Fatal("returning to the long issue did not start at its beginning")
	}
}

func TestLogViewIdentifiesPodFromStructuredRecord(t *testing.T) {
	m := workflowModel()
	m.config.Formatter.ShowPod = true
	event := core.LogEvent{Pod: "order-processing-7c795bb787-abcde", Container: "orders", Message: "[12:00:00 INF] request received"}
	updated, _ := m.Update(logMsg(event))
	m = updated.(model)
	for _, width := range []int{60, 100, 160} {
		m.width = width
		view := m.View()
		if !strings.Contains(view, event.Pod) || !strings.Contains(view, "Container:") || !strings.Contains(view, event.Container) {
			t.Fatalf("source missing at width %d: %s", width, view)
		}
		lines := strings.Split(view, "\n")
		if len(lines) > m.height {
			t.Fatalf("source bar overflowed viewport: %d > %d", len(lines), m.height)
		}
		for _, line := range lines {
			if lipgloss.Width(line) > width {
				t.Fatalf("overwide source view line: %q", line)
			}
		}
		row := renderRecordRow(core.RecordsForEvent(m.config.Formatter, event, true)[0], "", false, width, true, false)
		if !strings.Contains(row, "order") || !strings.Contains(row, "abcde") {
			t.Fatalf("row does not identify workload and replica: %s", row)
		}
	}
}
