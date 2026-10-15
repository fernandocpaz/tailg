package tui

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/fernandocpaz/tailg/internal/core"
	"github.com/fernandocpaz/tailg/internal/kube"
)

const namespaceActivityInterval = 15 * time.Second

type namespaceActivityState struct {
	tracker      core.NamespaceActivityTracker
	busy         bool
	request      int
	view         string
	selected     int
	detail       *namespaceActivityEntry
	detailScroll int
	filter       string
	editing      bool
	warningsOnly bool
}
type namespaceActivityEntry struct {
	id, heading, preview, detail string
	warning                      bool
}
type namespaceActivityTick struct {
	scope   string
	request int
}
type namespaceActivityResult struct {
	scope    string
	request  int
	snapshot core.NamespaceActivitySnapshot
}

func activityScope(kubeContext, namespace string) string { return kubeContext + "\x00" + namespace }
func loadNamespaceActivity(ctx context.Context, kubeContext, namespace string, request int) tea.Cmd {
	return func() tea.Msg {
		limited, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		snapshot := kube.NewRunner(namespace, kubeContext).NamespaceActivity(limited)
		return namespaceActivityResult{scope: activityScope(kubeContext, namespace), request: request, snapshot: snapshot}
	}
}
func namespaceActivityPoll(scope string, request int) tea.Cmd {
	return tea.Tick(namespaceActivityInterval, func(time.Time) tea.Msg { return namespaceActivityTick{scope: scope, request: request} })
}
func (m *pickerModel) refreshNamespaceActivity() tea.Cmd {
	if m.activity.busy {
		return nil
	}
	m.activity.busy = true
	m.activity.request++
	ctx := m.refreshContext
	if ctx == nil {
		ctx = context.Background()
	}
	return loadNamespaceActivity(ctx, m.kubeContext, m.namespace, m.activity.request)
}
func (m *pickerModel) updateNamespaceActivity(message tea.Msg) (bool, tea.Cmd) {
	switch msg := message.(type) {
	case namespaceActivityTick:
		if msg.scope != activityScope(m.kubeContext, m.namespace) || msg.request != m.activity.request || m.refreshContext == nil {
			return true, nil
		}
		return true, m.refreshNamespaceActivity()
	case namespaceActivityResult:
		if msg.scope != activityScope(m.kubeContext, m.namespace) || msg.request != m.activity.request || !m.activity.busy {
			return true, nil
		}
		selectedID := ""
		oldRows := m.namespaceActivityEntries()
		if m.activity.selected > 0 && m.activity.selected < len(oldRows) {
			selectedID = oldRows[m.activity.selected].id
		}
		m.activity.tracker.Apply(msg.snapshot)
		m.activity.busy = false
		rows := m.namespaceActivityEntries()
		m.activity.selected = min(m.activity.selected, max(0, len(rows)-1))
		for i, row := range rows {
			if row.id == selectedID {
				m.activity.selected = i
				break
			}
		}
		if m.refreshContext == nil {
			return true, nil
		}
		return true, namespaceActivityPoll(msg.scope, msg.request)
	}
	return false, nil
}

