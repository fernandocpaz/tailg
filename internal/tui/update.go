package tui

import (
	"context"
	"errors"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
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
	if width < 40 || height < 12 {
		if m.busy {
			return ansi.Wrap("Updating…\nCtrl+C quits.", max(1, width), "")
		}
		return ansi.Wrap("Update available. Resize to 40 × 12.\nEsc continues without updating.", max(1, width), "")
	}
	inner := min(76, width-6)
	header := strings.Split(ansi.Wrap(headerStyle.Render("tailg update available")+"\nInstalled: "+m.client.Version+" · Latest: "+m.release.Version, inner, ""), "\n")
	var body, footer []string
	if m.busy {
		body = strings.Split(ansi.Wrap("Downloading and verifying the release…\ntailg will restart after installation.", inner, ""), "\n")
	} else {
		later, install := "> [ Later ]", "  [ Update & restart ]"
		if m.focused {
			later, install = "  [ Later ]", "> [ Update & restart ]"
		}
		footer = strings.Split(ansi.Wrap(later+"  "+install+"\nU Update · Enter Select · Esc Later", inner, ""), "\n")
		if m.err != "" {
			body = pickerLimitedLines("Update failed: "+m.err, inner, max(1, height-5-len(header)-len(footer)))
			if width >= 80 && height >= 18 {
				body = append(body, "Manual: go install github.com/fernandocpaz/tailg/cmd/tailg@"+m.release.Version)
			}
		}
	}
	lines := append(header, body...)
	if len(footer) > 0 {
		lines = append(lines, "")
		lines = append(lines, footer...)
	}
	content := ansi.Wrap(strings.Join(lines, "\n"), inner, "")
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(accentColor).
		Foreground(modalForeground).Background(modalBackground).Padding(0, 1).Width(inner + 2).Render(content)
}

func (m model) showUpdate() bool {
	return m.updates.pending && !m.errorLogs.open && !m.replicas.open && !m.traceOpen && !m.freshnessOpen && !m.heartbeatOpen && !m.issueOpen && !m.resourceOpen && m.detail == ""
}
