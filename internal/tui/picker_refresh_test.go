package tui

import (
	"errors"
	"testing"
	"time"

	"github.com/fernandocpaz/tailg/internal/core"
)

func TestPickerRefreshKeepsHighlightAndSelectionWhileAddingDeployments(t *testing.T) {
	api := core.AppChoice{Name: "api", Pods: []string{"old"}, DeployedAt: time.Unix(1, 0)}
	m := newPickerModel([]core.AppChoice{api}, 1, "qa", "apollo", []string{"api"})
	api.Pods = []string{"new"}
	other := core.AppChoice{Name: "new-app", Pods: []string{"new-app-1"}, DeployedAt: time.Unix(2, 0)}
	m.refreshApps(pickerRefreshResult{apps: []core.AppChoice{other, api}})
	if len(m.apps) != 2 || m.apps[m.index].Name != "api" || !m.checked["api"] || m.apps[m.index].Pods[0] != "new" {
		t.Fatalf("refresh lost selected application: %+v", m)
	}
	m.refreshApps(pickerRefreshResult{err: errors.New("API unavailable")})
	if len(m.apps) != 2 || m.loadError == "" {
		t.Fatal("failed refresh replaced known inventory")
	}
}

func TestPodPickerRefreshAddsPodsAndPrunesOnlyDeletedSelections(t *testing.T) {
	m := podPickerModel{
		rows:    []podPickerRow{{pod: core.PodChoice{Name: "gone"}}, {pod: core.PodChoice{Name: "keep"}}},
		checked: map[string]bool{"gone": true, "keep": true}, index: 1,
	}
	m.refreshPods(pickerRefreshResult{apps: []core.AppChoice{{Name: "api", Pods: []string{"new", "keep"}}}})
	if len(m.rows) != 2 || m.rows[m.index].pod.Name != "keep" || !m.checked["keep"] || m.checked["gone"] {
		t.Fatalf("pod refresh lost survivor or retained deleted selection: %+v", m)
	}
}
