package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/fernandocpaz/tailg/internal/kube"
)

func TestRankPickerNamesPinsCurrentThenUsesFrequencyAndRecency(t *testing.T) {
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	usage := map[string]ChoiceUsage{
		"qa":   {Count: 2, LastUsed: now.Add(-time.Hour)},
		"prod": {Count: 5, LastUsed: now.Add(-24 * time.Hour)},
		"dev":  {Count: 2, LastUsed: now},
	}
	names := rankPickerNames([]string{"qa", "prod", "dev", "current", "unused"}, "current", usage)
	if got := strings.Join(names, ","); got != "current,prod,dev,qa,unused" {
		t.Fatalf("ranked choices = %q", got)
	}
}

func TestContextPickerMoreRevealsLessUsedContexts(t *testing.T) {
	m := contextPickerModel{}
	for i := 0; i < 8; i++ {
		m.contexts = append(m.contexts, kube.ContextInfo{Name: fmt.Sprintf("context-%d", i)})
	}
	if strings.Contains(m.View(), "context-7") || !strings.Contains(m.View(), "More") {
		t.Fatalf("collapsed picker should show More: %q", m.View())
	}
	for i := 0; i < frequentChoices; i++ {
		updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
		m = updated.(contextPickerModel)
	}
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(contextPickerModel)
	if !m.expanded || m.selected != "" || m.index != frequentChoices || !strings.Contains(m.View(), "context-7") {
		t.Fatalf("More should expand contexts: %+v", m)
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if got := updated.(contextPickerModel).selected; got != "context-5" {
		t.Fatalf("selected %q, want first less-used context", got)
	}
}

func TestNamespacePickerMoreRevealsLessUsedNamespaces(t *testing.T) {
	m := namespacePickerModel{}
	for i := 0; i < 8; i++ {
		m.namespaces = append(m.namespaces, fmt.Sprintf("namespace-%d", i))
	}
	for i := 0; i < frequentChoices; i++ {
		updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
		m = updated.(namespacePickerModel)
	}
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(namespacePickerModel)
	if !m.expanded || m.selected != "" || !strings.Contains(m.View(), "namespace-7") {
		t.Fatalf("More should expand namespaces: %+v", m)
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if got := updated.(namespacePickerModel); got.expanded || got.selected != "" {
		t.Fatalf("Esc should collapse to frequent namespaces: %+v", got)
	}
}
