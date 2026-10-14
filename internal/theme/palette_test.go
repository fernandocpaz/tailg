package theme

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/muesli/termenv"
)

func TestPaletteContrastOnCustomTerminalColors(t *testing.T) {
	for _, bg := range []string{"#000000", "#FFFFFF", "#808080", "#FFD166", "#302A1F", "#001F44", "#FF6B6B", "#008080"} {
		for _, fg := range []string{"#E6EDF3", "#101010", bg, "#00FF00"} {
			p := Build(fg, bg)
			for role, pair := range map[string][2]string{
				"foreground": {p.Foreground, bg}, "error": {p.Error, bg}, "error text": {p.ErrorText, bg},
				"warning": {p.Accent, bg}, "positive": {p.Positive, bg}, "muted": {p.Muted, bg},
				"info": {p.Info, bg}, "debug": {p.Debug, bg}, "selection": {p.SelectionFG, p.SelectionBG},
				"match": {p.MatchFG, p.MatchBG}, "mode": {p.ModeFG, p.ModeBG},
			} {
				if ratio := Contrast(pair[0], pair[1]); ratio < 4.5 {
					t.Errorf("%s on %s with %s: contrast %.2f", role, bg, fg, ratio)
				}
				c := Color(pair[0], pair[1], "7")
				quantizedBG := termenv.ConvertToRGB(termenv.ANSI256.Color(pair[1])).Hex()
				quantizedFG := termenv.ConvertToRGB(termenv.ANSI256.Color(c.ANSI256)).Hex()
				if ratio := Contrast(quantizedFG, quantizedBG); ratio < 4.5 {
					t.Errorf("256-color %s on %s: %.2f", role, bg, ratio)
				}
			}
			if Contrast(p.SelectionBG, bg) < 1.5 {
				t.Errorf("selection invisible on %s", bg)
			}
			if Contrast(p.MatchBG, bg) < 3 {
				t.Errorf("match invisible on %s", bg)
			}
		}
	}
}

func TestDetectFallbackAndOverrides(t *testing.T) {
	var output bytes.Buffer
	if _, ok := Detect("auto", &bytes.Buffer{}, &output); ok {
		t.Fatal("auto detection must skip non-terminal input/output")
	}
	if output.Len() != 0 {
		t.Fatal("query written to a pipe")
	}
	for _, mode := range []string{"light", "dark"} {
		p, ok := Detect(mode, &bytes.Buffer{}, &output)
		if !ok || Contrast(p.Foreground, p.Background) < 4.5 {
			t.Errorf("invalid %s override", mode)
		}
	}
	for _, mode := range []string{"", "auto", "light", "dark"} {
		if err := Validate(mode); err != nil {
			t.Fatal(err)
		}
	}
	if Validate("banana") == nil {
		t.Fatal("invalid theme accepted")
	}
	fg, bg := environmentColors("0;8;15")
	if fg != "#000000" || bg != "#ffffff" {
		t.Fatalf("COLORFGBG: %s %s", fg, bg)
	}
	fg, bg = environmentColors("invalid;-1")
	if fg != "#E6EDF3" || bg != "#1B1B1B" {
		t.Fatal("malformed fallback")
	}
}

func TestDirectLogSequences(t *testing.T) {
	p := Build("#000000", "#FFFFFF")
	for _, profile := range []termenv.Profile{termenv.TrueColor, termenv.ANSI256, termenv.ANSI, termenv.Ascii} {
		codes := p.LogColors(profile)
		for _, role := range []string{"red", "yellow", "info", "dim"} {
			if profile == termenv.Ascii && codes[role] != "" {
				t.Fatal("ANSI in plain output")
			}
			if profile != termenv.Ascii && codes[role] == "" {
				t.Fatal(fmt.Sprintf("missing %s in profile %d", role, profile))
			}
		}
	}
}
