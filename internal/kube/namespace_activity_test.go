package kube

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestNamespaceEventTimestampsAndSeriesAcrossAPIs(t *testing.T) {
	for _, item := range []map[string]any{
		{"metadata": map[string]any{"uid": "e", "creationTimestamp": "2026-10-15T12:00:00Z"}, "involvedObject": map[string]any{"kind": "Pod", "name": "api"}, "type": "Warning", "reason": "BackOff", "message": "retry", "count": float64(3), "eventTime": "2026-10-15T12:01:00Z", "lastTimestamp": "2026-10-15T12:02:00Z", "series": map[string]any{"count": float64(8), "lastObservedTime": "2026-10-15T12:03:00Z"}},
		{"metadata": map[string]any{"uid": "e", "creationTimestamp": "2026-10-15T12:00:00Z"}, "regarding": map[string]any{"kind": "Pod", "name": "api"}, "type": "Warning", "reason": "BackOff", "note": "retry", "deprecatedCount": float64(3), "eventTime": "2026-10-15T12:01:00Z", "deprecatedLastTimestamp": "2026-10-15T12:02:00Z", "series": map[string]any{"count": float64(8), "lastObservedTime": "2026-10-15T12:03:00Z"}},
	} {
		event := namespaceEvent(item)
		if event.ID != "e" || event.Message != "retry" || event.Object != "Pod/api" || event.Count != 8 || event.LastAt.Format(time.RFC3339) != "2026-10-15T12:03:00Z" {
			t.Fatalf("wrong event: %+v", event)
		}
	}
	event := namespaceEvent(map[string]any{"metadata": map[string]any{"name": "e", "creationTimestamp": "2026-10-15T12:00:00Z"}})
	if event.LastAt.IsZero() || event.FirstAt.IsZero() || event.Count != 1 {
		t.Fatalf("fallback missing: %+v", event)
	}
}

func TestNamespaceObjectsCaptureRolloutRestartAndCronJobEvidence(t *testing.T) {
	now := "2026-10-15T12:03:00Z"
	rs := namespaceObject(map[string]any{"metadata": map[string]any{"uid": "r", "name": "api-rs", "creationTimestamp": now, "annotations": map[string]any{"deployment.kubernetes.io/revision": "7"}, "ownerReferences": []any{map[string]any{"kind": "Deployment", "name": "api", "controller": true}}}, "spec": map[string]any{"replicas": float64(0), "template": map[string]any{"spec": map[string]any{"containers": []any{map[string]any{"name": "api", "image": "api:v2"}}}}}}, "replicasets", "ReplicaSet")
	if rs.Replicas != 0 || rs.Images != "api=api:v2" || len(rs.Seeds) != 2 || rs.Seeds[1].Object != "Deployment/api" || !strings.Contains(rs.Seeds[1].Detail, "revision 7") {
		t.Fatalf("rollout: %+v", rs)
	}
	pod := namespaceObject(map[string]any{"metadata": map[string]any{"name": "api", "uid": "p"}, "status": map[string]any{"containerStatuses": []any{map[string]any{"name": "api", "restartCount": float64(4), "state": map[string]any{"running": map[string]any{"startedAt": now}}, "lastState": map[string]any{"terminated": map[string]any{"reason": "OOMKilled"}}}}}}, "pods", "Pod")
	if len(pod.Seeds) != 2 || pod.Seeds[1].At.IsZero() || !strings.Contains(pod.Seeds[1].Detail, "OOMKilled") {
		t.Fatalf("restart: %+v", pod)
	}
	cron := namespaceObject(map[string]any{"metadata": map[string]any{"name": "audit"}, "spec": map[string]any{"schedule": "*/5 * * * *", "suspend": true, "jobTemplate": map[string]any{"spec": map[string]any{"template": map[string]any{"spec": map[string]any{"containers": []any{map[string]any{"name": "job", "image": "audit:v1"}}}}}}}}, "cronjobs", "CronJob")
	if cron.Images != "job=audit:v1" || !strings.Contains(cron.Configuration, "suspended: true") {
		t.Fatalf("cronjob: %+v", cron)
	}
}

func TestNamespaceActivityScopedCollectionWithIndependentFailures(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX kubectl fixture")
	}
	dir := t.TempDir()
	args := filepath.Join(dir, "args")
	t.Setenv("TAILG_ACTIVITY_FIXTURE", dir)
	t.Setenv("TAILG_ACTIVITY_ARGS", args)
	binary := filepath.Join(dir, "kubectl")
	script := `#!/bin/sh
printf '%s\n' "$*" >> "$TAILG_ACTIVITY_ARGS"
while [ "$1" != "get" ] && [ "$#" -gt 0 ]; do shift; done
shift
if [ "$1" = "daemonsets" ]; then echo 'Forbidden: cannot list daemonsets' >&2; exit 1; fi
cat "$TAILG_ACTIVITY_FIXTURE/$1.json"
`
	if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	for _, resource := range activityResources {
		items := []any{}
		if resource.resource == "deployments" {
			items = append(items, map[string]any{"metadata": map[string]any{"name": "api", "namespace": "apollo"}}, map[string]any{"metadata": map[string]any{"name": "other", "namespace": "elsewhere"}})
		}
		data, _ := json.Marshal(map[string]any{"items": items})
		if err := os.WriteFile(filepath.Join(dir, resource.resource+".json"), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	runner := Runner{Binary: binary, Context: "tkgs-qa", Namespace: "apollo"}
	snapshot := runner.NamespaceActivity(context.Background())
	if len(snapshot.Objects) != 1 || !snapshot.Complete["deployments"] || snapshot.Complete["daemonsets"] || !snapshot.Complete["events"] || len(snapshot.Errors) != 1 {
		t.Fatalf("partial collection: %+v", snapshot)
	}
	data, _ := os.ReadFile(args)
	calls := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(calls) != len(activityResources) {
		t.Fatalf("calls: %s", data)
	}
	for _, call := range calls {
		if !strings.HasPrefix(call, "--context tkgs-qa -n apollo get ") || strings.Contains(call, "--all-namespaces") {
			t.Fatalf("scope leaked: %s", call)
		}
	}
	// A malformed list is incomplete, not proof that the resource disappeared.
	os.WriteFile(filepath.Join(dir, "pods.json"), []byte(`{"kind":"List"}`), 0600)
	snapshot = runner.NamespaceActivity(context.Background())
	if snapshot.Complete["pods"] || len(snapshot.Errors) != 2 {
		t.Fatal("invalid list accepted as empty")
	}
	runner.Namespace = ""
	snapshot = runner.NamespaceActivity(context.Background())
	if len(snapshot.Errors) != 1 || len(snapshot.Complete) != 0 {
		t.Fatal("implicit scope allowed")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	runner.Namespace = "apollo"
	snapshot = runner.NamespaceActivity(ctx)
	if len(snapshot.Errors) != len(activityResources) {
		t.Fatal("cancelled collection marked complete")
	}
}
