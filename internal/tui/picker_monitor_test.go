package tui

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/fernandocpaz/tailg/internal/core"
	"github.com/muesli/termenv"
)

func monitorKey(key rune) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{key}}
}

func enableAppMonitor(t *testing.T, m pickerModel) (pickerModel, tea.Cmd) {
	t.Helper()
	updated, cmd := m.Update(monitorKey('m'))
	m = updated.(pickerModel)
	if !m.monitorEnabled || !m.monitorBusy || cmd == nil {
		t.Fatal("M must enable monitoring and start a scan")
	}
	t.Cleanup(m.monitorCancel)
	return m, cmd
}

func TestAppMonitorMenuAndDefault(t *testing.T) {
	m := newPickerModel([]core.AppChoice{{Name: "api", Pods: []string{"api-1"}}}, 1, "qa", "apollo", nil)
	if m.Init() != nil || m.monitorEnabled {
		t.Fatal("monitor must be opt-in")
	}
	view := strings.Join(strings.Fields(core.StripANSI(m.View())), " ")
	if !strings.Contains(view, "M Monitor") || !strings.Contains(view, "Monitor: OFF") {
		t.Fatalf("main menu is missing monitoring: %s", view)
	}
	m, _ = enableAppMonitor(t, m)
	if !strings.Contains(m.View(), "Monitor: ON · every 1m · scanning") {
		t.Fatalf("scan status is missing: %s", m.View())
	}
}

func TestClassifyAppHealthAcrossReplicas(t *testing.T) {
	app := core.AppChoice{Name: "api", Pods: []string{"api-1", "api-2", "api-2"}}
	tests := []struct {
		name     string
		one, two podHealthSample
		want     podHealthKind
	}{
		{"quiet", podHealthSample{}, podHealthSample{}, podHealthIdle},
		{"active clean", podHealthSample{lines: 4}, podHealthSample{}, podHealthHealthy},
		{"warning in another replica", podHealthSample{lines: 4}, podHealthSample{warnings: 1}, podHealthWarning},
		{"error overrides warning", podHealthSample{warnings: 8}, podHealthSample{errors: 1}, podHealthError},
		{"errors sum without duplicate pods", podHealthSample{errors: 2}, podHealthSample{errors: 3}, podHealthCritical},
		{"partial clean is gray", podHealthSample{lines: 4}, podHealthSample{err: errors.New("denied")}, podHealthIdle},
		{"partial error stays red", podHealthSample{errors: 1}, podHealthSample{err: errors.New("denied")}, podHealthError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyAppHealth(app, map[string]podHealthSample{"api-1": tt.one, "api-2": tt.two}, nil)
			if got.kind != tt.want {
				t.Fatalf("kind = %v, want %v", got.kind, tt.want)
			}
		})
	}
	// Inventory may expose detailed choices rather than the legacy name list.
	app = core.AppChoice{Name: "api", PodChoices: []core.PodChoice{{Name: "api-1"}, {Name: "api-2"}}}
	if got := classifyAppHealth(app, map[string]podHealthSample{"api-2": {errors: 1}}, nil); got.kind != podHealthError {
		t.Fatalf("detailed pod choices must be monitored: %+v", got)
	}
	if got := classifyAppHealth(app, map[string]podHealthSample{"api-1": {lines: 10}}, nil); got.kind != podHealthIdle {
		t.Fatal("missing replica must not appear healthy")
	}
}

