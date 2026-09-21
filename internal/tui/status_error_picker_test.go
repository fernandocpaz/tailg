package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/fernandocpaz/tailg/internal/kube"
)

func TestStatusErrorPickerViewShowsSelectors(t *testing.T) {
	model := statusErrorPickerModel{
		width: 140,
		errors: []kube.StatusErrorSelection{
			{Count: 3, Kind: "TIMEOUT", Service: "api", Pod: "api-123", Container: "api", Summary: "database timeout", LastSeen: time.Now().Add(-time.Minute)},
		},
	}
	view := model.View()
	for _, expected := range []string{"SELECT", "[1]", "TIMEOUT", "api-123/api", "database timeout"} {
		if !strings.Contains(view, expected) {
			t.Fatalf("view missing %q: %s", expected, view)
		}
	}
}

func TestStatusErrorPickerEnterSelectsCurrentRow(t *testing.T) {
	model := statusErrorPickerModel{
		index: 1,
		errors: []kube.StatusErrorSelection{
			{Pod: "first"},
			{Pod: "second"},
		},
	}
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(statusErrorPickerModel)
	if got.selected == nil || got.selected.Pod != "second" {
		t.Fatalf("selected=%+v", got.selected)
	}
}

func TestStatusErrorPickerMouseClickSelectsRow(t *testing.T) {
	model := statusErrorPickerModel{
		errors: []kube.StatusErrorSelection{
			{Pod: "first"},
			{Pod: "second"},
		},
	}
	updated, _ := model.Update(tea.MouseMsg{
		X: 2, Y: statusErrorRowsStart + 1,
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
	})
	got := updated.(statusErrorPickerModel)
	if got.selected == nil || got.selected.Pod != "second" {
		t.Fatalf("selected=%+v", got.selected)
	}
}
