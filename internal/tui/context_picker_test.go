package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/fernandocpaz/tailg/internal/kube"
)

func TestContextPickerNavigationAndCancel(t *testing.T) {
	m := contextPickerModel{contexts: []kube.ContextInfo{{Name: "tkgs-dev", Namespace: "apollo"}, {Name: "tkgs-qa", Namespace: "api"}}}
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	updated, _ = updated.(contextPickerModel).Update(tea.KeyMsg{Type: tea.KeyEnter})
	if got := updated.(contextPickerModel).selected; got != "tkgs-qa" {
		t.Fatalf("selected %q, want tkgs-qa", got)
	}
	if !strings.Contains(updated.(contextPickerModel).View(), "api") {
		t.Fatal("picker should show context namespace")
	}
	cancelled, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cancelled.(contextPickerModel).selected != "" {
		t.Fatal("Esc should cancel without selecting a context")
	}
}