func TestAppMonitorWarningTrendAndScanFailureStatus(t *testing.T) {
	m, _ := enableAppMonitor(t, pickerModel{apps: []core.AppChoice{{Name: "api", Pods: []string{"one", "two"}}}})
	scan := func(one, two podHealthSample) {
		updated, _ := m.Update(podHealthScanMsg{generation: m.monitorGeneration,
			samples: map[string]podHealthSample{"one": one, "two": two}, scannedAt: time.Now()})
		m = updated.(pickerModel)
	}
	scan(podHealthSample{warnings: 3}, podHealthSample{warnings: 1})
	scan(podHealthSample{warnings: 1}, podHealthSample{warnings: 3})
	if m.monitorApps["api"].warningsIncreasing {
		t.Fatal("warnings moved between replicas, but the application total did not increase")
	}
	scan(podHealthSample{warnings: 2}, podHealthSample{warnings: 3})
	if !m.monitorApps["api"].warningsIncreasing {
		t.Fatal("increasing application warning total was missed")
	}
	scan(podHealthSample{errors: 1}, podHealthSample{err: errors.New("denied")})
	if m.monitorBusy || m.monitorLastScan.IsZero() || !strings.Contains(m.View(), "1 pod scans failed") || m.monitorApps["api"].kind != podHealthError {
		t.Fatalf("scan result not reflected in main picker: %+v", m)
	}
}

func TestAppMonitorStopsAndRejectsOldMessages(t *testing.T) {
	m, _ := enableAppMonitor(t, pickerModel{apps: []core.AppChoice{{Name: "api", Pods: []string{"one"}}}})
	oldGeneration, oldContext := m.monitorGeneration, m.monitorContext
	updated, cmd := m.Update(monitorKey('M'))
	m = updated.(pickerModel)
	if m.monitorEnabled || m.monitorBusy || cmd != nil || oldContext.Err() == nil {
		t.Fatal("M must cancel active scans")
	}
	m, _ = enableAppMonitor(t, m)
	for _, msg := range []tea.Msg{
		podHealthScanMsg{generation: oldGeneration, samples: map[string]podHealthSample{"one": {errors: 10}}},
		podMonitorPollMsg{generation: oldGeneration}, podMonitorFlashMsg{generation: oldGeneration},
	} {
		updated, cmd = m.Update(msg)
		m = updated.(pickerModel)
		if cmd != nil || !m.monitorBusy || len(m.monitorApps) != 0 || !m.monitorFlashOn {
			t.Fatalf("old monitor message changed the new session: %T", msg)
		}
	}
}

func TestAppMonitorCancelsOnEveryPickerExit(t *testing.T) {
	for _, key := range []tea.KeyMsg{monitorKey('q'), monitorKey('c'), monitorKey('n'), monitorKey('r'), monitorKey('p'), {Type: tea.KeyEnter}, {Type: tea.KeyEsc}, {Type: tea.KeyCtrlC}} {
		t.Run(key.String(), func(t *testing.T) {
			m, _ := enableAppMonitor(t, pickerModel{apps: []core.AppChoice{{Name: "api", Pods: []string{"one"}}}})
			ctx := m.monitorContext
			updated, cmd := m.Update(key)
			if updated.(pickerModel).monitorEnabled || cmd == nil || ctx.Err() == nil {
				t.Fatal("leaving picker must cancel monitoring")
			}
		})
	}
}

func TestMonitorPollingSkipsOverlappingScanAndFlashes(t *testing.T) {
	m, _ := enableAppMonitor(t, pickerModel{})
	updated, cmd := m.Update(podMonitorPollMsg{generation: m.monitorGeneration})
	m = updated.(pickerModel)
	if cmd == nil || !m.monitorBusy {
		t.Fatal("busy monitor must retain its in-flight scan and schedule the next tick")
	}
	updated, _ = m.Update(podHealthScanMsg{generation: m.monitorGeneration, samples: map[string]podHealthSample{}})
	m = updated.(pickerModel)
	updated, cmd = m.Update(podMonitorPollMsg{generation: m.monitorGeneration})
	m = updated.(pickerModel)
	if !m.monitorBusy || len(cmd().(tea.BatchMsg)) != 2 {
		t.Fatal("idle monitor must schedule the next tick and scan")
	}
	updated, cmd = m.Update(podMonitorFlashMsg{generation: m.monitorGeneration})
	if updated.(pickerModel).monitorFlashOn || cmd == nil {
		t.Fatal("flash tick must change the visible flash phase and schedule another")
	}
}

