package tui

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/fernandocpaz/tailg/internal/core"
)

func searchViewModel() model {
	input := textinput.New()
	input.Focus()
	input.SetValue("needle")
	state := core.NewFilterState(100)
	state.Append("latest needle")
	state.SetFilter("needle")
	return model{ctx: context.Background(), input: input, state: state,
		heartbeat: &core.HeartbeatAnalyzer{}, width: 100, height: 10,
		followsLive: true, generation: 1, searching: true}
}

func TestHistoryViewPausesAndEndRestoresLiveBuffer(t *testing.T) {
	m := searchViewModel()
	history := []string{"old needle"}
	for i := 0; i < 30; i++ {
		history = append(history, fmt.Sprintf("old context %d", i))
	}
	updated, _ := m.Update(searchMsg{generation: 1, query: "needle", lines: history})
	m = updated.(model)
	if m.followsLive || !strings.Contains(m.renderHeader(), "PAUSED") {
		t.Fatal("historical context advertised as live")
	}
	start := len(m.state.Lines()) - m.logHeight() - m.scroll
	updated, _ = m.Update(logMsg(core.LogEvent{Message: "new needle"}))
	m = updated.(model)
	if got := len(m.state.Lines()) - m.logHeight() - m.scroll; got != start {
		t.Fatalf("incoming log moved history viewport: %d -> %d", start, got)
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnd})
	m = updated.(model)
	if !m.followsLive || m.scroll != 0 || m.searching {
		t.Fatal("End did not resume live view")
	}
	if strings.Contains(strings.Join(m.state.Lines(), "\n"), "old needle") || len(m.state.Lines()) != 2 {
		t.Fatalf("End retained history or lost live logs: %#v", m.state.Lines())
	}
	updated, _ = m.Update(searchMsg{generation: 1, query: "needle", lines: history})
	m = updated.(model)
	if !m.followsLive || len(m.state.Lines()) != 2 {
		t.Fatal("late search replaced live view after End")
	}
}

func TestClearFilterRestoresLiveView(t *testing.T) {
	for _, shared := range []bool{false, true} {
		t.Run(fmt.Sprintf("shared=%t", shared), func(t *testing.T) {
			m := searchViewModel()
			m.state.SetSearchResults("needle", []string{"old needle", "old context"})
			m.scroll = 20
			m.followsLive = false
			var updated tea.Model
			if shared {
				path := filepath.Join(t.TempDir(), "filter")
				if err := InitializeSharedFilter(path); err != nil {
					t.Fatal(err)
				}
				m.config.FilterFile = path
				m.lastSharedText = "needle"
				updated, _ = m.Update(sharedFilterTick(time.Now()))
			} else {
				m.input.CursorEnd()
				updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlU})
			}
			m = updated.(model)
			if m.input.Value() != "" || !m.followsLive || m.scroll != 0 || m.selected != 0 {
				t.Fatalf("clear did not restore live position: filter=%q live=%t scroll=%d selected=%d", m.input.Value(), m.followsLive, m.scroll, m.selected)
			}
			updated, _ = m.Update(searchMsg{generation: 1, query: "needle", lines: []string{"old needle"}})
			m = updated.(model)
			if got := m.state.Lines(); len(got) != 1 || got[0] != "latest needle" {
				t.Fatalf("old search intruded after clearing: %#v", got)
			}
		})
	}
}
