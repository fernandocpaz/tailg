package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Mapped ConfigMaps can be hundreds of lines long. Keep the original source
// intact for Enter-to-copy and only render the visible, wrapped viewport.
// The cached layout avoids re-wrapping large documents on every key press.
func (m *model) closeResourceDetail() {
	m.resourceDetail = ""
	m.resourceOffset = 0
	m.resourceLayout = nil
}

func (m model) mappedResourceHeight() int {
	return max(1, m.height-3) // header, separator, footer
}

func (m model) mappedResourceLines() []string {
	if m.resourceDetail == "" {
		return nil
	}
	// Reserve the rightmost terminal column for a vertical scrollbar.
	return wrapDetailText(m.resourceLayout, m.resourceDetail, "", max(1, m.width-1))
}

func (m *model) scrollMappedResource(key string) {
	lines := m.mappedResourceLines()
	height := m.mappedResourceHeight()
	limit := max(0, len(lines)-height)
	offset := min(max(0, m.resourceOffset), limit)
	page := max(1, height-1)
	switch key {
	case "up", "k":
		offset--
	case "down", "j":
		offset++
	case "pgup", "ctrl+u":
		offset -= page
	case "pgdown", "ctrl+d":
		offset += page
	case "home", "g":
		offset = 0
	case "end", "G":
		offset = limit
	}
	m.resourceOffset = min(max(0, offset), limit)
}

// Mouse wheel works when enabled in the terminal, and clicking the scrollbar
// track jumps to the approximate position in the source.
func (m *model) scrollMappedResourceMouse(msg tea.MouseMsg) {
	if msg.Action != tea.MouseActionPress {
		return
	}
	switch msg.Button {
	case tea.MouseButtonWheelUp:
		for i := 0; i < 3; i++ {
			m.scrollMappedResource("up")
		}
	case tea.MouseButtonWheelDown:
		for i := 0; i < 3; i++ {
			m.scrollMappedResource("down")
		}
	case tea.MouseButtonLeft:
		height := m.mappedResourceHeight()
		if msg.X != m.width-1 || msg.Y < 2 || msg.Y >= 2+height {
			return
		}
		limit := max(0, len(m.mappedResourceLines())-height)
		m.resourceOffset = limit * (msg.Y-2) / max(1, height-1)
	}
}

func resourceScrollIndicator(row, height, total, offset int) string {
	if total <= height {
		return " "
	}
	limit := total-height
	thumbHeight := max(1, (height*height+total-1)/total)
	thumbHeight = min(height, thumbHeight)
	start := offset * (height-thumbHeight) / max(1, limit)
	if row >= start && row < start+thumbHeight {
		return "█"
	}
	return "│"
}

func (m model) renderResourceDetail() string {
	height := m.mappedResourceHeight()
	contentWidth := max(1, m.width-1)
	lines := m.mappedResourceLines()
	limit := max(0, len(lines)-height)
	start := min(max(0, m.resourceOffset), limit)
	end := min(len(lines), start+height)

	body := make([]string, 0, height)
	for row := 0; row < height; row++ {
		text := ""
		if start+row < end {
			text = lines[start+row]
		}
		// Cell-aware width is necessary for ANSI and Unicode source text.
		padding := max(0, contentWidth-lipgloss.Width(text))
		body = append(body, text+strings.Repeat(" ", padding)+resourceScrollIndicator(row,height,len(lines),start))
	}

	position := "0/0"
	if len(lines)>0 {
		position = fmt.Sprintf("%d-%d/%d", start+1, end, len(lines))
	}
	footer := fmt.Sprintf("%s | ↑↓ Scroll | PgUp/PgDn | Home/End | Mouse wheel | Enter copy | Esc back",position)
	header := renderWithColor(headerStyle, "tailg", m.config.Formatter.Color) + "  Mapped resource"
	return strings.Join([]string{
		truncate(header, m.width),
		renderRule(m.width, m.config.Formatter.Color),
		strings.Join(body, "\n"),
		truncate(renderWithColor(dimStyle, footer, m.config.Formatter.Color),m.width),
	}, "\n")
}
