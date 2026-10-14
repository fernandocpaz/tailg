// Package theme selects semantic colors against the terminal's default colors.
package theme

import (
	"math"
	"strconv"

	"github.com/charmbracelet/lipgloss"
	"github.com/lucasb-eyer/go-colorful"
	"github.com/muesli/termenv"
)

type Palette struct {
	Foreground, Background                                     string
	Accent, Positive, Error, ErrorText, Muted, Info, Debug     string
	SelectionBG, SelectionFG, MatchBG, MatchFG, ModeBG, ModeFG string
}

// Build preserves severity hues and adjusts brightness until text has at least
// 4.5:1 contrast. Highlight fills also contrast with the terminal surface.
func Build(foreground, background string) Palette {
	bg, err := colorful.Hex(background)
	if err != nil {
		bg = rgb("#1B1B1B")
	}
	fg, err := colorful.Hex(foreground)
	if err != nil {
		fg = opposite(bg)
	}
	fg = readable(fg, bg, 4.5)
	dark := luminance(bg) < 0.179
	accent, positive, alert, errorText := "#7A4E00", "#176B3A", "#B42318", "#B42318"
	if dark {
		accent, positive, alert, errorText = "#FFD166", "#7EE787", "#FF6B6B", "#FF7B72"
	}
	a := readable(rgb(accent), bg, 4.5)
	selected := readable(bg.BlendRgb(a, 0.22), bg, 1.5)
	match := readable(rgb("#FFE66D"), bg, 3)
	p := Palette{
		Foreground: fg.Hex(), Background: bg.Hex(), Accent: a.Hex(),
		Positive: readable(rgb(positive), bg, 4.5).Hex(),
		Error:    readable(rgb(alert), bg, 4.5).Hex(), ErrorText: readable(rgb(errorText), bg, 4.5).Hex(),
		Muted:       readable(fg.BlendRgb(bg, 0.35), bg, 4.5).Hex(),
		Info:        readable(fg.BlendRgb(bg, 0.45), bg, 4.5).Hex(),
		Debug:       readable(fg.BlendRgb(bg, 0.55), bg, 4.5).Hex(),
		SelectionBG: selected.Hex(), SelectionFG: readable(fg, selected, 4.5).Hex(),
		MatchBG: match.Hex(), MatchFG: opposite(match).Hex(),
		ModeBG: a.Hex(), ModeFG: opposite(a).Hex(),
	}
	return p
}

func rgb(hex string) colorful.Color { c, _ := colorful.Hex(hex); return c }
func luminance(c colorful.Color) float64 {
	linear := func(v float64) float64 {
		if v <= 0.04045 {
			return v / 12.92
		}
		return math.Pow((v+0.055)/1.055, 2.4)
	}
	return 0.2126*linear(c.R) + 0.7152*linear(c.G) + 0.0722*linear(c.B)
}
func Contrast(a, b string) float64 { return contrast(rgb(a), rgb(b)) }
func contrast(a, b colorful.Color) float64 {
	x, y := luminance(a), luminance(b)
	if x < y {
		x, y = y, x
	}
	return (x + 0.05) / (y + 0.05)
}
func opposite(bg colorful.Color) colorful.Color {
	black, white := rgb("#000000"), rgb("#FFFFFF")
	if contrast(black, bg) > contrast(white, bg) {
		return black
	}
	return white
}
func readable(c, bg colorful.Color, minimum float64) colorful.Color {
	// Test rounded RGB values because those are what terminals actually receive.
	if Contrast(c.Hex(), bg.Hex()) >= minimum {
		return c
	}
	end := opposite(bg)
	for i := 1; i <= 100; i++ {
		candidate := c.BlendRgb(end, float64(i)/100)
		if Contrast(candidate.Hex(), bg.Hex()) >= minimum {
			return candidate
		}
	}
	return end
}

// Color supplies explicit ANSI fallbacks. The ANSI256 foreground is chosen
// against the quantized background; terminal-defined 16-color slots cannot
// provide a numeric contrast guarantee.
func Color(hex, background, ansi string) lipgloss.CompleteColor {
	target, bg := rgb(hex), rgb(background)
	quantized := termenv.ANSI256.Color(background)
	if quantized != nil {
		bg = termenv.ConvertToRGB(quantized)
	}
	best, distance := "", math.Inf(1)
	for i := 16; i < 256; i++ {
		c := termenv.ConvertToRGB(termenv.ANSI256Color(i))
		if contrast(c, bg) < 4.5 {
			continue
		}
		d := target.DistanceLab(c)
		if d < distance {
			distance, best = d, strconv.Itoa(i)
		}
	}
	return lipgloss.CompleteColor{TrueColor: hex, ANSI256: best, ANSI: ansi}
}

// Fill allows lower contrast for background fills than for text.
func Fill(hex string, ansi string) lipgloss.CompleteColor {
	return lipgloss.CompleteColor{TrueColor: hex, ANSI256: strconv.Itoa(int(termenv.ANSI256.Color(hex).(termenv.ANSI256Color))), ANSI: ansi}
}

// LogColors shares the UI palette with direct log streaming.
func (p Palette) LogColors(profile termenv.Profile) map[string]string {
	result := make(map[string]string)
	for role, entry := range map[string]struct{ hex, ansi string }{
		"red": {p.ErrorText, "1"}, "yellow": {p.Accent, "3"},
		"info": {p.Info, "7"}, "dim": {p.Debug, "7"},
	} {
		c := Color(entry.hex, p.Background, entry.ansi)
		var color termenv.Color
		switch profile {
		case termenv.TrueColor:
			color = profile.Color(c.TrueColor)
		case termenv.ANSI256:
			color = profile.Color(c.ANSI256)
		case termenv.ANSI:
			color = profile.Color(c.ANSI)
		}
		if color != nil {
			result[role] = "\x1b[" + color.Sequence(false) + "m"
		}
	}
	return result
}
