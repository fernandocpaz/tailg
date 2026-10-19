package tui

import (
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

const (
	startupIntroFrameCount = 12
	startupIntroInterval   = 85 * time.Millisecond
)

type startupIntroTick struct{ frame int }

func startupIntroCmd(frame int) tea.Cmd {
	return tea.Tick(startupIntroInterval, func(time.Time) tea.Msg {
		return startupIntroTick{frame: frame}
	})
}

// Animate inside Bubble Tea's alternate screen rather than writing escape
// sequences before it starts. This keeps the shell and scrollback untouched.
func (m pickerModel) startupIntroView() string {
	width, height := m.width, m.height
	if width <= 0 {
		width = 100
	}
	if height <= 0 {
		height = 28
	}
	// Short/narrow terminals go straight to the working picker.
	if width < 45 || height < 14 {
		return m.pickerScreen()
	}

	frame := m.introFrame
	// The bird travels across the center while its stride alternates.
	shift := max(1, (width-58)/2-10+frame*2)
	bird := []string{
		"             \\  |  /",
		"              \\ | /",
		"        ___.--(o)__________________>",
		"   ____/        \\_    ___     __/",
		"  /    _    __      \\__/   \\___/",
		"  \\___/ \\__/  \\_____/",
	}
	switch frame % 3 {
	case 0:
		bird = append(bird, "           / \\     __", "      ____/   \\___/  \\_", "     /____        ______/")
	case 1:
		bird = append(bird, "           / \\        __", "      ____/   \\_   __/  \\_", "     /__        \\_/_____/" )
	default:
		bird = append(bird, "           / \\    ___", "       ___/   \\__/   \\_", "      /_____        ____/" )
	}

	var lines []string
	lines = append(lines, headerStyle.Render("tailg")+"  "+dimStyle.Render("Kubernetes log explorer"))
	lines = append(lines, "")
	lines = append(lines, "")
	for _, line := range bird {
		lines = append(lines, strings.Repeat(" ", shift)+headerStyle.Render(line))
	}
	lines = append(lines, strings.Repeat(" ", max(1, shift-6))+dimStyle.Render("..  .   .     .   ."))
	lines = append(lines, "")
	if frame >= 6 {
		lines = append(lines, warnStyle.Render("MEEP! MEEP!")+"   "+dimStyle.Render("Starting tailg..."))
	} else {
		lines = append(lines, dimStyle.Render("Starting tailg..."))
	}
	lines = append(lines, dimStyle.Render("Press any key to skip"))
	
	contentWidth := 58
	firstRow := max(0, (height-len(lines))/2)
	body := make([]string, firstRow, firstRow+len(lines))
	// Keep header centered; the runner is shifted independently.
	for i, line := range lines {
		if i < 3 || i >= len(lines)-3 {
			line = strings.Repeat(" ", max(0, (width-lipgloss.Width(line))/2)) + line
		}
		body = append(body, ansi.Truncate(line, width, ""))
	}
	_ = contentWidth
	return pickerScreenFrame(body, nil, width, height)
}