func TestAppMonitorHealthColorSurvivesSelection(t *testing.T) {
	oldProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(oldProfile) })
	m, _ := enableAppMonitor(t, pickerModel{apps: []core.AppChoice{{Name: "api", Pods: []string{"one"}}}, checked: map[string]bool{"api": true}})
	updated, _ := m.Update(podHealthScanMsg{generation: m.monitorGeneration, samples: map[string]podHealthSample{"one": {errors: 5}}})
	m = updated.(pickerModel)
	name := "api" + strings.Repeat(" ", pickerAppWidth-3)
	if !strings.Contains(m.View(), alertStyle.Render(name)) {
		t.Fatal("selected application's name must retain its red health color")
	}
	plain := core.StripANSI(m.View())
	if !strings.Contains(plain, "> [x] api") {
		t.Fatal("monitoring must keep cursor and multi-selection visible")
	}
	updated, _ = m.Update(podMonitorFlashMsg{generation: m.monitorGeneration})
	if !strings.Contains(updated.(pickerModel).View(), dimStyle.Render(name)) {
		t.Fatal("critical application must change color during flash phase")
	}
}

func TestMainMonitorScansOnlyDisplayedAppsAllReplicasAndContainers(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake kubectl is a POSIX shell script")
	}
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args")
	t.Setenv("MONITOR_TEST_ARGS", argsFile)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$MONITOR_TEST_ARGS\"\nprintf '[12:00:00 INF] active\\n[12:00:01 ERR] failure\\n'\n"
	if err := os.WriteFile(filepath.Join(dir, "kubectl"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	apps := make([]core.AppChoice, 0, maxPickerApps+1)
	for i := 0; i <= maxPickerApps; i++ {
		apps = append(apps, core.AppChoice{Name: string(rune('a' + i)), Pods: []string{"shared"}, DeployedAt: time.Unix(int64(i), 0)})
	}
	apps[0].Pods = []string{"hidden"}
	apps[1].Pods = []string{"replica-one", "replica-two"}
	prepared, total := preparePickerApps(apps)
	m, cmd := enableAppMonitor(t, newPickerModel(prepared, total, "tkgs-qa", "apollo", nil))
	batch := cmd().(tea.BatchMsg)
	msg := batch[0]().(podHealthScanMsg)
	if len(msg.samples) != 3 || msg.samples["replica-two"].errors != 1 {
		t.Fatalf("unexpected scanned pods or severity: %+v", msg.samples)
	}
	updated, _ := m.Update(msg)
	if updated.(pickerModel).monitorBusy {
		t.Fatal("completed scan must clear scanning state")
	}
	args, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(args)), "\n") {
		if !strings.Contains(line, "--context tkgs-qa -n apollo logs pod/") || !strings.Contains(line, "--all-containers=true --since=1m --tail=-1") || strings.Contains(line, "hidden") {
			t.Fatalf("incorrect scope or log polling options: %s", line)
		}
	}
}

func TestExactPodMonitorUsesSharedLifecycle(t *testing.T) {
	m := podPickerModel{rows: []podPickerRow{{pod: core.PodChoice{Name: "one"}}}}
	updated, cmd := m.Update(monitorKey('M'))
	m = updated.(podPickerModel)
	if cmd == nil || !m.monitorEnabled {
		t.Fatal("exact-pod monitor toggle regressed")
	}
	t.Cleanup(m.monitorCancel)
	ctx := m.monitorContext
	updated, _ = m.Update(podHealthScanMsg{generation: m.monitorGeneration, samples: map[string]podHealthSample{"one": {errors: 1}}})
	m = updated.(podPickerModel)
	if m.monitorHealth["one"].kind != podHealthError || m.monitorBusy {
		t.Fatal("exact-pod health handling regressed")
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if updated.(podPickerModel).monitorEnabled || ctx.Err() == nil {
		t.Fatal("exact-pod exit must stop monitoring")
	}
}
