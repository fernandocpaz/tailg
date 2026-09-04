package kube

import (
	"strings"
	"testing"
)

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
