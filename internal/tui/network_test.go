package tui

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/fernandocpaz/tailg/internal/core"
)

func networkModelFixture() networkModel {
	r := core.NetworkReport{Context: "tkgs-qa", Namespace: "apollo", At: time.Now(), Rows: []core.NetworkRow{
		{Kind: "Ingress", Name: "api", Status: "REVIEW", Summary: "api.test/api -> Service/api:80", Detail: "missing ready backends\n" + strings.Repeat("long diagnostic line 日本語\n", 80), Objects: []string{"Service/api", "Pod/api-abc"}},
		{Kind: "Service", Name: "api", Status: "OK", Detail: "ready endpoints 1/1"},
		{Kind: "NetworkPolicy", Name: "api", Status: "INFO", Detail: "additive rules"},
	}, Events: []core.NamespaceEvent{{ID: "e", Object: "Pod/api-abc", Type: "Warning", Reason: "FailedCreatePodSandBox", Message: "CNI setup failed"}}}
	return networkModel{ctx: context.Background(), report: r, width: 100, height: 24}
}
func networkKey(m networkModel, key string) networkModel {
	msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)}
	switch key {
	case "enter":
		msg = tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		msg = tea.KeyMsg{Type: tea.KeyEsc}
	case "end":
		msg = tea.KeyMsg{Type: tea.KeyEnd}
	}
	updated, _ := m.Update(msg)
	return updated.(networkModel)
}

func TestNetworkModalTabsFilteringAndEvents(t *testing.T) {
	m := networkModelFixture()
	m = networkKey(m, "enter")
	if m.detail == nil || !strings.Contains(m.detail.detail, "CNI setup failed") {
		t.Fatal("route detail lost its pod event")
	}
	m = networkKey(m, "4")
	if m.tab != 0 {
		t.Fatal("detail modal leaked tab keys into background")
	}
	m = networkKey(m, "end")
	if m.scroll == 0 {
		t.Fatal("long details cannot scroll")
	}
	m = networkKey(m, "esc")
	if m.detail != nil || m.tab != 0 {
		t.Fatal("Esc did not preserve route review")
	}
	m = networkKey(m, "2")
	if m.tab != 1 || m.entries()[0].id != "Service/api" {
		t.Fatal("Services tab failed")
	}
	m = networkKey(m, "e")
	if m.tab != 3 || !strings.Contains(m.entries()[0].detail, "CNI setup failed") {
		t.Fatal("Events tab failed")
	}
	m = networkKey(m, "/")
	m = networkKey(m, "absent")
	m = networkKey(m, "enter")
	if len(m.entries()) != 0 {
		t.Fatal("filter ignored")
	}
	m.report.Errors = []string{"Forbidden: cannot list EndpointSlices"}
	if len(m.entries()) != 1 || !strings.Contains(m.View(), "INCOMPLETE") {
		t.Fatal("filter hid collection errors")
	}
}

func TestNetworkRefreshIsSerializedAndKeepsContext(t *testing.T) {
	m := networkModelFixture()
	m.filter = "api"
	calls := 0
	m.load = func(context.Context) core.NetworkReport { calls++; return m.report }
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	m = updated.(networkModel)
	if !m.busy || m.generation != 1 || cmd == nil || calls != 0 {
		t.Fatal("refresh must begin asynchronously and update state")
	}
	_, duplicate := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if duplicate != nil {
		t.Fatal("overlapping refresh allowed")
	}
	_, stale := m.Update(networkTick{generation: 0})
	if stale != nil {
		t.Fatal("stale timer started another poll chain")
	}
	result := cmd()
	updated, next := m.Update(result)
	m = updated.(networkModel)
	if m.busy || calls != 1 || next == nil || m.filter != "api" || m.tab != 0 {
		t.Fatal("refresh lost state or did not schedule next poll")
	}
}

func TestNetworkViewportAndLastResourceRemainVisible(t *testing.T) {
	for _, size := range [][2]int{{160, 40}, {80, 24}, {40, 12}, {24, 8}} {
		m := networkModelFixture()
		m.width, m.height = size[0], size[1]
		for i := 0; i < 60; i++ {
			m.report.Rows = append(m.report.Rows, core.NetworkRow{Kind: "Ingress", Name: fmt.Sprintf("route-%02d", i), Status: "OK", Summary: "route"})
		}
		m = networkKey(m, "end")
		view := m.View()
		if m.width >= 40 && !strings.Contains(view, "route-59") {
			t.Fatal("last resource only appears after viewport movement")
		}
		for _, line := range strings.Split(view, "\n") {
			if lipgloss.Width(line) > m.width {
				t.Fatalf("viewport overflow at %v: %q", size, line)
			}
		}
		if len(strings.Split(view, "\n")) != m.height {
			t.Fatalf("viewport height changed at %v", size)
		}
		if m.width >= 40 {
			m.selected = 0
			m = networkKey(m, "enter")
			m = networkKey(m, "end")
			view = m.View()
			for _, line := range strings.Split(view, "\n") {
				if lipgloss.Width(line) > m.width {
					t.Fatalf("modal overflow at %v", size)
				}
			}
			if len(strings.Split(view, "\n")) != m.height {
				t.Fatal("modal changed terminal height")
			}
		}
	}
}

func TestNetworkOutputStripsTerminalControlSequences(t *testing.T) {
	m := networkModelFixture()
	m.report.Events[0].Message = "CNI\x1b[31m failure\x1b[0m\x1b]0;injected-title\a\r"
	text := NetworkText(m.report)
	if strings.ContainsAny(text, "\x1b\a\r") || !strings.Contains(text, "CNI failure") {
		t.Fatalf("unsafe output: %q", text)
	}
	if strings.Contains(m.View(), "\x1b[") {
		t.Fatal("--no-color emitted styling")
	}
}
