package tui

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/fernandocpaz/tailg/internal/core"
	"github.com/fernandocpaz/tailg/internal/update"
)

type networkResult struct{ report core.NetworkReport }
type networkTick struct{ generation int }
type networkEntry struct{ id, heading, summary, detail, status string }
type networkModel struct {
	ctx                                              context.Context
	load                                             func(context.Context) core.NetworkReport
	report                                           core.NetworkReport
	width, height, tab, selected, scroll, generation int
	busy, color, editing, warnings                   bool
	filter                                           string
	detail                                           *networkEntry
	updates                                          updateState
}

// RunNetwork owns one terminal program; details are overlays in that program.
func RunNetwork(ctx context.Context, report core.NetworkReport, load func(context.Context) core.NetworkReport, input io.Reader, output io.Writer, color bool, updates *update.Client) error {
	final, err := tea.NewProgram(networkModel{ctx: ctx, report: report, load: load, color: color, width: 100, height: 24, busy: true, updates: updateState{client: updates}}, tea.WithContext(ctx), tea.WithInput(input), tea.WithOutput(output), tea.WithAltScreen()).Run()
	if m, ok := final.(networkModel); ok && m.updates.restart {
		return ErrRestart
	}
	return err
}

func networkPoll(generation int) tea.Cmd {
	return tea.Tick(15*time.Second, func(time.Time) tea.Msg { return networkTick{generation} })
}
func (m networkModel) Init() tea.Cmd {
	var load tea.Cmd
	if m.load != nil {
		load = func() tea.Msg { return networkResult{m.load(m.ctx)} }
	}
	return tea.Batch(load, m.updates.check(m.ctx))
}
func (m *networkModel) refresh() tea.Cmd {
	if m.busy || m.load == nil {
		return nil
	}
	m.busy = true
	m.generation++
	return func() tea.Msg { return networkResult{m.load(m.ctx)} }
}
func (m networkModel) entries() []networkEntry {
	var rows []networkEntry
	for i, e := range m.report.Errors {
		rows = append(rows, networkEntry{id: fmt.Sprintf("source/%d", i), heading: "INCOMPLETE · source unavailable", summary: e, detail: e + "\n\nOther sources remain available. Missing information is not a healthy result.", status: "UNKNOWN"})
	}
	if m.tab == 3 {
		for _, e := range m.report.Events {
			status := "INFO"
			if strings.EqualFold(e.Type, "Warning") {
				status = "REVIEW"
			}
			detail := fmt.Sprintf("%s\nObject: %s\nReason: %s\nFirst: %s\nLast: %s\nCount: %d\nSource: %s\n\n%s", e.Type, e.Object, e.Reason, activityTime(e.FirstAt), activityTime(e.LastAt), e.Count, e.Source, e.Message)
			rows = append(rows, networkEntry{id: "event/" + e.ID, heading: activityTime(e.LastAt) + " · " + e.Type + " · " + e.Object + " · " + e.Reason, summary: e.Message, detail: detail, status: status})
		}
	} else {
		for _, r := range m.report.Rows {
			if m.tab == 0 && r.Kind != "Ingress" && r.Kind != "HTTPProxy" {
				continue
			}
			if m.tab == 1 && r.Kind != "Service" {
				continue
			}
			if m.tab == 2 && r.Kind != "NetworkPolicy" {
				continue
			}
			detail := r.ID() + " · " + r.Status + "\nSnapshot: " + activityTime(m.report.At) + "\n" + r.Detail
			related := map[string]bool{r.ID(): true}
			for _, object := range r.Objects {
				related[object] = true
			}
			detail += "\n\nRelated retained events:\n"
			count := 0
			for _, e := range m.report.Events {
				if related[e.Object] {
					detail += fmt.Sprintf("%s · %s · %s · %s ×%d\n%s\n", activityTime(e.LastAt), e.Type, e.Object, e.Reason, e.Count, e.Message)
					count++
				}
			}
			if count == 0 {
				detail += "No related retained events available."
			}
			rows = append(rows, networkEntry{id: r.ID(), heading: r.Status + " · " + r.ID(), summary: r.Summary, detail: detail, status: r.Status})
		}
	}
	query := strings.ToLower(strings.TrimSpace(m.filter))
	var result []networkEntry
	for _, r := range rows {
		r.heading, r.summary, r.detail = activityText(r.heading), activityText(r.summary), activityText(r.detail)
		if strings.HasPrefix(r.id, "source/") || ((!m.warnings || r.status == "REVIEW" || r.status == "UNKNOWN") && (query == "" || strings.Contains(strings.ToLower(r.detail+" "+r.summary), query))) {
			result = append(result, r)
		}
	}
	return result
}
func (m networkModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	if m.updates.receive(message) {
		if m.updates.restart {
			return m, tea.Quit
		}
		return m, nil
	}
	switch msg := message.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = max(1, msg.Width), max(1, msg.Height)
	case networkTick:
		if msg.generation != m.generation {
			return m, nil
		}
		cmd := m.refresh()
		return m, cmd
	case networkResult:
		old := m.entries()
		id := ""
		if m.selected < len(old) {
			id = old[m.selected].id
		}
		m.report, m.busy = msg.report, false
		rows := m.entries()
		m.selected = min(m.selected, max(0, len(rows)-1))
		for i, r := range rows {
			if r.id == id {
				m.selected = i
			}
		}
		return m, networkPoll(m.generation)
	case tea.KeyMsg:
		key := msg.String()
		if key == "ctrl+c" {
			return m, tea.Quit
		}
		if m.updates.pending && m.detail == nil && !m.editing {
			if m.width < 40 || m.height < 12 {
				if key == "esc" || key == "q" {
					m.updates.pending = false
					m.updates.client.Dismiss()
				}
				return m, nil
			}
			cmd := m.updates.key(m.ctx, msg)
			return m, cmd
		}
		if m.detail != nil {
			maxScroll := max(0, len(m.detailLines())-m.detailHeight())
			switch key {
			case "esc", "q", "enter":
				m.detail = nil
				m.scroll = 0
			case "up", "k":
				m.scroll = max(0, m.scroll-1)
			case "down", "j":
				m.scroll = min(maxScroll, m.scroll+1)
			case "pgdown":
				m.scroll = min(maxScroll, m.scroll+m.detailHeight())
			case "pgup":
				m.scroll = max(0, m.scroll-m.detailHeight())
			case "home":
				m.scroll = 0
			case "end":
				m.scroll = maxScroll
			}
			return m, nil
		}
		if m.editing {
			switch key {
			case "enter", "esc":
				m.editing = false
			case "backspace", "ctrl+h":
				runes := []rune(m.filter)
				if len(runes) > 0 {
					m.filter = string(runes[:len(runes)-1])
				}
			case "ctrl+u":
				m.filter = ""
			default:
				if msg.Type == tea.KeyRunes {
					m.filter += string(msg.Runes)
				}
				if msg.Type == tea.KeySpace {
					m.filter += " "
				}
			}
			m.selected = 0
			return m, nil
		}
		switch key {
		case "q", "esc":
			return m, tea.Quit
		case "1", "2", "3", "4":
			m.tab = int(key[0] - '1')
			m.selected = 0
		case "e", "E":
			m.tab = 3
			m.selected = 0
		case "tab":
			m.tab = (m.tab + 1) % 4
			m.selected = 0
		case "shift+tab":
			m.tab = (m.tab + 3) % 4
			m.selected = 0
		case "r", "R":
			cmd := m.refresh()
			return m, cmd
		case "w", "W":
			m.warnings = !m.warnings
			m.selected = 0
		case "/":
			m.editing = true
		case "up", "k":
			m.selected = max(0, m.selected-1)
		case "down", "j":
			m.selected = min(max(0, len(m.entries())-1), m.selected+1)
		case "pgdown":
			m.selected = min(max(0, len(m.entries())-1), m.selected+max(1, (m.height-7)/2))
		case "pgup":
			m.selected = max(0, m.selected-max(1, (m.height-7)/2))
		case "home":
			m.selected = 0
		case "end":
			m.selected = max(0, len(m.entries())-1)
		case "enter":
			rows := m.entries()
			if len(rows) > 0 {
				entry := rows[min(m.selected, len(rows)-1)]
				m.detail = &entry
				m.scroll = 0
			}
		}
	}
	return m, nil
}

