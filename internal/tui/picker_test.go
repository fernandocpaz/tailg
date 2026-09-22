package tui

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
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

func TestAppPickerSwitchesContextEvenWithoutApplications(t *testing.T) {
	m := pickerModel{kubeContext: "tkgs-dev", namespace: "apollo"}
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	picker := updated.(pickerModel)
	if picker.result.Action != PickerSwitchContext {
		t.Fatalf("action = %v, want switch context", picker.result.Action)
	}
	if !strings.Contains(picker.View(), "tkgs-dev") || !strings.Contains(picker.View(), "apollo") {
		t.Fatalf("missing context or namespace: %q", picker.View())
	}
}

func TestAppPickerSwitchesNamespaceEvenWithoutApplications(t *testing.T) {
	m := pickerModel{kubeContext: "tkgs-dev", namespace: "apollo"}
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	if got := updated.(pickerModel).result.Action; got != PickerSwitchNamespace {
		t.Fatalf("action = %v, want switch namespace", got)
	}
}

func TestAppPickerCanRecoverFromExpiredCredentials(t *testing.T) {
	requestError := errors.New("E0922 memcache.go:265: credentials required\nerror: You must be logged in to the server")
	m := pickerModel{kubeContext: "tkgs-qa", namespace: "apollo", loadError: pickerErrorSummary(requestError)}
	view := m.View()
	if !strings.Contains(view, "You must be logged in") || strings.Contains(view, "memcache.go") {
		t.Fatalf("picker should show the actionable error: %q", view)
	}
	for _, key := range []struct {
		rune   rune
		action PickerAction
	}{{'c', PickerSwitchContext}, {'r', PickerRefresh}} {
		updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{key.rune}})
		if got := updated.(pickerModel).result.Action; got != key.action {
			t.Fatalf("%c action = %v, want %v", key.rune, got, key.action)
		}
	}
}

func TestAppPickerOpensSelectedApplication(t *testing.T) {
	m := pickerModel{apps: []core.AppChoice{{Name: "api"}, {Name: "worker"}}}
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	updated, _ = updated.(pickerModel).Update(tea.KeyMsg{Type: tea.KeyEnter})
	picker := updated.(pickerModel)
	if picker.result.Action != PickerOpenApp || picker.result.App.Name != "worker" {
		t.Fatalf("unexpected selection: %+v", picker.result)
	}
}

func TestAppPickerOpensCheckedApplicationsInsteadOfHighlighted(t *testing.T) {
	m := pickerModel{apps: []core.AppChoice{{Name: "api", Pods: []string{"api-1", "api-2"}}, {Name: "worker", Pods: []string{"worker-1"}}, {Name: "listener", Pods: []string{"listener-1"}}}}
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeySpace})
	m = updated.(pickerModel)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(pickerModel)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeySpace})
	m = updated.(pickerModel)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(pickerModel)
	if !strings.Contains(m.View(), "Selected: 2 applications · 3 current pods") {
		t.Fatalf("missing selection count: %q", m.View())
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	selected := updated.(pickerModel).result
	if selected.Action != PickerOpenApp || len(selected.Apps) != 2 || selected.Apps[0].Name != "api" || selected.Apps[1].Name != "worker" || strings.Join(selected.Marked, ",") != "api,worker" {
		t.Fatalf("unexpected selection: %+v", selected)
	}
}

func TestAppPickerEntersPodModeOnlyWithoutCheckedApps(t *testing.T) {
	m := pickerModel{apps: []core.AppChoice{{Name: "api"}}}
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeySpace})
	updated, _ = updated.(pickerModel).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}})
	if got := updated.(pickerModel); got.result.Action == PickerSelectPods || got.note == "" {
		t.Fatalf("checked apps should be retained: %+v", got)
	}
	updated, _ = updated.(pickerModel).Update(tea.KeyMsg{Type: tea.KeySpace})
	updated, _ = updated.(pickerModel).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}})
	if got := updated.(pickerModel).result.Action; got != PickerSelectPods {
		t.Fatalf("pod action = %v", got)
	}
}

func TestAppPickerRestoresOnlyVisibleSelections(t *testing.T) {
	apps := []core.AppChoice{{Name: "api"}, {Name: "worker"}}
	m := newPickerModel(apps, len(apps), "tkgs-qa", "apollo", []string{"worker", "old-pod"})
	if choices := m.checkedChoices(); len(choices) != 1 || choices[0].Name != "worker" {
		t.Fatalf("restored choices = %+v", choices)
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
