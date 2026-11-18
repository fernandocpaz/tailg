package tui

import (
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/fernandocpaz/tailg/internal/core"
)

func TestPickerFileKeyUsesSelectionAndKeypressTime(t *testing.T) {
	for _, key := range []string{"f", "F", "enter"} {
		for _, marked := range []bool{false, true} {
			m := newPickerModel([]core.AppChoice{{Name: "api", Pods: []string{"api-1"}}, {Name: "worker", Pods: []string{"worker-1"}}}, 2, "qa", "apollo", nil)
			if marked {
				m.checked = map[string]bool{"worker": true}
			}
			before := time.Now()
			msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)}
			if key == "enter" {
				msg = tea.KeyMsg{Type: tea.KeyEnter}
			}
			updated, cmd := m.Update(msg)
			result := updated.(pickerModel).result
			want := "api"
			if marked {
				want = "worker"
			}
			if cmd == nil || result.Action != PickerOpenApp || result.App.Name != want || result.LogToFile != (key != "enter") {
				t.Fatalf("key=%s marked=%t result=%+v", key, marked, result)
			}
			if result.LogToFile && (result.CaptureFrom.Before(before) || result.CaptureFrom.After(time.Now())) {
				t.Fatalf("missing keypress time: %v", result.CaptureFrom)
			}
			if !result.LogToFile && !result.CaptureFrom.IsZero() {
				t.Fatal("Enter must not enable live capture")
			}
		}
	}
}

func TestExactPodFileSelectionPropagatesToParent(t *testing.T) {
	m := newPickerModel([]core.AppChoice{{Name: "api", Pods: []string{"api-1", "api-2"}}}, 1, "qa", "apollo", nil)
	m.width, m.height = 100, 30
	m.openPickerSelection("pods")
	m.selection.pods.checked = map[string]bool{"api-1": true, "api-2": true}
	before := time.Now()
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'F'}})
	result := updated.(pickerModel).result
	if cmd == nil || !result.LogToFile || len(result.Pods) != 2 || result.CaptureFrom.Before(before) {
		t.Fatalf("exact pod capture: %+v", result)
	}
}

func TestFileKeyDoesNothingOnEmptyPicker(t *testing.T) {
	m := newPickerModel(nil, 0, "qa", "apollo", nil)
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	if cmd != nil || updated.(pickerModel).result.LogToFile {
		t.Fatal("empty picker started recording")
	}
}
