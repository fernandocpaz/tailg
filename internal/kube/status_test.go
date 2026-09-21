package kube

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/fernandocpaz/tailg/internal/core"
)

func TestRunStatusScansRecentErrorsUsingRequestedLookback(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake kubectl is a shell script")
	}
	path := filepath.Join(t.TempDir(), "kubectl")
	script := `#!/bin/sh
case "$*" in
  *"get pods -o json"*) printf '%s' '{"items":[{"metadata":{"name":"api-pod"},"spec":{"containers":[{"name":"api"}]},"status":{"phase":"Running","containerStatuses":[{"name":"api","ready":true,"restartCount":0,"state":{"running":{}}}]}}]}' ;;
  *"logs pod/api-pod -c api"*"--since 20m"*) printf '%s\n' '2026-09-11T12:00:00Z [12:00:00 ERR] database timeout' ;;
  *) echo "unexpected kubectl args: $*" >&2; exit 2 ;;
esac
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	code := (Runner{Namespace: "default", Binary: path}).RunStatus(context.Background(), "default", StatusOptions{Lookback: 20 * time.Minute, Output: &output})
	if code != 0 {
		t.Fatalf("status code=%d output=%s", code, output.String())
	}
	for _, expected := range []string{"all pods healthy", "RECENT ERRORS", "lookback=20m", "Select", "[1]", "Last Seen", "Pod / Container", "api-pod/api", "database timeout"} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("status output missing %q: %s", expected, output.String())
		}
	}
}

func TestFormatRecentErrorReportUsesLookbackAndCompactSummary(t *testing.T) {
	checkedAt := time.Date(2026, 9, 11, 15, 30, 0, 0, time.FixedZone("EDT", -4*60*60))
	groups := map[string]*recentErrorGroup{
		"timeout": {issue: core.Issue{Kind: "TIMEOUT", Service: "api", Summary: "compact timeout detail", FullSummary: "complete timeout detail with database host"}, count: 3, lastSeen: checkedAt.Add(-time.Minute), sources: map[string]bool{"api-7c9/api": true}},
		"http":    {issue: core.Issue{Kind: "HTTP 5XX", Service: "worker", Summary: "503"}, count: 1},
	}
	report := formatRecentErrorReportAt(groups, 20*time.Minute, 4, 1, checkedAt)
	for _, expected := range []string{"checked=2026-09-11T15:30:00-04:00", "lookback=20m", "groups=2", "events=4", "streams=4", "failed=1", "| 3", "TIMEOUT", "2026-09-11T15:29:00-04:00", "1m ago", "api-7c9/api", "complete timeout detail with database host"} {
		if !strings.Contains(report, expected) {
			t.Fatalf("report missing %q: %s", expected, report)
		}
	}
	if statusLookbackArgument(2*time.Hour) != "120m" {
		t.Fatalf("default kubectl lookback = %q", statusLookbackArgument(2*time.Hour))
	}
}

func TestFormatRecentErrorReportNoErrors(t *testing.T) {
	report := formatRecentErrorReportAt(nil, 2*time.Hour, 3, 0, time.Date(2026, 9, 11, 19, 0, 0, 0, time.UTC))
	for _, expected := range []string{"checked=2026-09-11T19:00:00Z", "lookback=120m", "events=0", "| Result", "No recent errors found"} {
		if !strings.Contains(report, expected) {
			t.Fatalf("report missing %q: %s", expected, report)
		}
	}
}

func TestNamespaceStatusReportIsTimestampedTableWithWrappedFullDetails(t *testing.T) {
	longMessage := strings.Repeat("scheduling remains blocked ", 5)
	payload := map[string]any{"items": []any{
		map[string]any{
			"metadata": map[string]any{"name": "api-with-a-very-long-generated-pod-name-1234567890"},
			"spec":     map[string]any{"containers": []any{map[string]any{"name": "api"}}},
			"status": map[string]any{
				"phase":      "Pending",
				"conditions": []any{map[string]any{"type": "PodScheduled", "status": "False", "reason": "Unschedulable", "message": longMessage}},
			},
		},
	}}
	report, unhealthy := namespaceStatusReportAt(payload, "apollo", time.Date(2026, 9, 11, 19, 5, 0, 0, time.UTC))
	for _, expected := range []string{"Checked At", "2026-09-11T19:05:00Z", "UNHEALTHY POD(S)", "| Pod", "api-with-a-very-long-generated-", "pod-name-1234567890", "scheduling remains blocked"} {
		if !strings.Contains(report, expected) {
			t.Fatalf("report missing %q: %s", expected, report)
		}
	}
	if unhealthy != 1 || strings.Count(report, "scheduling") != 6 || strings.Contains(report, "…") {
		t.Fatalf("unhealthy=%d; full detail was not preserved: %s", unhealthy, report)
	}
}

func TestStyledStatusDashboardAdaptsToNarrowTerminal(t *testing.T) {
	payload := map[string]any{"items": []any{
		map[string]any{
			"metadata": map[string]any{"name": "patient-api-7c9d8"},
			"spec":     map[string]any{"containers": []any{map[string]any{"name": "api"}}},
			"status": map[string]any{
				"phase":             "Running",
				"containerStatuses": []any{map[string]any{"name": "api", "ready": false, "restartCount": 4, "state": map[string]any{"waiting": map[string]any{"reason": "CrashLoopBackOff"}}}},
			},
		},
	}}
	report, unhealthy := namespaceStatusReportAtStyled(payload, "apollo", time.Date(2026, 9, 11, 19, 5, 0, 0, time.UTC), statusVisuals{Decorated: true, Width: 72})
	for _, expected := range []string{"● TAILG STATUS", "╭", "Status", "Value", "Healthy", "● 1 UNHEALTHY POD(S)", "Health", "Problem", "CrashLoopBackOff"} {
		if !strings.Contains(report, expected) {
			t.Fatalf("dashboard missing %q: %s", expected, report)
		}
	}
	if unhealthy != 1 {
		t.Fatalf("unhealthy=%d", unhealthy)
	}
	if strings.Contains(report, "\x1b[") {
		t.Fatalf("no-color dashboard contains ANSI escapes: %q", report)
	}
	for _, line := range strings.Split(report, "\n") {
		if lipgloss.Width(line) > 72 {
			t.Fatalf("dashboard line width=%d: %q", lipgloss.Width(line), line)
		}
	}
}

func TestRecentErrorReportCountsUnscannedStreamsAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	payload := map[string]any{"items": []any{
		map[string]any{"metadata": map[string]any{"name": "api-pod"}, "spec": map[string]any{"containers": []any{map[string]any{"name": "api"}, map[string]any{"name": "metrics"}}}},
	}}
	report, err := (Runner{}).RecentErrorReport(ctx, payload, 20*time.Minute)
	if err == nil || !strings.Contains(report, "streams=2 | failed=2") {
		t.Fatalf("report=%q err=%v", report, err)
	}
}

func TestNamespaceStatusReportsUnhealthyPodsInTable(t *testing.T) {
	payload := map[string]any{"items": []any{
		map[string]any{"metadata": map[string]any{"name": "healthy"}, "spec": map[string]any{"containers": []any{map[string]any{"name": "api"}}}, "status": map[string]any{"phase": "Running", "containerStatuses": []any{map[string]any{"name": "api", "ready": true, "restartCount": float64(0), "state": map[string]any{"running": map[string]any{}}}}}},
		map[string]any{"metadata": map[string]any{"name": "bad-worker"}, "spec": map[string]any{"containers": []any{map[string]any{"name": "worker"}}}, "status": map[string]any{"phase": "Running", "containerStatuses": []any{map[string]any{"name": "worker", "ready": false, "restartCount": float64(4), "state": map[string]any{"waiting": map[string]any{"reason": "CrashLoopBackOff", "message": "back-off 5m"}}}}}},
	}}
	report, count := NamespaceStatusReport(payload, "default")
	if count != 1 || !strings.Contains(report, "| ALERT  | default") || !strings.Contains(report, "| 2    | 1") || !strings.Contains(report, "Review unhealthy pods") || !strings.Contains(report, "CrashLoopBackOff") || !strings.Contains(report, "| bad-worker") || strings.Contains(report, "| healthy") {
		t.Fatalf("count=%d report=%s", count, report)
	}
}

func TestSucceededPodIsHealthy(t *testing.T) {
	pod := map[string]any{"metadata": map[string]any{"name": "job"}, "status": map[string]any{"phase": "Succeeded"}}
	if issues := PodHealthIssues(pod); len(issues) != 0 {
		t.Fatalf("issues=%#v", issues)
	}
}

func TestPodStatusSummaryKeepsHistoricalCrashEvidenceWithoutMarkingPodUnhealthy(t *testing.T) {
	pod := map[string]any{
		"metadata": map[string]any{"name": "api-7d9"},
		"spec":     map[string]any{"containers": []any{map[string]any{"name": "api"}}},
		"status": map[string]any{
			"phase": "Running",
			"containerStatuses": []any{map[string]any{
				"name": "api", "ready": true, "restartCount": float64(2),
				"state": map[string]any{"running": map[string]any{"startedAt": "2026-09-04T15:00:00Z"}},
				"lastState": map[string]any{"terminated": map[string]any{
					"reason": "OOMKilled", "exitCode": float64(137), "finishedAt": "2026-09-04T14:59:58Z",
				}},
			}},
		},
	}
	payload := map[string]any{"items": []any{pod}}
	summaries := PodStatusSummaries(payload)
	if len(summaries) != 1 || len(summaries[0].Containers) != 1 {
		t.Fatalf("summaries=%#v", summaries)
	}
	container := summaries[0].Containers[0]
	if container.State != "running" || container.Restarts != 2 || container.LastReason != "OOMKilled" || container.LastExitCode != 137 || container.LastFinishedAt != "2026-09-04T14:59:58Z" {
		t.Fatalf("container=%+v", container)
	}
	if issues := PodHealthIssues(pod); len(issues) != 0 {
		t.Fatalf("historical restart should not make a healthy pod unhealthy: %#v", issues)
	}
}


func TestRecentErrorSelectionsUseLatestSourceAndSearchTerm(t *testing.T) {
	now := time.Date(2026, 9, 21, 16, 0, 0, 0, time.UTC)
	groups := map[string]*recentErrorGroup{
		"timeout": {
			issue: core.Issue{Kind: "TIMEOUT", Service: "api", FullSummary: "database timeout", SearchTerm: "timeout"},
			count: 2, lastSeen: now,
			latestPod: "api-new", latestContainer: "api",
			sources: map[string]bool{"api-old/api": true, "api-new/api": true},
		},
	}
	selections := recentErrorSelections(groups)
	if len(selections) != 1 {
		t.Fatalf("selections=%#v", selections)
	}
	got := selections[0]
	if got.Pod != "api-new" || got.Container != "api" || got.SearchTerm != "timeout" || got.Summary != "database timeout" || got.Count != 2 {
		t.Fatalf("selection=%+v", got)
	}
}

func TestRunStatusOffersRecentErrorsForInteractiveOpening(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake kubectl is a shell script")
	}
	path := filepath.Join(t.TempDir(), "kubectl")
	script := `#!/bin/sh
