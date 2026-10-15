package tui

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/fernandocpaz/tailg/internal/core"
	"github.com/fernandocpaz/tailg/internal/kube"
)

func selectionFixture(kind string) pickerModel {
	m := newPickerModel([]core.AppChoice{{Name: "api", Pods: []string{"api-1", "api-2"}}, {Name: "worker", Pods: []string{"worker-1"}}}, 2, "tkgs-qa", "apollo", nil)
	m.width, m.height, m.index = 100, 30, 1
	m.openPickerSelection(kind)
	if kind != "pods" {
		var namespaces []string
		var contexts []kube.ContextInfo
		for i := 0; i < 30; i++ {
			namespaces = append(namespaces, fmt.Sprintf("namespace-%02d", i))
			contexts = append(contexts, kube.ContextInfo{Name: fmt.Sprintf("context-%02d", i), Namespace: "apollo"})
		}
		handled, _ := m.updatePickerSelectionMessage(pickerChoicesResult{scope: activityScope(m.kubeContext, m.namespace), request: m.selectionRequest, namespaces: namespaces, contexts: contexts})
		if !handled {
			panic("fixture did not load")
		}
	}
	return m
}

func TestSelectionModalsCancelWithoutLosingMainStateOrMonitor(t *testing.T) {
	for _, kind := range []string{"context", "namespace", "pods"} {
		t.Run(kind, func(t *testing.T) {
			m := selectionFixture(kind)
			m.checked = map[string]bool{"api": true}
			m.monitorEnabled, m.monitorGeneration = true, 7
			m = pickerTestKey(m, "j")
			if m.index != 1 || !m.checked["api"] {
				t.Fatal("modal navigation reached main list")
			}
			m = pickerTestKey(m, "esc")
			if m.selection != nil || m.index != 1 || !m.checked["api"] || !m.monitorEnabled || m.monitorGeneration != 7 {
				t.Fatal("cancel discarded main state")
			}
		})
	}
}

func TestNamespaceModalManualEntryAfterForbiddenListing(t *testing.T) {
	m := selectionFixture("namespace")
	_, _ = m.updatePickerSelectionMessage(pickerChoicesResult{scope: activityScope(m.kubeContext, m.namespace), request: m.selectionRequest, err: errors.New("Forbidden")})
	if !strings.Contains(ansi.Strip(m.View()), "Namespace list unavailable") {
		t.Fatal("RBAC failure hidden")
	}
	m = pickerTestKey(m, "/")
	m = pickerTestKey(m, "cronjob")
	updated, cmd := m.Update(pickerTestKeyMsg("enter"))
	m = updated.(pickerModel)
	if cmd == nil || !m.selection.loading || m.namespace != "apollo" {
		t.Fatal("namespace changed before the load finished")
	}
	request := m.selectionRequest
	m = pickerTestKey(m, "esc")
	updated, cmd = m.Update(pickerScopeResult{fromScope: activityScope("tkgs-qa", "apollo"), request: request, kubeContext: "tkgs-qa", namespace: "cronjob"})
	m = updated.(pickerModel)
	if cmd != nil || m.namespace != "apollo" || m.selection != nil {
		t.Fatal("canceled scope load changed the namespace")
	}
}

func TestScopeChangeClearsOldDataAndRejectsLateMessages(t *testing.T) {
	m := selectionFixture("namespace")
	m.checked = map[string]bool{"api": true}
	m.monitorEnabled, m.monitorGeneration = true, 4
	m.inventoryBusy, m.inventoryRequest = true, 6
	m.activity.request = 8
	scope := activityScope(m.kubeContext, m.namespace)
	m.selectPickerScope("tkgs-dev", "ui")
	request := m.selectionRequest
	updated, cmd := m.Update(pickerScopeResult{fromScope: scope, request: request, kubeContext: "tkgs-dev", namespace: "ui", apps: []core.AppChoice{{Name: "new-ui", Pods: []string{"new-ui-1"}}}})
	m = updated.(pickerModel)
	if cmd == nil || m.selection != nil || m.kubeContext != "tkgs-dev" || m.namespace != "ui" || len(m.checked) > 0 || m.monitorEnabled || m.apps[0].Name != "new-ui" {
		t.Fatalf("scope not committed atomically: %+v", m)
	}
	for _, msg := range []tea.Msg{
		pickerRefreshResult{scope: scope, request: 6, apps: []core.AppChoice{{Name: "old-api"}}},
		pickerRefreshTick{scope: scope, request: 6},
		namespaceActivityResult{scope: scope, request: 8},
		podHealthScanMsg{generation: 4, samples: map[string]podHealthSample{"api-1": {errors: 5}}},
	} {
		updated, cmd = m.Update(msg)
		m = updated.(pickerModel)
		if cmd != nil || m.apps[0].Name != "new-ui" || m.monitorEnabled {
			t.Fatalf("stale %T changed the new scope", msg)
		}
	}
	if m.activity.request <= 8 {
		t.Fatal("activity request generation reused")
	}
}

