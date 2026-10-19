package kube

import (
	"strings"
	"testing"
)

func TestPodImageVersionsIncludesEveryContainerTypeAndRunningImageIDs(t *testing.T) {
	payload := map[string]any{"items": []any{
		map[string]any{
			"metadata": map[string]any{
				"name": "api-1",
				"labels": map[string]any{"app.kubernetes.io/name": "api"},
			},
			"spec": map[string]any{
				"initContainers": []any{map[string]any{"name": "migrate", "image": "registry:5000/tools/migrate:42"}},
				"containers": []any{
					map[string]any{"name": "sidecar", "image": "otel/collector"},
					map[string]any{"name": "api", "image": "registry.example.com/team/api:latest"},
				},
				"ephemeralContainers": []any{map[string]any{"name": "debug", "image": "busybox:latest"}},
			},
			"status": map[string]any{
				"initContainerStatuses": []any{
					map[string]any{"name": "migrate", "imageID": "docker-pullable://registry:5000/tools/migrate@sha256:init123"},
				},
				"containerStatuses": []any{
					map[string]any{"name": "api", "imageID": "containerd://sha256:api456"},
					map[string]any{"name": "sidecar", "imageID": "docker.io/otel/collector@sha256:side789"},
				},
			},
		},
	}}
	versions := PodImageVersions(payload)
	if len(versions) != 4 {
		t.Fatalf("versions=%#v", versions)
	}
	got := map[string]ImageVersion{}
	for _, version := range versions {
		got[version.Type+"/"+version.Container] = version
		if version.Workload != "api" {
			t.Errorf("workload=%q, want api", version.Workload)
		}
	}
	for key, expected := range map[string]string{
		"init/migrate": "sha256:init123",
		"app/api": "sha256:api456",
		"app/sidecar": "sha256:side789",
		"ephemeral/debug": "unavailable",
	} {
		if got[key].ImageID != expected {
			t.Errorf("%s image ID=%q, want %q", key, got[key].ImageID, expected)
		}
	}
	if got["app/api"].Tag != "latest" {
		t.Fatalf("configured tag=%q, want latest", got["app/api"].Tag)
	}
	if versions[0].Type != "init" || versions[1].Container != "api" || versions[2].Container != "sidecar" || versions[3].Type != "ephemeral" {
		t.Fatalf("unexpected stable ordering: %#v", versions)
	}
}

func TestNormalizeImageIDHandlesCommonRuntimeFormats(t *testing.T) {
	for input, expected := range map[string]string{
		"docker-pullable://registry.example.com/team/api@sha256:abc": "sha256:abc",
		"docker://sha256:def": "sha256:def",
		"containerd://sha256:ghi": "sha256:ghi",
		"sha256:jkl": "sha256:jkl",
	} {
		if got := normalizeImageID(input); got != expected {
			t.Errorf("normalizeImageID(%q)=%q, want %q", input, got, expected)
		}
	}
}

func TestImageVersionsReportIsReadableAndHandlesEmptyNamespace(t *testing.T) {
	payload := map[string]any{"items": []any{
		map[string]any{
			"metadata": map[string]any{"name": "worker-2"},
			"spec": map[string]any{"containers": []any{
				map[string]any{"name": "worker", "image": "example/worker:latest"},
			}},
			"status": map[string]any{"containerStatuses": []any{
				map[string]any{"name": "worker", "imageID": "example/worker@sha256:worker7"},
			}},
		},
	}}
	report := ImageVersionsReport(payload, "jobs")
	for _, expected := range []string{"IMAGE VERSIONS | namespace=jobs | pods=1 | containers=1", "| POD", "| CONTAINER", "| IMAGE ID", "worker-2", "worker", "latest", "sha256:worker7", "example/worker:latest"} {
		if !strings.Contains(report, expected) {
			t.Fatalf("missing %q in report:\n%s", expected, report)
		}
	}

	empty := ImageVersionsReport(map[string]any{"items": []any{}}, "default")
	if !strings.Contains(empty, "pods=0 | containers=0") || !strings.Contains(empty, "No pod containers found.") {
		t.Fatalf("unexpected empty report: %s", empty)
	}
}