case "$*" in
  *"get pods -o json"*) printf '%s' '{"items":[{"metadata":{"name":"api-pod"},"spec":{"containers":[{"name":"api"}]},"status":{"phase":"Running","containerStatuses":[{"name":"api","ready":true,"restartCount":0,"state":{"running":{}}}]}}]}' ;;
  *"logs pod/api-pod -c api"*"--since 20m"*) printf '%s\\n' '2026-09-21T15:58:00Z [15:58:00 ERR] upstream timeout' ;;
  *) echo "unexpected kubectl args: $*" >&2; exit 2 ;;
esac
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	var offered []StatusErrorSelection
	code := (Runner{Namespace: "default", Binary: path}).RunStatus(context.Background(), "default", StatusOptions{
		Lookback: 20 * time.Minute,
		Output: &output,
		OpenRecentErrors: func(values []StatusErrorSelection) error {
			offered = append([]StatusErrorSelection(nil), values...)
			return nil
		},
	})
	if code != 0 {
		t.Fatalf("status code=%d output=%s", code, output.String())
	}
	if len(offered) != 1 {
		t.Fatalf("offered=%#v output=%s", offered, output.String())
	}
	if offered[0].Pod != "api-pod" || offered[0].Container != "api" || offered[0].SearchTerm == "" {
		t.Fatalf("offered selection=%+v", offered[0])
	}
}
