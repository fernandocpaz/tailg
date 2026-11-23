package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/fernandocpaz/tailg/internal/core"
)

func TestAnalyticsF9DashboardAndEscapePreserveLiveView(t *testing.T) {
	now := time.Now()
	analytics := core.NewRollingAnalytics()
	analytics.ObserveAt(core.LogEvent{
		Pod: "api-7d9", Container: "api",
		Message: "HTTP GET /orders/17 responded 503 in 700 ms",
		ObservedAt: now,
	}, now)
	m := model{
		config: Config{Formatter: core.Formatter{Color: false}},
		state: core.NewFilterState(10),
		input: textinput.New(),
		analytics: analytics,
		width: 120,
		height: 22,
		followsLive: true,
	}
	opened, _ := m.Update(tea.KeyMsg{Type: tea.KeyF9})
	active := opened.(model)
	if !active.analyticsOpen {
		t.Fatal("F9 did not open analytics")
	}
	view := active.View()
	for _, expected := range []string{
		"HTTP analytics (F9)", "Requests:", "5xx:", "Slow (>250ms)",
		"Approx p50/p95/p99", "/orders/{id}", "F9/Esc closes",
	} {
		if !strings.Contains(view, expected) {
			t.Fatalf("missing analytics label %q:\n%s", expected, view)
		}
	}
	closed, _ := active.Update(tea.KeyMsg{Type: tea.KeyEsc})
	previous := closed.(model)
	if previous.analyticsOpen || !previous.followsLive || previous.state != m.state {
		t.Fatal("Esc did not restore the original live log state")
	}
}

func TestAnalyticsKeyDoesNotChangeSearchFilter(t *testing.T) {
	state := core.NewFilterState(10)
	state.SetFilter("timeout")
	input := textinput.New()
	input.SetValue("timeout")
	m := model{state: state, input: input, width: 80, height: 16, analyticsOpen: true}
	closed, _ := m.Update(tea.KeyMsg{Type: tea.KeyF9})
	got := closed.(model)
	if got.analyticsOpen || got.input.Value() != "timeout" || got.state.Filter() != "timeout" {
		t.Fatalf("F9 toggling changed search state: open=%t filter=%q", got.analyticsOpen, got.input.Value())
	}
}

func TestAnalyticsErrorTrendNeedsRealBaselineAndLargeChange(t *testing.T) {
	if message := analyticsErrorTrend(core.AnalyticsCounts{StatusKnown: 10, ServerErrors: 10}, core.AnalyticsCounts{StatusKnown: 10}); !strings.Contains(message, "insufficient") {
		t.Fatalf("sparse baseline should be inconclusive: %q", message)
	}
	if message := analyticsErrorTrend(core.AnalyticsCounts{StatusKnown: 100, ServerErrors: 20}, core.AnalyticsCounts{StatusKnown: 100, ServerErrors: 1}); !strings.Contains(message, "Possible 5xx spike") {
		t.Fatalf("strong spike should be flagged: %q", message)
	}
	if message := analyticsErrorTrend(core.AnalyticsCounts{StatusKnown: 100, ServerErrors: 2}, core.AnalyticsCounts{StatusKnown: 100, ServerErrors: 1}); strings.Contains(message, "Possible 5xx spike") {
		t.Fatalf("small random movement should not be flagged: %q", message)
	}
}