func (m pickerModel) updateNamespaceActivityKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	state := &m.activity
	if key.String() == "ctrl+c" {
		m.podMonitorState.stop()
		m.result.Action = PickerQuit
		return m, tea.Quit
	}
	if state.editing {
		switch key.String() {
		case "esc", "enter":
			state.editing = false
		case "backspace", "ctrl+h":
			runes := []rune(state.filter)
			if len(runes) > 0 {
				state.filter = string(runes[:len(runes)-1])
			}
		case "ctrl+u":
			state.filter = ""
		default:
			if key.Type == tea.KeyRunes {
				state.filter += string(key.Runes)
			}
			if key.Type == tea.KeySpace {
				state.filter += " "
			}
		}
		state.selected = 0
		return m, nil
	}
	switch key.String() {
	case "esc", "q":
		if state.detail != nil {
			state.detail = nil
			state.detailScroll = 0
		} else {
			state.view = ""
		}
	case "h", "H", "e", "E":
		state.view = "changes"
		if strings.EqualFold(key.String(), "e") {
			state.view = "events"
		}
		state.selected = 0
		state.detail = nil
		state.detailScroll = 0
	case "r", "R":
		cmd := m.refreshNamespaceActivity()
		return m, cmd
	case "/":
		state.editing = true
		state.detail = nil
	case "w", "W":
		if state.view == "events" {
			state.warningsOnly = !state.warningsOnly
			state.selected = 0
			state.detail = nil
		}
	case "enter":
		if state.detail == nil {
			rows := m.namespaceActivityEntries()
			if len(rows) > 0 {
				entry := rows[min(state.selected, len(rows)-1)]
				entry.detail = activityText("Context / namespace: "+m.kubeContext+" / "+m.namespace) + "\n\n" + entry.detail
				state.detail = &entry
				state.detailScroll = 0
			}
		}
	case "up", "k", "down", "j", "pgup", "pgdown", "home", "end":
		if state.detail != nil {
			_, maximum := m.namespaceActivityModal()
			step := 1
			if key.String() == "pgup" || key.String() == "pgdown" {
				step = max(1, m.height/2)
			}
			switch key.String() {
			case "up", "k", "pgup":
				state.detailScroll = max(0, state.detailScroll-step)
			case "down", "j", "pgdown":
				state.detailScroll = min(maximum, state.detailScroll+step)
			case "home":
				state.detailScroll = 0
			case "end":
				state.detailScroll = maximum
			}
		} else {
			maximum := max(0, len(m.namespaceActivityEntries())-1)
			step := 1
			if key.String() == "pgup" || key.String() == "pgdown" {
				step = max(1, (m.height-12)/2)
			}
			switch key.String() {
			case "up", "k", "pgup":
				state.selected = max(0, state.selected-step)
			case "down", "j", "pgdown":
				state.selected = min(maximum, state.selected+step)
			case "home":
				state.selected = 0
			case "end":
				state.selected = maximum
			}
		}
	}
	return m, nil
}
func activityTime(at time.Time) string {
	if at.IsZero() {
		return "unknown"
	}
	return at.Local().Format("2006-01-02 15:04:05 MST")
}
func activityText(value string) string {
	// Messages are data, never terminal control sequences. Keep readable newlines
	// for full detail views, but remove carriage returns and control characters.
	value = ansi.Strip(value)
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' || !unicode.IsControl(r) {
			return r
		}
		return -1
	}, value)
}
func (m pickerModel) namespaceActivityEntries() []namespaceActivityEntry {
	var result []namespaceActivityEntry
	if errors := m.activity.tracker.Errors; len(errors) > 0 {
		text := activityText(strings.Join(errors, "\n\n"))
		result = append(result, namespaceActivityEntry{id: "collection-errors", heading: fmt.Sprintf("INCOMPLETE · %d sources unavailable", len(errors)), preview: activityText(errors[0]), detail: "Sources unavailable\n" + text + "\n\nOther successful sources remain available. Previous data is retained when a source fails.", warning: true})
	}

	if m.activity.view == "events" {
		for _, event := range m.activity.tracker.Events {
			warning := strings.EqualFold(event.Type, "Warning")
			if m.activity.warningsOnly && !warning {
				continue
			}
			heading := fmt.Sprintf("%s · %s · %s · %s ×%d", activityTime(event.LastAt), event.Type, event.Reason, event.Object, event.Count)
			detail := fmt.Sprintf("%s\nReason: %s\nObject: %s\nOccurrences: %d\nFirst: %s\nLast: %s\nSource: %s\n\n%s", event.Type, event.Reason, event.Object, event.Count, activityTime(event.FirstAt), activityTime(event.LastAt), event.Source, event.Message)
			result = append(result, namespaceActivityEntry{id: "event/" + event.ID, heading: activityText(heading), preview: activityText(event.Message), detail: activityText(detail), warning: warning})
		}
	} else {
		for _, change := range m.activity.tracker.Changes() {
			origin := "Kubernetes timestamp"
			if change.Observed {
				origin = "Observed by tailg; exact change time is unknown"
			}
			heading := activityTime(change.At) + " · " + change.Action + " · " + change.Object
			detail := heading + "\nTime source: " + origin + "\n\n" + change.Detail
			result = append(result, namespaceActivityEntry{id: "change/" + change.ID, heading: activityText(heading), preview: activityText(change.Detail), detail: activityText(detail)})
		}
	}
	query := strings.ToLower(strings.TrimSpace(m.activity.filter))
	if query == "" {
		return result
	}
	filtered := make([]namespaceActivityEntry, 0, len(result))
	for _, row := range result {
		if row.id == "collection-errors" || strings.Contains(strings.ToLower(row.detail), query) {
			filtered = append(filtered, row)
		}
	}
	return filtered
}

