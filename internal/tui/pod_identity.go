package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/fernandocpaz/tailg/internal/core"
)

// renderLogSource renders the source of the currently selected record. Source
// metadata on a record always wins over inventory, since the inventory can
// change while retained records remain visible.
func (m model) renderLogSource() string {
	var pod, container string
	selected := false
	if m.state != nil {
		if record, ok := m.state.SelectedRecord(m.selected); ok {
			pod, container = record.Event.Pod, record.Event.Container
			selected = pod != "" || container != ""
		}
	}

	// A waiting view has no selected record. Inventory is safe to use only when
	// it identifies one pod (and a container is safe only when it is unique).
	pods := make(map[string]struct{})
	containers := make(map[string]struct{})
	for _, item := range m.items {
		if item.Pod != "" {
			pods[item.Pod] = struct{}{}
		}
		if item.Container != "" {
			containers[item.Container] = struct{}{}
		}
	}
	if !selected && pod == "" && len(pods) == 1 {
		for value := range pods {
			pod = value
		}
		if len(containers) == 1 {
			for value := range containers {
				container = value
			}
		}
	}
	if pod == "" && container == "" {
		return ansi.Wrap("Select a log to see source", max(1, m.width), " ")
	}
	if pod == "" {
		pod = "(unknown)"
	}
	if container == "" {
		container = "(unknown)"
	}

	text := fmt.Sprintf("Pod: %s  Container: %s", pod, container)
	return ansi.Wrap(text, max(1, m.width), " ")
}

// compactPodLabel keeps both the workload name and replica suffix visible in
// the narrow source column. Width is measured in terminal cells.
func compactPodLabel(name string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(name) <= width {
		return name
	}
	if width == 1 {
		return "…"
	}

	suffix := core.PodReplicaSuffix(name)
	prefix := strings.TrimSuffix(name, suffix)
	if prefix == "" {
		prefix, suffix = name, ""
	}
	leftWidth := (width - 1 + 1) / 2
	rightWidth := width - 1 - leftWidth
	if suffix == "" {
		return truncateCells(prefix, leftWidth) + "…" + takeLastCells(prefix, rightWidth)
	}
	return truncateCells(prefix, leftWidth) + "…" + takeLastCells(suffix, rightWidth)
}

func truncateCells(value string, width int) string {
	if width <= 0 {
		return ""
	}
	var out []rune
	used := 0
	for _, r := range value {
		cell := lipgloss.Width(string(r))
		if used+cell > width {
			break
		}
		out = append(out, r)
		used += cell
	}
	return string(out)
}

func takeLastCells(value string, width int) string {
	if width <= 0 {
		return ""
	}
	runes := []rune(value)
	used := 0
	start := len(runes)
	for start > 0 {
		cell := lipgloss.Width(string(runes[start-1]))
		if used+cell > width {
			break
		}
		start--
		used += cell
	}
	return string(runes[start:])
}
