package tui

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/fernandocpaz/tailg/internal/core"
)

func toastApp(name, tag string, deployed int64, pods ...string) core.AppChoice {
	return core.AppChoice{Name: name, ImageTag: tag, DeployedAt: time.Unix(deployed, 0), Pods: pods}
}

func toastMonitor(t *testing.T, apps []core.AppChoice) pickerModel {
	t.Helper()
	m := newPickerModel(apps, len(apps), "tkgs-qa", "apollo", nil)
	m.width, m.height = 120, 30
	updated, _ := m.Update(monitorKey('m'))
	m = updated.(pickerModel)
	if !m.monitorEnabled || len(m.deploymentToasts) != 0 {
		t.Fatal("monitor should start with a silent inventory baseline")
	}
	t.Cleanup(func() { m.monitorCancel() })
	return m
}

func toastRefresh(m pickerModel, apps []core.AppChoice, err error) (pickerModel, tea.Cmd) {
	m.inventoryBusy = true
	updated, cmd := m.Update(pickerRefreshResult{
		scope: activityScope(m.kubeContext, m.namespace), request: m.inventoryRequest,
		apps: apps, err: err,
	})
	return updated.(pickerModel), cmd
}

func TestDeploymentToastOnlyForNewRolloutNotRestartOrInitialInventory(t *testing.T) {
	first := toastApp("api", "v1", 100, "api-old")
	m := toastMonitor(t, []core.AppChoice{first})
	m, _ = toastRefresh(m, []core.AppChoice{first}, nil)
	if len(m.deploymentToasts) != 0 {
		t.Fatal("first refresh of existing workloads must not notify")
	}
	restarted := first
	restarted.Pods = []string{"api-restarted"}
	restarted.StartedAt = time.Unix(200, 0)
	m, _ = toastRefresh(m, []core.AppChoice{restarted}, nil)
	if len(m.deploymentToasts) != 0 {
		t.Fatal("ordinary pod restart must not be treated as a rollout")
	}
	sameTagRollout := toastApp("api", "v1", 300, "api-new")
	m, cmd := toastRefresh(m, []core.AppChoice{sameTagRollout}, nil)
	if len(m.deploymentToasts) != 1 || cmd == nil {
		t.Fatalf("new ReplicaSet with same image tag must notify: %+v", m.deploymentToasts)
	}
	if !strings.Contains(m.deploymentToasts[0].detail, "New rollout") {
		t.Fatal("toast must identify a rollout, not a pod restart")
	}
	view := ansi.Strip(m.View())
	for _, field := range []string{"NEW DEPLOYMENT", "api", "tkgs-qa / apollo", "v1"} {
		if !strings.Contains(view, field) {
			t.Fatalf("toast missing %q: %s", field, view)
		}
	}
	m, _ = toastRefresh(m, []core.AppChoice{sameTagRollout}, nil)
	if len(m.deploymentToasts) != 1 {
		t.Fatal("identical refresh must not generate duplicate notifications")
	}
}

func TestDeploymentToastTracksFullInventoryAndIgnoresCronJobs(t *testing.T) {
	old := make([]core.AppChoice, 0, maxPickerApps+2)
	for i := 0; i < maxPickerApps+1; i++ {
		old = append(old, toastApp(fmt.Sprintf("app-%02d", i), "v1", 100, "pod"))
	}
	m := toastMonitor(t, old)
	current := append([]core.AppChoice(nil), old...)
	current[maxPickerApps] = toastApp(fmt.Sprintf("app-%02d", maxPickerApps), "v2", 200, "new-pod")
	current = append(current, toastApp("brand-new-app", "v1", 200, "new"))
	current = append(current, core.AppChoice{Name: "cronjob/nightly", CronJob: &core.CronJobInfo{}})
	m, _ = toastRefresh(m, current, nil)
	if len(m.deploymentToasts) != 2 {
		t.Fatalf("expected two new deployments including one outside newest-20 view, got %+v", m.deploymentToasts)
	}
	if m.deploymentToasts[0].name != fmt.Sprintf("app-%02d", maxPickerApps) || m.deploymentToasts[1].name != "brand-new-app" {
		t.Fatalf("unexpected toast names: %+v", m.deploymentToasts)
	}
	if !strings.Contains(m.deploymentToasts[0].detail, "v1 → v2") {
		t.Fatalf("missing old/new version: %+v", m.deploymentToasts[0])
	}
}