func TestImageVersionDifferencesReportUsesIDsWhenTagsAreLatest(t *testing.T) {
	qa := ImageVersionSnapshot{
		Context: "tkgs-qa", Namespace: "apollo",
		Versions: []ImageVersion{
			{Workload: "encounter", Type: "app", Container: "api", Tag: "latest", ImageID: "sha256:aaa"},
			{Workload: "patient", Type: "app", Container: "api", Tag: "latest", ImageID: "sha256:ccc"},
			{Workload: "audit", Type: "app", Container: "worker", Tag: "latest", ImageID: "sha256:ddd"},
		},
	}
	dev := ImageVersionSnapshot{
		Context: "tkgs-dev", Namespace: "apollo",
		Versions: []ImageVersion{
			{Workload: "encounter", Type: "app", Container: "api", Tag: "latest", ImageID: "sha256:bbb"},
			{Workload: "encounter", Type: "app", Container: "api", Tag: "latest", ImageID: "sha256:eee"},
			{Workload: "patient", Type: "app", Container: "api", Tag: "latest", ImageID: "sha256:ccc"},
		},
	}
	report := ImageVersionDifferencesReport([]ImageVersionSnapshot{qa, dev})
	for _, expected := range []string{
		"IMAGE VERSION DIFFERENCES | tkgs-qa=apollo | tkgs-dev=apollo | mismatches=2",
		"WORKLOAD", "tkgs-qa", "tkgs-dev", "encounter", "sha256:aaa", "sha256:bbb, sha256:eee", "audit", "missing",
	} {
		if !strings.Contains(report, expected) {
			t.Fatalf("missing %q in report:\n%s", expected, report)
		}
	}
	if strings.Contains(report, "patient") {
		t.Fatalf("matching image ID should be omitted:\n%s", report)
	}
}

func TestImageVersionDifferencesReportDetectsSameTagDifferentImageIDs(t *testing.T) {
	snapshots := []ImageVersionSnapshot{
		{
			Context: "qa", Namespace: "api",
			Versions: []ImageVersion{
				{Workload: "patient", Type: "app", Container: "api", Tag: "210", ImageID: "sha256:qa-patient"},
				{Workload: "encounter", Type: "app", Container: "api", Tag: "latest", ImageID: "sha256:same"},
			},
		},
		{
			Context: "dev", Namespace: "api",
			Versions: []ImageVersion{
				{Workload: "patient", Type: "app", Container: "api", Tag: "210", ImageID: "sha256:dev-patient"},
				{Workload: "encounter", Type: "app", Container: "api", Tag: "latest", ImageID: "sha256:same"},
			},
		},
	}
	report := ImageVersionDifferencesReport(snapshots)
	if !strings.Contains(report, "mismatches=1") || !strings.Contains(report, "unknown=0") ||
		!strings.Contains(report, "patient") || !strings.Contains(report, "sha256:qa-patient") ||
		!strings.Contains(report, "sha256:dev-patient") || !strings.Contains(report, "IMAGE ID") ||
		!strings.Contains(report, "DIFF") {
		t.Fatalf("identical tags with different running images must differ: %s", report)
	}
	if strings.Contains(report, "encounter") {
		t.Fatalf("same running Image IDs should match and be omitted: %s", report)
	}
}

