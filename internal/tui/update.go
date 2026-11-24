package tui

import (
	"context"
	"errors"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/fernandocpaz/tailg/internal/update"
)

var ErrRestart = errors.New("restart tailg after update")
var ErrUpdateQuit = errors.New("quit tailg from the update prompt")

type updateOfferMsg struct {
	release   update.Release
	available bool
}
type updateInstalledMsg struct{ err error }

type updateState struct {
	quit                            bool
	client                          *update.Client
	release                         update.Release
	pending, busy, focused, restart bool
	err                             string
}

func (m updateState) check(ctx context.Context) tea.Cmd {
	if m.client == nil {
		return nil
	}
	return func() tea.Msg {
		m.client.Cleanup()
		release, available := m.client.Offer(ctx)
		return updateOfferMsg{release: release, available: available}
	}
}

func (m *updateState) receive(message tea.Msg) bool {
	switch msg := message.(type) {
	case updateOfferMsg:
		m.release, m.pending = msg.release, msg.available
		return true
	case updateInstalledMsg:
		m.busy = false
		if msg.err != nil {
			m.err = msg.err.Error()
		} else {
			m.restart = true
		}
		return true
	}
	return false
}

func (m *updateState) key(ctx context.Context, msg tea.KeyMsg) tea.Cmd {
	if m.busy {
		return nil
	}
	switch msg.String() {
	case "esc", "q", "Q", "l", "L", "n", "N":
		m.pending = false
		m.client.Dismiss()
	case "tab", "shift+tab", "left", "right":
		m.focused = !m.focused
	case "enter":
		if !m.focused {
			m.pending = false
			m.client.Dismiss()
			return nil
		}
		return m.install(ctx)
	case "u", "U":
		return m.install(ctx)
	}
	return nil
}

func (m *updateState) install(ctx context.Context) tea.Cmd {
	m.busy, m.err = true, ""
	client, release := m.client, m.release
	return func() tea.Msg { return updateInstalledMsg{err: client.Install(ctx, release)} }
}

func (m updateState) view(width, height int) string {
    return renderUpdateDialog("tailg", m.client.Version, m.release.Version, m.busy, m.focused, m.err, width, height)
}

func (m model) showUpdate() bool {
	return m.updates.pending && !m.errorLogs.open && !m.replicas.open && !m.traceOpen && !m.freshnessOpen && !m.heartbeatOpen && !m.issueOpen && !m.resourceOpen && m.detail == ""
}
