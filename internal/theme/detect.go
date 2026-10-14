package theme

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/term"
	"github.com/muesli/termenv"
)

func Validate(mode string) error {
	switch mode {
	case "", "auto", "light", "dark":
		return nil
	}
	return fmt.Errorf("--theme must be auto, light, or dark")
}

// Detect runs once before any UI owns terminal input. Never query pipes or
// injected readers. Unsupported terminals use COLORFGBG, then a dark fallback.
func Detect(mode string, input io.Reader, output io.Writer) (Palette, bool) {
	if mode == "light" {
		return Build("#242424", "#FFFFFF"), true
	}
	if mode == "dark" {
		return Build("#E6EDF3", "#1B1B1B"), true
	}
	in, inOK := input.(*os.File)
	out, outOK := output.(*os.File)
	if !inOK || !outOK || !term.IsTerminal(in.Fd()) || !term.IsTerminal(out.Fd()) {
		return Palette{}, false
	}
	fg, bg := environmentColors(os.Getenv("COLORFGBG"))
	if detectedFG, detectedBG, ok := terminalColors(out); ok {
		fg, bg = detectedFG, detectedBG
	}
	return Build(fg, bg), true
}

func environmentColors(value string) (string, string) {
	fg, bg := "#E6EDF3", "#1B1B1B"
	fields := strings.Split(value, ";")
	if len(fields) < 2 {
		return fg, bg
	}
	convert := func(s string, fallback string) string {
		n, err := strconv.Atoi(s)
		if err != nil || n < 0 || n > 255 {
			return fallback
		}
		return termenv.ConvertToRGB(termenv.ANSI256Color(n)).Hex()
	}
	return convert(fields[0], fg), convert(fields[len(fields)-1], bg)
}
