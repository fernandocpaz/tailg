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
	for _, expected := range []string{"all pods healthy", "RECENT ERRORS", "lookback=20m", "database timeout"} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("status output missing %q: %s", expected, output.String())
		}
	}
}

func TestFormatRecentErrorReportUsesLookbackAndCompactSummary(t *testing.T) {
	groups := map[string]*recentErrorGroup{
		"timeout": {issue: core.Issue{Kind: "TIMEOUT", Service: "api", Summary: "compact timeout detail", FullSummary: strings.Repeat("full detail ", 100)}, count: 3},
		"http":    {issue: core.Issue{Kind: "HTTP 5XX", Service: "worker", Summary: "503"}, count: 1},
	}
	report := FormatRecentErrorReport(groups, 20*time.Minute, 4, 1)
	for _, expected := range []string{"lookback=20m", "groups=2", "events=4", "streams=4", "failed=1", "3× TIMEOUT", "compact timeout detail"} {
		if !strings.Contains(report, expected) {
			t.Fatalf("report missing %q: %s", expected, report)
		}
	}
	if statusLookbackArgument(2*time.Hour) != "120m" {
		t.Fatalf("default kubectl lookback = %q", statusLookbackArgument(2*time.Hour))
	}
}

func TestFormatRecentErrorReportNoErrors(t *testing.T) {
	report := FormatRecentErrorReport(nil, 2*time.Hour, 3, 0)
	if report != "NO RECENT ERRORS | lookback=120m | streams=3 | failed=0\n" {
		t.Fatalf("report = %q", report)
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

func TestNamespaceStatusReportsOnlyUnhealthyPods(t *testing.T) {
	payload := map[string]any{"items": []any{
		map[string]any{"metadata": map[string]any{"name": "healthy"}, "spec": map[string]any{"containers": []any{map[string]any{"name": "api"}}}, "status": map[string]any{"phase": "Running", "containerStatuses": []any{map[string]any{"name": "api", "ready": true, "restartCount": float64(0), "state": map[string]any{"running": map[string]any{}}}}}},
		map[string]any{"metadata": map[string]any{"name": "bad-worker"}, "spec": map[string]any{"containers": []any{map[string]any{"name": "worker"}}}, "status": map[string]any{"phase": "Running", "containerStatuses": []any{map[string]any{"name": "worker", "ready": false, "restartCount": float64(4), "state": map[string]any{"waiting": map[string]any{"reason": "CrashLoopBackOff", "message": "back-off 5m"}}}}}},
	}}
	report, count := NamespaceStatusReport(payload, "default")
	if count != 1 || !strings.Contains(report, "unhealthy=1/2") || !strings.Contains(report, "CrashLoopBackOff") || strings.Contains(report, "healthy |") {
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
