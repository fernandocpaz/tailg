package kube

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
	"github.com/charmbracelet/x/ansi"
)

type statusVisuals struct {
	Decorated bool
	Color     bool
	Width     int
}

type tableTone int

const (
	toneNeutral tableTone = iota
	toneHealthy
	toneAlert
)

var asciiTableBorder = lipgloss.Border{
	Top: "-", Bottom: "-", Left: "|", Right: "|",
	TopLeft: "+", TopRight: "+", BottomLeft: "+", BottomRight: "+",
	MiddleLeft: "+", MiddleRight: "+", Middle: "+", MiddleTop: "+", MiddleBottom: "+",
}

var (
	statusGreen = lipgloss.AdaptiveColor{Light: "#006B3C", Dark: "#5AF78E"}
	statusRed   = lipgloss.AdaptiveColor{Light: "#B00020", Dark: "#FF5F5F"}
	statusGold  = lipgloss.AdaptiveColor{Light: "#7A5200", Dark: "#FFD75F"}
	statusAccent = lipgloss.AdaptiveColor{Light: "#7A4E00", Dark: "#FFD166"}
	statusMuted = lipgloss.AdaptiveColor{Light: "#5F6368", Dark: "#A0A0A0"}
)

// formatTable preserves the plain ASCII API used by non-interactive callers.
func formatTable(headers []string, rows [][]string, maximums []int) string {
	return formatTableStyled(headers, rows, maximums, statusVisuals{}, toneNeutral, true)
}

func formatTableStyled(headers []string, rows [][]string, maximums []int, visuals statusVisuals, tone tableTone, separateRows bool) string {
	prepared := make([][]string, len(rows))
	for row := range rows {
		prepared[row] = make([]string, len(rows[row]))
		for column, value := range rows[row] {
			if column < len(maximums) && maximums[column] > 0 {
				value = ansi.Wrap(value, maximums[column], "")
			}
			prepared[row][column] = value
		}
	}

	border := asciiTableBorder
	if visuals.Decorated {
		border = lipgloss.RoundedBorder()
	}
	rendered := table.New().
		Border(border).
		BorderRow(separateRows).
		Headers(headers...).
		Rows(prepared...).
		Wrap(true).
		StyleFunc(func(row, column int) lipgloss.Style {
			style := lipgloss.NewStyle().Padding(0, 1)
			if !visuals.Color {
				return style
			}
			if row == table.HeaderRow {
				return style.Bold(true).Foreground(statusAccent)
			}
			value := ""
			if row >= 0 && row < len(prepared) && column < len(prepared[row]) {
				value = strings.ToUpper(strings.TrimSpace(prepared[row][column]))
			}
			switch {
			case value == "OK" || value == "HEALTHY":
				return style.Bold(true).Foreground(statusGreen)
			case value == "ALERT" || strings.Contains(value, "ERROR") || strings.Contains(value, "TIMEOUT") || strings.Contains(value, "FAILURE"):
				return style.Bold(true).Foreground(statusRed)
			case value == "WARN" || value == "WARNING" || value == "PENDING":
				return style.Bold(true).Foreground(statusGold)
			case tone == toneAlert && column == 0:
				return style.Bold(true).Foreground(statusRed)
			case tone == toneHealthy && column == 0:
				return style.Bold(true).Foreground(statusGreen)
			default:
				return style
			}
		})
	if visuals.Color {
		rendered.BorderStyle(lipgloss.NewStyle().Foreground(statusMuted))
	}
	if visuals.Decorated && visuals.Width > 0 {
		rendered.Width(max(36, visuals.Width))
	}
	return rendered.String() + "\n"
}

func statusSection(title string, visuals statusVisuals, tone tableTone) string {
	if !visuals.Decorated {
		return title + "\n"
	}
	style := lipgloss.NewStyle().Bold(true)
	if visuals.Color {
		switch tone {
		case toneHealthy:
			style = style.Foreground(statusGreen)
		case toneAlert:
			style = style.Foreground(statusRed)
		default:
			style = style.Foreground(statusAccent)
		}
	}
	return style.Render("● "+title) + "\n"
}

func statusMetadata(value string, visuals statusVisuals) string {
	width := 118
	if visuals.Width > 0 {
		width = max(36, visuals.Width)
	}
	value = ansi.Wrap(value, width, "")
	if visuals.Color {
		value = lipgloss.NewStyle().Foreground(statusMuted).Render(value)
	}
	return value + "\n"
}

func narrowStatusLayout(visuals statusVisuals) bool {
	return visuals.Decorated && visuals.Width > 0 && visuals.Width < 90
}
