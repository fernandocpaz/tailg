package kube

import (
	"strings"
	"testing"
)

func TestPodImageVersionsIncludesEveryContainerTypeAndExtractsTags(t *testing.T) {
	payload := map[string]any{"items": []any{
		map[string]any{
			"metadata": map[string]any{"name": "api-1"},
			"spec": map[string]any{
				"initContainers": []any{map[string]any{"name": "migrate", "image": "registry:5000/tools/migrate:42"}},
				"containers": []any{
					map[string]any{"name": "sidecar", "image": "otel/collector"},
					map[string]any{"name": "api", "image": "registry.example.com/team/api:20260914"},
				},
				"ephemeralContainers": []any{map[string]any{"name": "debug", "image": "busybox@sha256:abcdef"}},
			},
		},
	}}
	versions := PodImageVersions(payload)
	if len(versions) != 4 {
		t.Fatalf("versions=%#v", versions)
	}
	got := map[string]string{}
	for _, version := range versions {
		got[version.Type+"/"+version.Container] = version.Tag
	}
	for key, expected := range map[string]string{
		"init/migrate": "42", "app/api": "20260914", "app/sidecar": "latest", "ephemeral/debug": "sha256:abcdef",
	} {
		if got[key] != expected {
			t.Errorf("%s tag=%q, want %q", key, got[key], expected)
		}
	}
	if versions[0].Type != "init" || versions[1].Container != "api" || versions[2].Container != "sidecar" || versions[3].Type != "ephemeral" {
		t.Fatalf("unexpected stable ordering: %#v", versions)
	}
}

func TestImageVersionsReportIsReadableAndHandlesEmptyNamespace(t *testing.T) {
	payload := map[string]any{"items": []any{
		map[string]any{
			"metadata": map[string]any{"name": "worker-2"},
			"spec": map[string]any{"containers": []any{
				map[string]any{"name": "worker", "image": "example/worker:v7"},
			}},
		},
	}}
	report := ImageVersionsReport(payload, "jobs")
	for _, expected := range []string{"IMAGE VERSIONS | namespace=jobs | pods=1 | containers=1", "POD", "TYPE", "TAG / DIGEST", "worker-2", "worker", "v7", "example/worker:v7"} {
		if !strings.Contains(report, expected) {
			t.Fatalf("missing %q in report:\n%s", expected, report)
		}
	}

	empty := ImageVersionsReport(map[string]any{"items": []any{}}, "default")
	if !strings.Contains(empty, "pods=0 | containers=0") || !strings.Contains(empty, "No pod containers found.") {
		t.Fatalf("unexpected empty report: %s", empty)
	}
}