func (m pickerModel) namespaceActivityModal() (string, int) {
	width, height := m.modalViewport()
	if width < 40 || height < 12 {
		return ansi.Hardwrap("Resize to at least 40 × 12 for namespace activity.\nEsc closes.", max(1, width), true), 0
	}
	inner := min(116, width-4) - 4
	title := "Recent changes · last 24h"
	info := "Retained timestamps + session observations; M adds log markers for displayed apps"
	updated := m.activity.tracker.LastSnapshot
	if m.activity.view == "events" {
		title = "Kubernetes events · retained by the cluster"
		info = fmt.Sprintf("Newest %d of %d events · all namespace resources", len(m.activity.tracker.Events), m.activity.tracker.EventTotal)
		if m.activity.warningsOnly {
			info += " · WARNINGS ONLY"
		}
		updated = m.activity.tracker.EventsAt
	} else if m.activity.tracker.ChangesTruncated {
		info += " · 1000-entry history cap"
	}
	collected := "not yet collected"
	if !updated.IsZero() {
		collected = updated.Local().Format("15:04:05")
	}
	status := "Collected: " + collected + " · refresh 15s"
	if m.activity.busy {
		status += " · loading"
	}
	if len(m.activity.tracker.Errors) > 0 {
		status = "INCOMPLETE · " + status
	}
	prefix := []string{headerStyle.Render(truncatePlain(title, inner)), truncatePlain(activityText(m.namespace+" · "+m.kubeContext), inner), truncatePlain(status, inner), truncatePlain(info, inner)}
	controls := "↑↓ Move · Enter Details · / Filter · R Refresh · H/E Switch · Esc Close"
	if m.activity.view == "events" {
		controls += " · W Warnings"
	}
	if inner < 60 || height < 18 {
		controls = "↑↓ Enter / R H/E W · Esc Close"
	}
	if m.activity.detail != nil {
		controls = "↑↓ PgUp/PgDn Home/End · Esc Back"
	}
	filter := "Filter: " + activityText(m.activity.filter)
	if m.activity.editing {
		filter += "▏ · Enter applies"
	}
	footer := pickerWrapLines([]string{truncatePlain(filter, inner), controls}, inner)
	prefix = pickerWrapLines(prefix, inner)
	// Keep two rows outside the centered dialog; account for its border and
	// the body status line before selecting visible entries.
	capacity := max(1, height-4-len(prefix)-len(footer)-1)
	var body []string
	maximum := 0
	if m.activity.detail != nil {
		all := strings.Split(ansi.Hardwrap(m.activity.detail.detail, inner, true), "\n")
		maximum = max(0, len(all)-capacity)
		start := min(m.activity.detailScroll, maximum)
		body = append(body, all[start:min(len(all), start+capacity)]...)
		body = append(body, fmt.Sprintf("Lines %d–%d of %d", start+1, min(len(all), start+capacity), len(all)))
	} else {
		rows := m.namespaceActivityEntries()
		if len(rows) == 0 {
			message := "No retained entries match this view."
			if m.activity.busy && m.activity.tracker.LastSnapshot.IsZero() {
				message = "Loading namespace activity…"
			}
			body = pickerWrapLines([]string{message}, inner)
		} else {
			count := max(1, capacity/2)
			selected := min(m.activity.selected, len(rows)-1)
			start := max(0, selected-count+1)
			end := min(len(rows), start+count)
			for i := start; i < end; i++ {
				marker := "  "
				if i == selected {
					marker = "> "
				}
				style := lipgloss.NewStyle()
				if rows[i].warning {
					style = warnStyle
				}
				if i == selected {
					style = selectedStyle
				}
				heading := style.Render(truncatePlain(marker+rows[i].heading, inner))
				preview := style.Render(truncatePlain("  "+strings.ReplaceAll(rows[i].preview, "\n", " "), inner))
				body = append(body, heading, preview)
			}
			body = append(body, fmt.Sprintf("Rows %d–%d of %d", start+1, end, len(rows)))
		}
	}
	content := append(prefix, body...)
	content = append(content, footer...)
	// On extremely constrained screens, keep the dialog inside the viewport.
	content = content[:min(len(content), height-2)]
	panel := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(accentColor).
		Foreground(modalForeground).Background(modalBackground).Padding(0, 1).Width(inner + 2)
	return panel.Render(strings.Join(content, "\n")), maximum
}
