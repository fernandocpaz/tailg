package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/fernandocpaz/tailg/internal/core"
)

// overlayModal composes a centered dialog over a dimmed viewport without
// cursor-position escape sequences. Cuts are in terminal cells, not bytes, so
// styled text and wide Unicode characters cannot shift the dialog's border.
func overlayModal(background, dialog string, width, height int) string {
	width, height = max(1, width), max(1, height)
	backLines := strings.Split(core.StripANSI(background), "\n")
	frontLines := strings.Split(dialog, "\n")
	frontWidth, frontHeight := min(width, lipgloss.Width(dialog)), min(height, len(frontLines))
	x, y := (width-frontWidth)/2, (height-frontHeight)/2
	lines := make([]string, height)
	for row := range lines {
		backgroundLine := ""
		if row < len(backLines) {
			backgroundLine = backLines[row]
		}
		if row < y || row >= y+frontHeight {
			lines[row] = dimStyle.Render(modalCells(backgroundLine, 0, width))
			continue
		}
		lines[row] = dimStyle.Render(modalCells(backgroundLine, 0, x)) +
			modalCells(frontLines[row-y], 0, frontWidth) +
			dimStyle.Render(modalCells(backgroundLine, x+frontWidth, width))
	}
	return strings.Join(lines, "\n")
}

func modalCells(line string, left, right int) string {
	cut := ansi.Cut(line, left, right)
	return cut + strings.Repeat(" ", max(0, right-left-lipgloss.Width(cut)))
}
