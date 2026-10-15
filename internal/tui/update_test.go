package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/fernandocpaz/tailg/internal/update"
)

func updatePickerFixture() pickerModel {
	m := newPickerModel(pickerViewportFixture(), 13, "qa", "apollo", []string{"app-00"})
	m.width, m.height, m.index, m.refreshContext = 80, 24, 4, context.Background()
	m.updates.client = update.New("v0.1.16")
	m.updates.receive(updateOfferMsg{release: update.Release{Version: "v0.1.17"}, available: true})
	return m
}

func TestStartupUpdateDefaultsToLaterAndPreservesPicker(t *testing.T) {
	for _, key := range []string{"enter", "esc"} {
		m := updatePickerFixture()
		if !strings.Contains(ansi.Strip(m.View()), "> [ Later ]") {
			t.Fatal("Later is not focused by default")
		}
		updated, cmd := m.Update(pickerTestKeyMsg(key))
		m = updated.(pickerModel)
		if cmd != nil || m.updates.pending || m.index != 4 || !m.checked["app-00"] {
			t.Fatal("Later changed selection or installed an update")
		}
	}
}

func TestUpdatePromptDefersBehindExistingModalAndCapturesKeys(t *testing.T) {
	m := updatePickerFixture()
	m.openPickerSelection("pods")
	if m.showUpdate() || strings.Contains(ansi.Strip(m.View()), "tailg update available") {
		t.Fatal("update prompt covered existing selector")
	}
	m = pickerTestKey(m, "esc")
	if !m.showUpdate() {
		t.Fatal("pending update did not appear after selector closed")
	}
	m = pickerTestKey(m, "j")
	if m.index != 4 {
		t.Fatal("update key reached background picker")
	}
	updated, cmd := m.Update(pickerTestKeyMsg("U"))
	m = updated.(pickerModel)
	if cmd == nil || !m.updates.busy {
		t.Fatal("explicit Update did not begin install")
	}
	updated, duplicate := m.Update(pickerTestKeyMsg("U"))
	m = updated.(pickerModel)
	if duplicate != nil {
		t.Fatal("update could be started twice")
	}
	updated, _ = m.Update(updateInstalledMsg{err: errors.New("checksum mismatch")})
	m = updated.(pickerModel)
	if m.updates.restart || !m.updates.pending || m.updates.busy || !strings.Contains(ansi.Strip(m.View()), "checksum mismatch") {
		t.Fatal("failed update quit or hid failure")
	}
	updated, cmd = m.Update(updateInstalledMsg{})
	if cmd == nil || updated.(pickerModel).result.Action != PickerRestart {
		t.Fatal("successful update did not request restart")
	}
}

func TestUpdateModalFitsNormalTinyBusyAndErrorViews(t *testing.T) {
	for _, size := range []tea.WindowSizeMsg{{Width: 24, Height: 8}, {Width: 40, Height: 12}, {Width: 80, Height: 24}, {Width: 160, Height: 40}} {
		for _, mode := range []string{"offer", "busy", "error"} {
			m := updatePickerFixture()
			m.updates.busy = mode == "busy"
			if mode == "error" {
				m.updates.err = strings.Repeat("cannot replace installed executable; ", 20)
			}
			updated, _ := m.Update(size)
			m = updated.(pickerModel)
			assertPickerFits(t, m)
			view := ansi.Strip(m.View())
			if mode != "busy" && !strings.Contains(view, "Esc") {
				t.Fatalf("close control hidden at %+v mode=%s:\n%s", size, mode, view)
			}
		}
	}
}

func TestLogUpdateDoesNotStealExistingPanelAndWaitsForExplicitChoice(t *testing.T) {
	m := model{ctx: context.Background(), width: 80, height: 24}
	m.updates = updatePickerFixture().updates
	m.resourceOpen = true
	if m.showUpdate() {
		t.Fatal("update stole focus from resource panel")
	}
	m.resourceOpen = false
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil || updated.(model).updates.pending {
		t.Fatal("Enter did not continue existing log session")
	}
}
