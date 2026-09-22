package app

import (
	"strings"
	"testing"

	"github.com/fernandocpaz/tailg/internal/core"
	"github.com/fernandocpaz/tailg/internal/tui"
)

func TestPickerSelectionScopeSeparatesFollowingAppsFromPinnedPods(t *testing.T) {
	apps := []core.AppChoice{
		{Name: "api", Selector: "app=api", Pods: []string{"api-1"}},
		{Name: "worker", Selector: "app=worker", Pods: []string{"worker-1"}},
		{Name: "job", Pods: []string{"job-1"}},
	}
	pods, selectors, title := pickerSelectionScope(tui.PickerResult{Apps: apps})
	if strings.Join(pods, ",") != "job-1" || strings.Join(selectors, ",") != "app=api,app=worker" || title != "3 selected apps" {
		t.Fatalf("app scope: pods=%v selectors=%v title=%q", pods, selectors, title)
	}
	pods, selectors, title = pickerSelectionScope(tui.PickerResult{Pods: []string{"api-1", "worker-1", "api-1"}})
	if strings.Join(pods, ",") != "api-1,worker-1" || len(selectors) != 0 || title != "2 selected pods" {
		t.Fatalf("pod scope: pods=%v selectors=%v title=%q", pods, selectors, title)
	}
}

func TestPickerSelectionOpensWindowsTerminalPanesForMultipleChoices(t *testing.T) {
	tests := []struct {
		name      string
		selection tui.PickerResult
		goos      string
		want      bool
	}{
		{name: "multiple apps", selection: tui.PickerResult{Apps: []core.AppChoice{{Name: "api"}, {Name: "worker"}}}, goos: "windows", want: true},
		{name: "multiple exact pods", selection: tui.PickerResult{Pods: []string{"api-1", "worker-1"}}, goos: "windows", want: true},
		{name: "duplicate pod", selection: tui.PickerResult{Pods: []string{"api-1", "api-1"}}, goos: "windows", want: false},
		{name: "single app", selection: tui.PickerResult{Apps: []core.AppChoice{{Name: "api"}}}, goos: "windows", want: false},
		{name: "combined fallback", selection: tui.PickerResult{Pods: []string{"api-1", "worker-1"}}, goos: "linux", want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := pickerSelectionOpensPanes(test.selection, test.goos); got != test.want {
				t.Fatalf("pickerSelectionOpensPanes()=%t, want %t", got, test.want)
			}
		})
	}
}
