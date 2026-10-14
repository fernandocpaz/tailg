//go:build !windows

package theme

import (
	"os"

	"github.com/muesli/termenv"
)

func terminalColors(out *os.File) (string, string, bool) {
	// termenv uses bounded OSC 10/11 queries, restores terminal modes, and caches
	// responses. Its ANSI fallbacks are not measured RGB values.
	output := termenv.NewOutput(out, termenv.WithColorCache(true))
	fg, fgOK := output.ForegroundColor().(termenv.RGBColor)
	bg, bgOK := output.BackgroundColor().(termenv.RGBColor)
	if !bgOK {
		return "", "", false
	}
	if !fgOK {
		fallback, _ := environmentColors(os.Getenv("COLORFGBG"))
		fg = termenv.RGBColor(fallback)
	}
	return string(fg), string(bg), true
}
