package tui

import (
	"strings"
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


func TestRenderPickerRowUsesColumnsWithoutRepeatedLabels(t *testing.T) {
	row := renderPickerRow(
		"> ",
		"vitas-emr-api-patient",
		"2/2",
		"Running",
		"0",
		"3h ago",
		"2d ago",
		"13571",
		16,
	)
	for _, unwanted := range []string{"ready=", "phase=", "restarts=", "started=", "deployed=", "image="} {
		if strings.Contains(row, unwanted) {
			t.Fatalf("row contains repeated label %q: %q", unwanted, row)
		}
	}
	for _, value := range []string{"vitas-emr-api-patient", "2/2", "Running", "3h ago", "2d ago", "13571"} {
		if !strings.Contains(row, value) {
			t.Fatalf("row missing %q: %q", value, row)
		}
	}
}

func TestPickerImageWidthKeepsImageColumnVisible(t *testing.T) {
	if got := pickerImageWidth(80); got < pickerImageMinWidth {
		t.Fatalf("pickerImageWidth(80) = %d, want at least %d", got, pickerImageMinWidth)
	}
}

func TestTruncatePickerCell(t *testing.T) {
	if got := truncatePickerCell("abcdefghijkl", 8); got != "abcdefg…" {
		t.Fatalf("truncatePickerCell = %q", got)
	}
}
