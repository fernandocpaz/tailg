package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/fernandocpaz/tailg/internal/core"
)

const replicaRefreshInterval = 30 * time.Second

type replicaViewState struct {
	open, loading      bool
	pod                string
	info               core.ReplicaExplanation
	notice             string
	fetched            time.Time
	generation, offset int
	searches           *searchController
}

type replicaMsg struct {
	pod        string
	generation int
	info       core.ReplicaExplanation
	err        error
}

func (m model) selectedReplicaPod() string {
	if m.state != nil {
		if record, ok := m.state.SelectedRecord(m.selected); ok && record.Event.Pod != "" {
			return record.Event.Pod
		}
	}
	pods := core.UniquePods(m.items)
	if len(pods) == 1 {
		return pods[0]
	}
	return ""
}

// Background lookups are limited to single-pod views; multi-pod views require
// F7 on a selected log, avoiding repeated API calls as the live selection moves.
func (m *model) ensureReplicaLookup(now time.Time) tea.Cmd {
	if m.config.ExplainReplicas == nil {
		return nil
	}
	target := ""
	if m.replicas.open {
		target = m.replicas.pod
	} else if len(core.UniquePods(m.items)) == 1 {
		target = m.selectedReplicaPod()
	}
	if target == "" {
		return nil
	}
	if m.replicas.pod == target && (m.replicas.loading || (!m.replicas.fetched.IsZero() && now.Sub(m.replicas.fetched) < replicaRefreshInterval)) {
		return nil
	}
	return m.startReplicaLookup(target)
}

func (m *model) startReplicaLookup(pod string) tea.Cmd {
	if m.config.ExplainReplicas == nil || pod == "" {
		return nil
	}
	if m.replicas.searches == nil {
		m.replicas.searches = &searchController{}
	}
	parent := m.ctx
	if parent == nil {
		parent = context.Background()
	}
	ctx := m.replicas.searches.start(parent)
	m.replicas.generation++
	generation := m.replicas.generation
	if m.replicas.pod != pod {
		m.replicas.info = core.ReplicaExplanation{}
		m.replicas.fetched = time.Time{}
		m.replicas.offset = 0
	}
	m.replicas.pod = pod
	m.replicas.loading = true
	m.replicas.notice = ""
	lookup := m.config.ExplainReplicas
	return func() tea.Msg {
		limited, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		info, err := lookup(limited, pod)
		if ctx.Err() != nil {
			return nil
		}
		return replicaMsg{pod: pod, generation: generation, info: info, err: err}
	}
}

func (m *model) openReplicaExplanation() tea.Cmd {
	pod := m.selectedReplicaPod()
	if pod == "" {
		m.notice = "Select a log with a known pod to explain replicas"
		return nil
	}
	if m.config.ExplainReplicas == nil {
		m.notice = "Replica information is unavailable in this view"
		return nil
	}
	m.replicas.open = true
	m.replicas.offset = 0
	if m.replicas.pod == pod && (m.replicas.loading || (!m.replicas.fetched.IsZero() && time.Since(m.replicas.fetched) < replicaRefreshInterval)) {
		return nil
	}
	return m.startReplicaLookup(pod)
}

func (m model) updateReplicaResult(msg replicaMsg) (tea.Model, tea.Cmd) {
	if msg.pod != m.replicas.pod || msg.generation != m.replicas.generation {
		return m, nil
	}
	m.replicas.loading = false
	m.replicas.fetched = time.Now()
	m.replicas.info = msg.info
	m.replicas.notice = ""
	if msg.err != nil {
		m.replicas.notice = msg.err.Error()
	}
	return m, nil
}

func (m model) renderReplicaSummary() string {
	if m.config.ExplainReplicas == nil {
		return ""
	}
	if m.selectedReplicaPod() != m.replicas.pod || m.replicas.pod == "" {
		return "Replicas: F7 explains the selected pod"
	}
	if m.replicas.loading {
		return "Replicas: checking workload and autoscaling... | F7 details"
	}
	if m.replicas.notice != "" {
		return "Replicas: lookup incomplete | F7 details"
	}
	if m.replicas.info.Summary == "" {
		return "Replicas: explanation unavailable | F7 details"
	}
	suffix := " | F7 details"
	if len(m.replicas.info.Warnings) > 0 {
		suffix = " | incomplete lookup; F7 details"
	}
	return "Replicas: " + m.replicas.info.Summary + suffix
}

func (m model) replicaExplanationLines() []string {
	info := m.replicas.info
	lines := []string{"Pod: " + m.replicas.pod}
	if m.replicas.loading {
		lines = append(lines, "Refreshing workload and autoscaler information...")
	}
	if info.Workload != "" {
		lines = append(lines, "Workload: "+info.Workload)
	}
	if !m.replicas.fetched.IsZero() {
		lines = append(lines, "Observed: "+m.replicas.fetched.Local().Format(time.RFC3339))
	}
	if info.Summary != "" {
		lines = append(lines, "", info.Summary)
	}
	lines = append(lines, info.Details...)
	for _, warning := range info.Warnings {
		lines = append(lines, "Incomplete: "+warning)
	}
	if m.replicas.notice != "" {
		lines = append(lines, "Lookup incomplete: "+m.replicas.notice)
	}
	return strings.Split(ansi.Wrap(strings.Join(lines, "\n"), max(1, m.width), ""), "\n")
}

func (m model) replicaFooter() string {
	return ansi.Wrap("Up/Down or PgUp/PgDn scroll | R refresh | F7/Esc closes", max(1, m.width), "")
}

func (m model) replicaHeight() int {
	return max(1, m.height-2-len(strings.Split(m.replicaFooter(), "\n")))
}

func (m model) renderReplicaExplanation() string {
	lines := m.replicaExplanationLines()
	height := m.replicaHeight()
	start := min(max(0, m.replicas.offset), max(0, len(lines)-height))
	end := min(len(lines), start+height)
	visible := append([]string(nil), lines[start:end]...)
	for len(visible) < height {
		visible = append(visible, "")
	}
	header := fmt.Sprintf("Why replicas? | lines %d-%d/%d", start+1, end, len(lines))
	return strings.Join([]string{truncatePlain(header, m.width), renderRule(m.width, m.config.Formatter.Color), strings.Join(visible, "\n"), m.replicaFooter()}, "\n")
}

func (m model) updateReplicaKey(key string) (tea.Model, tea.Cmd) {
	limit := max(0, len(m.replicaExplanationLines())-m.replicaHeight())
	m.replicas.offset = min(max(0, m.replicas.offset), limit)
	switch key {
	case "f7":
		m.replicas.open = false
	case "r":
		cmd := m.startReplicaLookup(m.replicas.pod)
		return m, cmd
	case "up":
		m.replicas.offset--
	case "down":
		m.replicas.offset++
	case "pgup":
		m.replicas.offset -= max(1, m.replicaHeight()-1)
	case "pgdown":
		m.replicas.offset += max(1, m.replicaHeight()-1)
	}
	m.replicas.offset = min(max(0, m.replicas.offset), limit)
	return m, nil
}
