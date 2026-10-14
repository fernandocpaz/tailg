package tui

import (
	"github.com/charmbracelet/lipgloss"
	"github.com/fernandocpaz/tailg/internal/theme"
	"github.com/muesli/termenv"
)

var (
	adaptiveErrorCode string
	modalForeground   lipgloss.TerminalColor = selectionFG
	modalBackground   lipgloss.TerminalColor = selectionBG
)

// ApplyPalette is called at startup, before any Bubble Tea program runs.
func ApplyPalette(p theme.Palette) {
	adaptiveErrorCode = p.LogColors(lipgloss.ColorProfile())["red"]
	modalForeground = theme.Color(p.Foreground, p.Background, "7")
	modalBackground = theme.Fill(p.Background, "0")
	accentColor = theme.Color(p.Accent, p.Background, "3")
	positiveColor = theme.Color(p.Positive, p.Background, "2")
	mutedColor = theme.Color(p.Muted, p.Background, "7")
	infoColor = theme.Color(p.Info, p.Background, "7")
	debugColor = theme.Color(p.Debug, p.Background, "7")
	selectionBG = theme.Fill(p.SelectionBG, "3")
	selectionFG = theme.Color(p.SelectionFG, p.SelectionBG, "0")
	modeBackground = theme.Fill(p.ModeBG, "3")
	modeForeground = theme.Color(p.ModeFG, p.ModeBG, "0")
	matchBackground = theme.Fill(p.MatchBG, "3")
	matchForeground = theme.Color(p.MatchFG, p.MatchBG, "0")
	headerStyle = lipgloss.NewStyle().Bold(true).Foreground(accentColor)
	selectedStyle = lipgloss.NewStyle().Foreground(selectionFG).Background(selectionBG)
	matchStyle = lipgloss.NewStyle().Bold(true).Foreground(matchForeground).Background(matchBackground)
	filterLabelStyle = lipgloss.NewStyle().Bold(true).Foreground(positiveColor)
	modeStyle = lipgloss.NewStyle().Bold(true).Foreground(modeForeground).Background(modeBackground).Padding(0, 1)
	okStyle = lipgloss.NewStyle().Bold(true).Foreground(positiveColor)
	warnStyle = lipgloss.NewStyle().Bold(true).Foreground(accentColor)
	infoStyle = lipgloss.NewStyle().Foreground(infoColor)
	debugStyle = lipgloss.NewStyle().Foreground(debugColor)
	alertStyle = lipgloss.NewStyle().Bold(true).Foreground(theme.Color(p.Error, p.Background, "1"))
	errorLineStyle = lipgloss.NewStyle().Foreground(theme.Color(p.ErrorText, p.Background, "1"))
	warningLineStyle = lipgloss.NewStyle().Foreground(accentColor)
	infoLineStyle = lipgloss.NewStyle().Foreground(infoColor)
	debugLineStyle = lipgloss.NewStyle().Foreground(debugColor)
	dimStyle = lipgloss.NewStyle().Foreground(mutedColor)
	pickerHelpLabelStyle = lipgloss.NewStyle().Foreground(theme.Color(p.Foreground, p.Background, "7"))
	// A terminal's 16 ANSI slots can be customized arbitrarily. Invert its
	// own foreground/background for highlights rather than guess slot pairs.
	if lipgloss.ColorProfile() == termenv.ANSI {
		modalForeground, modalBackground = lipgloss.NoColor{}, lipgloss.NoColor{}
		selectedStyle = lipgloss.NewStyle().Reverse(true)
		matchStyle = lipgloss.NewStyle().Bold(true).Reverse(true)
		modeStyle = lipgloss.NewStyle().Bold(true).Reverse(true).Padding(0, 1)
	}
	pickerHelpKeyStyle = modeStyle
}
