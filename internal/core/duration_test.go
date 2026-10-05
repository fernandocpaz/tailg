package core

import "testing"

func TestNormalizeSince(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input string
		want  string
	}{
		{"days", "4d", "96h"},
		{"days and hours", "1d2h", "26h"},
		{"days and minutes", "1d1m", "86460s"},
		{"days and seconds", "1d1s", "86401s"},
		{"days and milliseconds", "1d500ms", "24h0m0.5s"},
		{"mixed precision", "1d2h3m4s5ms", "26h3m4.005s"},
		{"millisecond only with zero days", "0d1ms", "1ms"},
		{"whole second milliseconds", "1d1000ms", "86401s"},
		{"case and whitespace", " 1D500MS ", "24h0m0.5s"},
		{"no days", " 500ms ", "500ms"},
		{"invalid", " 1day ", "1day"},
		{"empty", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := NormalizeSince(tc.input); got != tc.want {
				t.Errorf("NormalizeSince(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}
