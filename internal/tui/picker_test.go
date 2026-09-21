package tui

import (
	"testing"
	"time"
)

func TestFormatPickerAge(t *testing.T) {
	now := time.Date(2026, 9, 21, 14, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		at   time.Time
		want string
	}{
		{name: "missing", want: "-"},
		{name: "just started", at: now.Add(-30 * time.Second), want: "now"},
		{name: "minutes", at: now.Add(-12 * time.Minute), want: "12m ago"},
		{name: "hours", at: now.Add(-90 * time.Minute), want: "1h ago"},
		{name: "days", at: now.Add(-49 * time.Hour), want: "2d ago"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatPickerAge(now, tt.at); got != tt.want {
				t.Fatalf("formatPickerAge = %q, want %q", got, tt.want)
			}
		})
	}
}
