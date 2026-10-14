package tui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

type pickerViewportRow struct {
	lines   []string
	section int
}

func pickerWrapLines(lines []string, width int) []string {
	if width <= 0 {
		return lines
	}
	var wrapped []string
	for _, line := range lines {
		wrapped = append(wrapped, strings.Split(ansi.Hardwrap(line, width, true), "\n")...)
	}
	return wrapped
}

// pickerViewportRange fills the available terminal height with complete rows.
// Section headers count only when present; a scroll hint costs space only when
// the full inventory cannot fit. Expand around the cursor without requiring a
// user to highlight the last row just to reveal it.
func pickerViewportRange(rows []pickerViewportRow, headers [2][]string, selected, budget, scrollHeight int, bounded bool) (int, int) {
	if len(rows) == 0 || !bounded {
		return 0, len(rows)
	}
	height := 0
	for i, row := range rows {
		if i == 0 || row.section != rows[i-1].section {
			height += len(headers[row.section])
		}
		height += len(row.lines)
	}
	if height <= budget {
		return 0, len(rows)
	}
	budget -= scrollHeight
	selected = min(max(0, selected), len(rows)-1)
	start, end := selected, selected+1
	height = len(rows[selected].lines) + len(headers[rows[selected].section])
	for start > 0 {
		cost := len(rows[start-1].lines)
		if rows[start-1].section != rows[start].section {
			cost += len(headers[rows[start-1].section])
		}
		if height+cost > budget {
			break
		}
		height += cost
		start--
	}
	for end < len(rows) {
		cost := len(rows[end].lines)
		if rows[end].section != rows[end-1].section {
			cost += len(headers[rows[end].section])
		}
		if height+cost > budget {
			break
		}
		height += cost
		end++
	}
	return start, end
}
