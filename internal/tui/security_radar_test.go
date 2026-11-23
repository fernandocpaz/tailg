package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/fernandocpaz/tailg/internal/core"
)

func TestSecurityRadarF10OpensAndClosesWithoutChangingLogView(t *testing.T) {
	now := time.Now().UTC()
	state := core.NewFilterState(200)
	input := textinput.New()
	input.SetValue("timeout")
	state.SetFilter("timeout")
	m := model{
		config: Config{Formatter: core.Formatter{Color: false}},
		state: state,
		heartbeat: &core.HeartbeatAnalyzer{},
		input: input,
		width: 128, height: 24, followsLive: true,
	}
	var events []core.LogEvent
	for i := 0; i < 36; i++ {
		status := 401
		if i >= 30 {
			status = 200
		}
		message := fmt.Sprintf("{\"RequestMethod\":\"GET\",\"RequestPath\":\"/login\",\"RemoteIpAddress\":\"192.0.2.55\",\"StatusCode\":%d,\"Elapsed\":12}", status)
		events = append(events, core.LogEvent{
			Workload: "login-api", Pod: "login-api-0", Container: "login-api",
			Message: message, ObservedAt: now,
		})
	}
	if err := m.processLogBatch(events); err != nil {
		t.Fatalf("live log processing: %v", err)
	}
	if m.securityRadar == nil || m.securityRadar.Snapshot(now).ObservedRequests != 36 {
		t.Fatalf("stream processor did not feed security radar")
	}
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyF10})
	active := updated.(model)
	if !active.securityRadarOpen {
		t.Fatal("F10 did not open stream radar")
	}
	rendered := active.View()
	for _, expected := range []string{
		"Stream security radar (F10)", "AUTH-BURST", "login-api",
		"192.0.2.55", "attributed sources", "F10/Esc closes",
	} {
		if !strings.Contains(rendered, expected) {
			t.Fatalf("radar view missing %q:\n%s", expected, rendered)
		}
	}
	closed, _ := active.Update(tea.KeyMsg{Type: tea.KeyEsc})
	got := closed.(model)
	if got.securityRadarOpen || got.state != state || got.input.Value() != "timeout" || !got.followsLive {
		t.Fatal("Esc changed filter/follow state")
	}
	reopened, _ := got.Update(tea.KeyMsg{Type: tea.KeyF10})
	closedAgain, _ := reopened.(model).Update(tea.KeyMsg{Type: tea.KeyF10})
	if closedAgain.(model).securityRadarOpen {
		t.Fatal("F10 did not toggle radar closed")
	}
}

func TestSecurityRadarDoesNotReplaceSingleLogSInspector(t *testing.T) {
	m := model{
		config: Config{Formatter: core.Formatter{Color: false}},
		state: core.NewFilterState(5), input: textinput.New(),
		width: 100, height: 18,
	}
	event := core.LogEvent{Pod: "api", Container: "api", Message: "HTTP GET /.git/config responded 404 in 8 ms"}
	records := core.RecordsForEvent(m.config.Formatter, event, false)
	if len(records) == 0 {
		t.Fatal("missing formatted record")
	}
	m.state.AppendRecords(records...)
	m.selected = 0
	opened, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'S'}})
	got := opened.(model)
	if !got.securityView || got.securityRadarOpen || !strings.Contains(got.securityText, "Sensitive endpoint") {
		t.Fatalf("S inspector shortcut changed: view=%v radar=%v", got.securityView, got.securityRadarOpen)
	}
}

func TestSecurityRadarEmptySessionReportsLimitations(t *testing.T) {
	m := model{securityRadarOpen: true, config: Config{Formatter: core.Formatter{Color: false}}, width: 110, height: 17}
	view := m.View()
	for _, term := range []string{"No multi-request indicators", "missing source", "local"} {
		if !strings.Contains(strings.ToLower(view), strings.ToLower(term)) {
			t.Fatalf("empty view missing %q: %s", term, view)
		}
	}
}
