package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/fernandocpaz/tailg/internal/core"
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


func TestPreparePickerAppsShowsNewestTwentyDeployments(t *testing.T) {
	base := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	apps := make([]core.AppChoice, 0, 25)
	for i := 0; i < 25; i++ {
		apps = append(apps, core.AppChoice{
			Name:       fmt.Sprintf("app-%02d", i),
			DeployedAt: base.Add(time.Duration(i) * time.Minute),
		})
	}

	got, total := preparePickerApps(apps)
	if total != 25 {
		t.Fatalf("total=%d, want 25", total)
	}
	if len(got) != maxPickerApps {
		t.Fatalf("len=%d, want %d", len(got), maxPickerApps)
	}
	if got[0].Name != "app-24" {
		t.Fatalf("first=%q, want newest deployment app-24", got[0].Name)
	}
	if got[len(got)-1].Name != "app-05" {
		t.Fatalf("last=%q, want twentieth-newest deployment app-05", got[len(got)-1].Name)
	}
	for i := 1; i < len(got); i++ {
		if got[i].DeployedAt.After(got[i-1].DeployedAt) {
			t.Fatalf("apps are not sorted newest-first at %d: %s before %s", i, got[i-1].Name, got[i].Name)
		}
	}
}

func TestPreparePickerAppsPutsUnknownDeploymentTimesLast(t *testing.T) {
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	apps := []core.AppChoice{
		{Name: "unknown"},
		{Name: "older", DeployedAt: now.Add(-time.Hour)},
		{Name: "newer", DeployedAt: now},
	}
	got, _ := preparePickerApps(apps)
	if got[0].Name != "newer" || got[1].Name != "older" || got[2].Name != "unknown" {
		t.Fatalf("unexpected order: %v, %v, %v", got[0].Name, got[1].Name, got[2].Name)
	}
}
