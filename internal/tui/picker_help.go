package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

type pickerShortcut struct {
	key   string
	label string
}

var (
	pickerHelpLabelStyle = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{
		Light: "#34373C",
		Dark:  "#E6EDF3",
	})
	pickerHelpKeyStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(modeForeground).
				Background(modeBackground).
				Padding(0, 1)
)

func shortcut(key, label string) pickerShortcut {
	return pickerShortcut{key: key, label: label}
}

func renderPickerShortcuts(shortcuts ...pickerShortcut) string {
	parts := make([]string, 0, len(shortcuts))
	for _, item := range shortcuts {
		parts = append(parts, pickerHelpKeyStyle.Render(item.key)+" "+pickerHelpLabelStyle.Render(item.label))
	}
	return strings.Join(parts, dimStyle.Render("  ·  "))
}
