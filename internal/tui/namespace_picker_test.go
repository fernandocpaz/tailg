package tui

import (
	"errors"
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

func TestNamespacePickerUsesHistoryWhenListingIsForbidden(t *testing.T) {
	usage := map[string]ChoiceUsage{"apollo": {Count: 5}, "lrjob": {Count: 2}}
	withError := namespacePickerChoices(nil, "ui", errors.New("forbidden"), usage)
	if got := strings.Join(withError, ","); got != "ui,apollo,lrjob" {
		t.Fatalf("fallback choices = %q", got)
	}
	fromCluster := namespacePickerChoices([]string{"ui", "api"}, "ui", nil, usage)
	if got := strings.Join(fromCluster, ","); got != "ui,api" {
		t.Fatalf("listed choices = %q", got)
	}
}