func (m networkModel) detailWidth() int  { return max(1, min(116, m.width-4)-4) }
func (m networkModel) detailHeight() int { return max(1, m.height-8) }
func (m networkModel) detailLines() []string {
	return strings.Split(ansi.Hardwrap(ansi.Wrap(m.detail.detail, m.detailWidth(), ""), m.detailWidth(), true), "\n")
}

func (m networkModel) View() string {
	if m.width < 40 || m.height < 12 {
		return pickerScreenFrame([]string{"tailg · Network", "Resize to at least 40 × 12."}, []string{"Q/Esc Quit"}, m.width, m.height)
	}
	header := []string{
		renderWithColor(headerStyle, truncatePlain("tailg · Network · "+activityText(m.report.Namespace)+" / "+activityText(m.report.Context), m.width), m.color),
		"1 Routes  2 Services  3 Policies  4/E Events  · Tab Switch",
		"Configuration review · OK = checks passed; traffic not tested",
	}
	if m.width < 70 {
		header[1] = "1 Routes 2 Svc 3 Policy 4/E Events · Tab"
	}
	collected := "not yet collected"
	if !m.report.At.IsZero() {
		collected = m.report.At.Local().Format("15:04:05")
	}
	review, unknown := 0, 0
	for _, r := range m.report.Rows {
		if r.Status == "REVIEW" {
			review++
		}
		if r.Status == "UNKNOWN" {
			unknown++
		}
	}
	status := fmt.Sprintf("Collected %s · %d REVIEW %d UNKNOWN · refresh 15s", collected, review, unknown)
	if m.busy {
		status += " · refreshing"
	}
	if len(m.report.Errors) > 0 {
		status = "INCOMPLETE · " + status
	}
	header = append(header, truncatePlain(status, m.width))
	tabs := []string{"Routes", "Services", "Policies", "Events"}
	filter := tabs[m.tab] + " | Filter: " + activityText(m.filter)
	if m.editing {
		filter += "▏"
	}
	if m.warnings {
		filter += " | NEEDS REVIEW"
	}
	header = append(header, truncatePlain(filter, m.width))
	rows := m.entries()
	capacity := max(1, (m.height-len(header)-2)/2)
	selected := min(m.selected, max(0, len(rows)-1))
	start := max(0, selected-capacity+1)
	end := min(len(rows), start+capacity)
	for i := start; i < end; i++ {
		style := lipgloss.NewStyle()
		if rows[i].status == "OK" {
			style = okStyle
		}
		if rows[i].status == "REVIEW" {
			style = alertStyle
		}
		if rows[i].status == "UNKNOWN" {
			style = warnStyle
		}
		marker := "  "
		if i == selected {
			style = selectedStyle
			marker = "> "
		}
		header = append(header, renderWithColor(style, truncatePlain(marker+rows[i].heading, m.width), m.color), renderWithColor(style, truncatePlain("  "+strings.ReplaceAll(rows[i].summary, "\n", " "), m.width), m.color))
	}
	if len(rows) == 0 {
		message := "No entries match this view. Unavailable sources appear above."
		if m.busy && m.report.At.IsZero() {
			message = "Loading namespace network resources…"
		}
		header = append(header, message)
	}
	footer := []string{fmt.Sprintf("Rows %d–%d of %d", min(len(rows), start+1), end, len(rows)), "↑↓ Enter Details · / Filter · W Review · R Refresh · Q Quit"}
	if m.width < 70 {
		footer[1] = "↑↓ Enter / W R · Q Quit"
	}
	background := pickerScreenFrame(header, footer, m.width, m.height)
	if m.detail != nil {
		all := m.detailLines()
		height := m.detailHeight()
		start := min(m.scroll, max(0, len(all)-height))
		end := min(len(all), start+height)
		content := []string{truncatePlain(m.detail.heading, m.detailWidth()), ""}
		content = append(content, all[start:end]...)
		content = append(content, fmt.Sprintf("Lines %d–%d/%d · Esc Back", start+1, end, len(all)))
		style := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1).Width(m.detailWidth() + 2)
		if m.color {
			style = style.Foreground(modalForeground).Background(modalBackground).BorderForeground(accentColor)
		}
		background = overlayModal(background, style.Render(strings.Join(content, "\n")), m.width, m.height)
	}
	if m.updates.pending && m.detail == nil && !m.editing {
		background = overlayModal(background, m.updates.view(m.width, m.height), m.width, m.height)
	}
	if !m.color {
		return core.StripANSI(background)
	}
	return background
}

// NetworkText uses the same control-character sanitization as interactive details.
func NetworkText(report core.NetworkReport) string { return activityText(report.Text()) }
