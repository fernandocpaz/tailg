package kube

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"
)

func TestPodPickerMetadata(t *testing.T) {
	pod := map[string]any{
		"metadata": map[string]any{
			"name":              "api-abc-123",
			"creationTimestamp": "2026-09-20T10:05:00Z",
			"ownerReferences": []any{
				map[string]any{"kind": "ReplicaSet", "name": "api-abc", "controller": true},
			},
		},
		"spec": map[string]any{
			"containers": []any{
				map[string]any{"name": "api", "image": "registry.example/api:20260920.3"},
				map[string]any{"name": "otel", "image": "registry.example/otel@sha256:abc123"},
			},
		},
		"status": map[string]any{
			"startTime": "2026-09-20T10:06:00Z",
			"containerStatuses": []any{
				map[string]any{"name": "api", "state": map[string]any{"running": map[string]any{"startedAt": "2026-09-20T10:08:00Z"}}},
				map[string]any{"name": "otel", "state": map[string]any{"running": map[string]any{"startedAt": "2026-09-20T10:07:00Z"}}},
			},
		},
	}

	wantStarted := time.Date(2026, 9, 20, 10, 8, 0, 0, time.UTC)
	if got := podStartedAt(pod); !got.Equal(wantStarted) {
		t.Fatalf("podStartedAt = %s, want %s", got, wantStarted)
	}

	rsTimes := map[string]time.Time{
		"api-abc": time.Date(2026, 9, 20, 9, 59, 0, 0, time.UTC),
	}
	if got := podDeployedAt(pod, rsTimes); !got.Equal(rsTimes["api-abc"]) {
		t.Fatalf("podDeployedAt = %s, want ReplicaSet creation %s", got, rsTimes["api-abc"])
	}

	wantTags := []string{"api=20260920.3", "otel=sha256:abc123"}
	if got := podImageTagLabels(pod); !reflect.DeepEqual(got, wantTags) {
		t.Fatalf("podImageTagLabels = %v, want %v", got, wantTags)
	}
}

func TestAppsProvideIndividualPodDetailsForPicker(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX kubectl fixture")
	}
	path := filepath.Join(t.TempDir(), "kubectl")
	script := `#!/bin/sh
case "$*" in
  *"get pods -o json"*) echo '{"items":[{"metadata":{"name":"api-123","labels":{"app":"api"}},"spec":{"containers":[{"name":"api"}]},"status":{"phase":"Running","startTime":"2026-09-20T10:06:00Z","containerStatuses":[{"name":"api","ready":true,"state":{"running":{"startedAt":"2026-09-20T10:08:00Z"}}}]}}]}' ;;
  *"get replicasets -o json"*) echo '{"items":[]}' ;;
  *) exit 1 ;;
esac
`
	if err := os.WriteFile(path, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	apps, err := (Runner{Binary: path, Namespace: "apollo"}).Apps(context.Background())
	if err != nil || len(apps) != 1 || len(apps[0].PodChoices) != 1 {
		t.Fatalf("app choices = %+v, error = %v", apps, err)
	}
	pod := apps[0].PodChoices[0]
	if pod.Name != "api-123" || pod.Ready != "1/1" || pod.Phase != "Running" || pod.StartedAt.IsZero() {
		t.Fatalf("unexpected pod choice: %+v", pod)
	}
}

func TestPodDeployedAtFallsBackToPodCreation(t *testing.T) {
	pod := map[string]any{
		"metadata": map[string]any{
			"creationTimestamp": "2026-09-20T11:00:00Z",
			"ownerReferences": []any{
				map[string]any{"kind": "ReplicaSet", "name": "missing-rs", "controller": true},
			},
		},
	}
	want := time.Date(2026, 9, 20, 11, 0, 0, 0, time.UTC)
	if got := podDeployedAt(pod, nil); !got.Equal(want) {
		t.Fatalf("podDeployedAt fallback = %s, want %s", got, want)
	}
}

func TestImageTagSummary(t *testing.T) {
	got := imageTagSummary(map[string]bool{"worker=42": true, "sidecar=1.2": true})
	if got != "sidecar=1.2,worker=42" {
		t.Fatalf("imageTagSummary = %q", got)
	}
}
