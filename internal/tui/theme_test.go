package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/fernandocpaz/tailg/internal/core"
	"github.com/fernandocpaz/tailg/internal/theme"
	"github.com/muesli/termenv"
)

func TestAdaptivePaletteReachesLogRowsAndHighlights(t *testing.T) {
	profile, code := lipgloss.ColorProfile(), adaptiveErrorCode
	colorPointers := []*lipgloss.TerminalColor{&accentColor, &positiveColor, &mutedColor, &infoColor, &debugColor, &selectionBG, &selectionFG, &modeBackground, &modeForeground, &matchBackground, &matchForeground, &modalForeground, &modalBackground}
	oldColors := make([]lipgloss.TerminalColor, len(colorPointers))
	for i, ptr := range colorPointers {
		oldColors[i] = *ptr
	}
	stylePointers := []*lipgloss.Style{&headerStyle, &selectedStyle, &matchStyle, &filterLabelStyle, &modeStyle, &okStyle, &warnStyle, &infoStyle, &debugStyle, &alertStyle, &errorLineStyle, &warningLineStyle, &infoLineStyle, &debugLineStyle, &dimStyle, &pickerHelpLabelStyle, &pickerHelpKeyStyle}
	oldStyles := make([]lipgloss.Style, len(stylePointers))
	for i, ptr := range stylePointers {
		oldStyles[i] = *ptr
	}
	t.Cleanup(func() {
		lipgloss.SetColorProfile(profile)
		adaptiveErrorCode = code
		for i, ptr := range colorPointers {
			*ptr = oldColors[i]
		}
		for i, ptr := range stylePointers {
			*ptr = oldStyles[i]
		}
	})
	for _, profile := range []termenv.Profile{termenv.TrueColor, termenv.ANSI256, termenv.ANSI} {
		lipgloss.SetColorProfile(profile)
		p := theme.Build("#222222", "#FFFFFF")
		ApplyPalette(p)
		formatter := core.Formatter{Color: true, ColorCodes: p.LogColors(profile)}
		lines := formatter.Format("api", "api", `{"ts":"2026-10-14T12:00:00Z","level":"ERROR","message":"request failed","exception":"full exception"}`, false)
		if len(lines) != 2 {
			t.Fatalf("formatted: %v", lines)
		}
		row := renderLogRow(lines[0], "failed", false, 100, false, true)
		if !strings.Contains(row, matchStyle.Render("failed")) {
			t.Fatalf("adaptive match missing: %q", row)
		}
		row = renderLogRow(lines[1], "", false, 100, false, true)
		if !strings.Contains(row, errorLineStyle.Render("full exception")) {
			t.Fatalf("exception lost severity style: %q", row)
		}
		row = renderLogRow(lines[0], "", true, 100, false, true)
		if !strings.Contains(row, "\x1b[") || !strings.Contains(core.StripANSI(row), "ERR") {
			t.Fatalf("selection unreadable: %q", row)
		}
		if profile == termenv.ANSI && !strings.Contains(row, "7m") {
			t.Fatalf("16-color selection must invert terminal colors: %q", row)
		}
		row = renderLogRow(lines[0], "failed", true, 100, false, false)
		if row != core.StripANSI(row) {
			t.Fatal("color disabled but ANSI present")
		}
	}
}
