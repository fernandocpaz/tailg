package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/fernandocpaz/tailg/internal/core"
)

const quietStreamAfter = 30 * time.Second

type streamFreshness struct {
	item         core.InventoryItem
	lastReceived time.Time
	lastLog      time.Time
	awaiting     bool
	replayed     uint64
}

func (m *model) recordFreshness(event core.LogEvent) {
	if m.freshness == nil {
		m.freshness = make(map[string]streamFreshness)
	}
	key := streamKey(event.Pod, event.Container)
	state := m.freshness[key]
	state.item = core.InventoryItem{Pod: event.Pod, Container: event.Container}
	if event.Started || event.Closed {
		state.awaiting = true
	} else {
		state.awaiting = false
		state.lastReceived = event.ReceivedAt
		if state.lastReceived.IsZero() {
			state.lastReceived = time.Now()
		}
		// Report the timestamp of the last line received, including historical
		// catch-up. Receipt time alone does not prove that logs are up to date.
		state.lastLog = event.ObservedAt
		if event.Replayed {
			state.replayed++
		}
	}
	m.freshness[key] = state
}

func (m *model) pruneFreshness(items []core.InventoryItem) {
	wanted := make(map[string]bool, len(items))
	for _, item := range items {
		wanted[item.Key()] = true
	}
	for key := range m.freshness {
		if !wanted[key] {
			delete(m.freshness, key)
		}
	}
}

func (m model) freshnessItems() []core.InventoryItem {
	items := append([]core.InventoryItem(nil), m.items...)
	// Unit models and events arriving before the inventory update still have a
	// useful identity. Inventory updates subsequently prune removed streams.
	seen := make(map[string]bool, len(items))
	for _, item := range items {
		seen[item.Key()] = true
	}
	for key, state := range m.freshness {
		if !seen[key] {
			items = append(items, state.item)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Key() < items[j].Key() })
	return items
}

func (m model) waitingStreams() int {
	count := 0
	for _, item := range m.freshnessItems() {
		state := m.freshness[item.Key()]
		if state.awaiting || state.lastReceived.IsZero() {
			count++
		}
	}
	return count
}

func (m model) freshnessSummary(now time.Time) string {
	items := m.freshnessItems()
	if len(items) == 1 {
		state := m.freshness[items[0].Key()]
		if state.lastReceived.IsZero() {
			return " · no logs yet"
		}
		quiet := ""
		if !state.awaiting && now.Sub(state.lastReceived) >= quietStreamAfter {
			quiet = "QUIET · "
		}
		return " · " + quiet + "recv " + freshnessAge(state.lastReceived, now) + " · log " + freshnessAge(state.lastLog, now)
	}
	if len(items) == 0 {
		return ""
	}
	quiet := 0
	for _, item := range items {
		state := m.freshness[item.Key()]
		if !state.awaiting && !state.lastReceived.IsZero() && now.Sub(state.lastReceived) >= quietStreamAfter {
			quiet++
		}
	}
	return fmt.Sprintf(" · %d quiet · F4 streams", quiet)
}

func freshnessAge(at, now time.Time) string {
	if at.IsZero() {
		return "unknown"
	}
	if at.After(now) {
		return "clock+" + at.Sub(now).Round(time.Second).String()
	}
	return now.Sub(at).Truncate(time.Second).String()
}

func (m model) freshnessStatus(item core.InventoryItem, now time.Time) string {
	if _, ok := m.reconnecting[item.Key()]; ok {
		return "RECONNECTING"
	}
	state := m.freshness[item.Key()]
	if state.awaiting || state.lastReceived.IsZero() {
		return "WAITING"
	}
	if now.Sub(state.lastReceived) >= quietStreamAfter {
		return "QUIET"
	}
	return "RECEIVING"
}

func (m model) renderFreshness(now time.Time) string {
	items := m.freshnessItems()
	used, limit := m.state.BufferUsage()
	lines := []string{
		fmt.Sprintf("%d streams | queued %d | live buffer %d/%d lines", len(items), len(m.events), used, limit),
		"recv = time since receipt; log = age of last line; quiet does not mean unhealthy",
	}
	if m.config.Version != "" {
		lines = append(lines, "Build: "+m.config.Version)
	}
	height := max(1, m.height-4-len(lines))
	start := min(m.freshnessIndex, max(0, len(items)-1))
	for _, item := range items[start:min(len(items), start+height)] {
		state := m.freshness[item.Key()]
		identityWidth := max(10, m.width-68)
		identity := truncatePlain(item.Pod+"/"+item.Container, identityWidth)
		lines = append(lines, fmt.Sprintf("%s  %s | recv %s | log %s | replayed %d", identity, m.freshnessStatus(item, now), freshnessAge(state.lastReceived, now), freshnessAge(state.lastLog, now), state.replayed))
	}
	if len(items) == 0 {
		lines = append(lines, "Waiting for pod inventory...")
	}
	for i := range lines {
		lines[i] = truncatePlain(lines[i], m.width)
	}
	return m.panel("Log stream freshness", strings.Join(lines, "\n"), "Up/Down scroll | F4/Esc closes")
}

func (m model) updateFreshnessKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "f4":
		m.freshnessOpen = false
	case "up":
		m.freshnessIndex--
	case "down":
		m.freshnessIndex++
	case "pgup":
		m.freshnessIndex -= max(1, m.height-6)
	case "pgdown":
		m.freshnessIndex += max(1, m.height-6)
	}
	m.freshnessIndex = max(0, min(m.freshnessIndex, len(m.freshnessItems())-1))
	return m, nil
}

func waitForInventory(updates <-chan inventoryMsg) tea.Cmd {
	if updates == nil {
		return nil
	}
	return func() tea.Msg {
		update, ok := <-updates
		if !ok {
			return nil
		}
		return update
	}
}