func TestContextResolveFailureStaysInModalAndCanCancel(t *testing.T) {
	m := selectionFixture("context")
	m.selectPickerScope("missing", "")
	updated, _ := m.Update(pickerScopeResult{fromScope: activityScope(m.kubeContext, m.namespace), request: m.selectionRequest, resolveErr: errors.New("kubeconfig unavailable")})
	m = updated.(pickerModel)
	if m.selection == nil || m.selection.loading || m.kubeContext != "tkgs-qa" || !strings.Contains(ansi.Strip(m.View()), "kubeconfig unavailable") {
		t.Fatal("failed context changed scope or closed the picker")
	}
	m = pickerTestKey(m, "esc")
	if m.selection != nil {
		t.Fatal("context error could not be dismissed")
	}
}

func TestSelectorsFitResizesAndKeepCloseHintVisible(t *testing.T) {
	for _, kind := range []string{"context", "namespace", "pods"} {
		for _, size := range []tea.WindowSizeMsg{{Width: 24, Height: 8}, {Width: 40, Height: 12}, {Width: 80, Height: 24}, {Width: 160, Height: 40}} {
			m := selectionFixture(kind)
			if kind == "namespace" {
				m.selection.namespaces.loadError = strings.Repeat("Forbidden; ", 20)
				m = pickerTestKey(m, "/")
				m = pickerTestKey(m, strings.Repeat("namespace-", 30))
			}
			updated, _ := m.Update(size)
			m = updated.(pickerModel)
			view := ansi.Strip(m.View())
			if !strings.Contains(view, "Esc") {
				t.Fatalf("%s %+v hid Esc:\n%s", kind, size, view)
			}
			lines := strings.Split(view, "\n")
			if len(lines) != size.Height {
				t.Fatal("modal did not fill viewport")
			}
			for _, line := range lines {
				if lipgloss.Width(line) != size.Width {
					t.Fatalf("%s %+v overflows", kind, size)
				}
			}
		}
	}
}

func TestMoreAndExactPodsWorkInsideParent(t *testing.T) {
	m := selectionFixture("context")
	for i := 0; i < frequentChoices; i++ {
		m = pickerTestKey(m, "j")
	}
	m = pickerTestKey(m, "enter")
	if !m.selection.contexts.expanded || m.selection.contexts.selected != "" {
		t.Fatal("More selected a context")
	}
	m = pickerTestKey(m, "esc")
	if m.selection == nil || m.selection.contexts.expanded {
		t.Fatal("Esc should first collapse More")
	}
	m = pickerTestKey(m, "esc")
	if m.selection != nil {
		t.Fatal("second Esc did not close")
	}
	m = selectionFixture("pods")
	m = pickerTestKey(m, " ")
	m = pickerTestKey(m, "j")
	m = pickerTestKey(m, " ")
	updated, cmd := m.Update(pickerTestKeyMsg("enter"))
	m = updated.(pickerModel)
	if cmd == nil || m.result.Action != PickerOpenApp || strings.Join(m.result.Pods, ",") != "api-1,api-2" {
		t.Fatalf("exact pod result lost: %+v", m.result)
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("opening logs should leave the picker")
	}
}

func TestCanceledChoiceLoadCannotReplaceReopenedModal(t *testing.T) {
	m := selectionFixture("namespace")
	old := m.selectionRequest
	m = pickerTestKey(m, "esc")
	m.openPickerSelection("namespace")
	updated, cmd := m.Update(pickerChoicesResult{scope: activityScope(m.kubeContext, m.namespace), request: old, namespaces: []string{"wrong"}})
	m = updated.(pickerModel)
	if cmd != nil || !m.selection.loading || m.selection.namespaces != nil {
		t.Fatal("old choice load replaced new modal")
	}
}

func TestRefreshInPlaceAndPodInventoryIncludesAllApps(t *testing.T) {
	m := selectionFixture("pods")
	m.closePickerSelection()
	m.checked = map[string]bool{"worker": true}
	updated, cmd := m.Update(pickerTestKeyMsg("r"))
	m = updated.(pickerModel)
	if cmd == nil || !m.inventoryBusy || m.index != 1 || !m.checked["worker"] {
		t.Fatal("manual refresh discarded state")
	}
	updated, _ = m.Update(pickerRefreshResult{scope: activityScope(m.kubeContext, m.namespace), request: m.inventoryRequest, apps: []core.AppChoice{{Name: "worker", Pods: []string{"new-worker"}}}})
	m = updated.(pickerModel)
	if m.inventoryBusy || m.apps[m.index].Pods[0] != "new-worker" || !m.checked["worker"] {
		t.Fatal("refresh result lost selected workload")
	}
	apps := make([]core.AppChoice, 25)
	for i := range apps {
		apps[i] = core.AppChoice{Name: fmt.Sprintf("app-%02d", i), Pods: []string{fmt.Sprintf("pod-%02d", i)}}
	}
	m.refreshApps(pickerRefreshResult{apps: apps})
	m.openPickerSelection("pods")
	if len(m.apps) != 20 || len(m.selection.pods.rows) != 25 {
		t.Fatal("exact-pod modal incorrectly used the 20-application limit")
	}
}