func TestAnyLatestForcesImageIDComparisonAcrossContexts(t *testing.T) {
	snapshots := []ImageVersionSnapshot{
		{
			Context: "qa", Namespace: "api",
			Versions: []ImageVersion{
				{Workload: "same-build", Type: "app", Container: "api", Tag: "10452", ImageID: "sha256:same"},
				{Workload: "different-build", Type: "app", Container: "api", Tag: "10452", ImageID: "sha256:old"},
			},
		},
		{
			Context: "dev", Namespace: "api",
			Versions: []ImageVersion{
				{Workload: "same-build", Type: "app", Container: "api", Tag: "LATEST", ImageID: "sha256:same"},
				{Workload: "different-build", Type: "app", Container: "api", Tag: "latest", ImageID: "sha256:new"},
			},
		},
	}
	report := ImageVersionDifferencesReport(snapshots)
	for _, expected := range []string{"mismatches=1", "different-build", "COMPARE", "IMAGE ID", "sha256:old", "sha256:new"} {
		if !strings.Contains(report, expected) {
			t.Fatalf("missing %q in report:\n%s", expected, report)
		}
	}
	if strings.Contains(report, "same-build") {
		t.Fatalf("mixed tags with matching Image IDs should be omitted:\n%s", report)
	}
}

func TestImageVersionDifferencesReportSameImageIDDifferentTagsIsMatch(t *testing.T) {
	report := ImageVersionDifferencesReport([]ImageVersionSnapshot{
		{Context: "qa", Namespace: "apollo", Versions: []ImageVersion{
			{Workload: "patient", Type: "app", Container: "api", Tag: "105", ImageID: "sha256:same"},
		}},
		{Context: "dev", Namespace: "apollo", Versions: []ImageVersion{
			{Workload: "patient", Type: "app", Container: "api", Tag: "106", ImageID: "sha256:same"},
		}},
	})
	if !strings.Contains(report, "mismatches=0") || !strings.Contains(report, "unknown=0") ||
		!strings.Contains(report, "image IDs match") {
		t.Fatalf("different tags with the same Image ID must match: %s", report)
	}
}

func TestImageVersionDifferencesReportMissingImageIDsAreUnknown(t *testing.T) {
	report := ImageVersionDifferencesReport([]ImageVersionSnapshot{
		{Context: "qa", Namespace: "apollo", Versions: []ImageVersion{
			{Workload: "unresolved", Type: "app", Container: "api", Tag: "42", ImageID: "unavailable"},
			{Workload: "mixed", Type: "app", Container: "api", Tag: "42", ImageID: "sha256:known"},
			{Workload: "mixed", Type: "app", Container: "api", Tag: "42", ImageID: "unavailable"},
		}},
		{Context: "dev", Namespace: "apollo", Versions: []ImageVersion{
			{Workload: "unresolved", Type: "app", Container: "api", Tag: "42", ImageID: "unavailable"},
			{Workload: "mixed", Type: "app", Container: "api", Tag: "42", ImageID: "sha256:known"},
		}},
	})
	for _, expected := range []string{"mismatches=0", "unknown=2", "UNKNOWN", "IMAGE ID", "unresolved", "mixed", "unavailable"} {
		if !strings.Contains(report, expected) {
			t.Fatalf("missing Image ID cannot produce a verified match (%q): %s", expected, report)
		}
	}
}

func TestImageVersionDifferencesReportIdenticalTagsWithMixedReplicaIDs(t *testing.T) {
	report := ImageVersionDifferencesReport([]ImageVersionSnapshot{
		{Context: "qa", Versions: []ImageVersion{
			{Workload: "api", Type: "app", Container: "main", Tag: "stable", ImageID: "sha256:a"},
			{Workload: "api", Type: "app", Container: "main", Tag: "stable", ImageID: "sha256:b"},
		}},
		{Context: "dev", Versions: []ImageVersion{
			{Workload: "api", Type: "app", Container: "main", Tag: "stable", ImageID: "sha256:b"},
		}},
	})
	for _, expected := range []string{"mismatches=1", "sha256:a, sha256:b", "DIFF"} {
		if !strings.Contains(report, expected) {
			t.Fatalf("mixed replica IDs must be detected even with the same tag (%q): %s", expected, report)
		}
	}
}
