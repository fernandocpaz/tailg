package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

func TestNamespacePickerListAndManualEntry(t *testing.T) {
	namespaces := prepareNamespaces([]string{"ui", "apollo", "ui"}, "lrjob")
	if strings.Join(namespaces, ",") != "apollo,lrjob,ui" {
		t.Fatalf("namespaces = %v", namespaces)
	}
	m := namespacePickerModel{namespaces: namespaces}
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	updated, _ = updated.(namespacePickerModel).Update(tea.KeyMsg{Type: tea.KeyEnter})
	if got := updated.(namespacePickerModel).selected; got != "lrjob" {
		t.Fatalf("selected %q, want lrjob", got)
	}
	manual := namespacePickerModel{input: textinput.New(), loadError: "forbidden"}
	updated, _ = manual.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	for _, char := range "cronjob" {
		updated, _ = updated.(namespacePickerModel).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{char}})
	}
	updated, _ = updated.(namespacePickerModel).Update(tea.KeyMsg{Type: tea.KeyEnter})
	if got := updated.(namespacePickerModel).selected; got != "cronjob" {
		t.Fatalf("manual namespace = %q, want cronjob", got)
	}
	if !strings.Contains(manual.View(), "Namespace list unavailable") {
		t.Fatalf("missing listing error: %q", manual.View())
	}
}