func TestDeploymentToastFailedInventoryDoesNotChangeBaseline(t *testing.T) {
	old := toastApp("api", "v1", 100, "pod")
	m := toastMonitor(t, []core.AppChoice{old})
	m, _ = toastRefresh(m, nil, errors.New("kubectl unavailable"))
	if len(m.deploymentToasts) != 0 {
		t.Fatal("failed inventory must not notify")
	}
	m, _ = toastRefresh(m, []core.AppChoice{toastApp("api", "v2", 200, "new")}, nil)
	if len(m.deploymentToasts) != 1 {
		t.Fatal("recovery must detect an intervening rollout")
	}
}

func TestDeploymentToastExpiryAndStopRespectMonitorGeneration(t *testing.T) {
	m := toastMonitor(t, []core.AppChoice{toastApp("api", "v1", 100, "pod")})
	m, _ = toastRefresh(m, []core.AppChoice{toastApp("api", "v2", 200, "pod-2")}, nil)
	id, generation := m.deploymentToasts[0].id, m.monitorGeneration
	updated, _ := m.Update(deploymentToastExpiredMsg{id: id + 1, generation: generation})
	m = updated.(pickerModel)
	if len(m.deploymentToasts) != 1 {
		t.Fatal("unrelated expiry must be ignored")
	}
	updated, _ = m.Update(monitorKey('m'))
	m = updated.(pickerModel)
	if len(m.deploymentToasts) != 0 || m.deploymentInventory != nil {
		t.Fatal("stopping monitor must clear notifications and baseline")
	}
	updated, _ = m.Update(monitorKey('m'))
	m = updated.(pickerModel)
	m, _ = toastRefresh(m, []core.AppChoice{toastApp("api", "v3", 300, "pod-3")}, nil)
	if len(m.deploymentToasts) != 1 {
		t.Fatal("new monitor session should be able to report another rollout")
	}
	updated, _ = m.Update(deploymentToastExpiredMsg{id: id, generation: generation})
	m = updated.(pickerModel)
	if len(m.deploymentToasts) != 1 {
		t.Fatal("stale expiry from old monitor session must not erase a new toast")
	}
	updated, _ = m.Update(deploymentToastExpiredMsg{id: m.deploymentToasts[0].id, generation: m.monitorGeneration})
	if len(updated.(pickerModel).deploymentToasts) != 0 {
		t.Fatal("matching expiry must dismiss toast")
	}
}

func TestDeploymentToastOverlayKeepsViewportDimensions(t *testing.T) {
	m := toastMonitor(t, []core.AppChoice{toastApp("api", "v1", 100, "pod")})
	m, _ = toastRefresh(m, []core.AppChoice{toastApp("api", "v2", 200, "new")}, nil)
	for _, size := range []tea.WindowSizeMsg{{Width: 32, Height: 12}, {Width: 40, Height: 16}, {Width: 80, Height: 24}, {Width: 140, Height: 40}} {
		updated, _ := m.Update(size)
		m = updated.(pickerModel)
		lines := strings.Split(ansi.Strip(m.View()), "\n")
		if len(lines) != size.Height {
			t.Fatalf("toast changed viewport height: got %d, want %d", len(lines), size.Height)
		}
		for _, line := range lines {
			if lipgloss.Width(line) != size.Width {
				t.Fatalf("toast changed viewport width: %d != %d", lipgloss.Width(line), size.Width)
			}
		}
	}
}

func TestDeploymentToastBoundedToThreeAndClearedOnScopeChange(t *testing.T) {
	m := toastMonitor(t, []core.AppChoice{toastApp("a", "v1", 100, "pod")})
	var next []core.AppChoice
	for i := 0; i < 5; i++ {
		next = append(next, toastApp(fmt.Sprintf("app-%d", i), "v2", 200, "pod"))
	}
	m, _ = toastRefresh(m, next, nil)
	if len(m.deploymentToasts) != maxDeploymentToasts {
		t.Fatal("too many stacked notifications")
	}
	m.clearDeploymentNotifications()
	if len(m.deploymentToasts) != 0 || m.deploymentInventory != nil {
		t.Fatal("switching scope must clear old notifications")
	}
}
