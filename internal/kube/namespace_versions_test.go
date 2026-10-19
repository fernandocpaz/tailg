package kube

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func namespaceSnapshot(namespace string, versions ...ImageVersion) ImageVersionSnapshot {
	return ImageVersionSnapshot{Context: "tkgs-qa", Namespace: namespace, Versions: versions}
}

func versionForCompare(workload, container, tag, imageID string) ImageVersion {
	return ImageVersion{Workload: workload, Type: "app", Container: container, Tag: tag, ImageID: imageID}
}

func TestNamespaceComparisonMatchesByWorkloadAndContainerNotPodName(t *testing.T) {
	apollo := namespaceSnapshot("apollo",
		versionForCompare("encounter", "api", "101", "sha256:a"),
		versionForCompare("patient", "api", "latest", "sha256:same"),
		versionForCompare("stable", "app", "230", "sha256:one"),
		versionForCompare("apollo-only", "worker", "111", "sha256:alone"),
	)
	train := namespaceSnapshot("emr2train",
		versionForCompare("encounter", "api", "102", "sha256:b"),
		versionForCompare("patient", "api", "latest", "sha256:same"),
		versionForCompare("stable", "app", "230", "sha256:different"),
		versionForCompare("train-only", "worker", "111", "sha256:alone"),
	)
	report := ImageNamespaceComparisonReport([]ImageVersionSnapshot{apollo, train})
	for _, want := range []string{
		"context=tkgs-qa", "namespaces=apollo,emr2train",
		"comparable=3", "matching=1", "differing=2", "unknown=0", "unmatched=2",
		"WORKLOAD", "CONTAINER", "COMPARE", "RESULT", "encounter", "sha256:a", "sha256:b", "DIFF",
		"patient", "IMAGE ID", "sha256:same", "stable", "sha256:one", "sha256:different", "MATCH",
		"NOT COMPARABLE", "apollo-only", "train-only", "missing",
	} {
		if !strings.Contains(report, want) {
			t.Fatalf("report missing %q:\n%s", want, report)
		}
	}
	if strings.Index(report, "encounter") > strings.Index(report, "patient") {
		t.Fatal("differences must sort above matching versions")
	}
	if !strings.Contains(report, "sha256:different") {
		t.Fatal("a numbered tag must not hide different image IDs")
	}
}

func TestNamespaceComparisonUsesImageIDsForMixedTags(t *testing.T) {
	report := ImageNamespaceComparisonReport([]ImageVersionSnapshot{
		namespaceSnapshot("apollo",
			versionForCompare("same", "api", "105", "sha256:yes"),
			versionForCompare("diff", "api", "105", "sha256:old"),
			versionForCompare("not-ready", "api", "latest", "unavailable"),
		),
		namespaceSnapshot("emr2train",
			versionForCompare("same", "api", "LATEST", "sha256:yes"),
			versionForCompare("diff", "api", "latest", "sha256:new"),
			versionForCompare("not-ready", "api", "latest", "unavailable"),
		),
	})
	for _, want := range []string{"comparable=3", "matching=1", "differing=1", "unknown=1", "IMAGE ID", "sha256:old", "sha256:new", "UNKNOWN"} {
		if !strings.Contains(report, want) {
			t.Fatalf("report missing %q:\n%s", want, report)
		}
	}
}

func TestNamespaceComparisonHandlesNoSharedWorkloadsAndMissingContainers(t *testing.T) {
	report := ImageNamespaceComparisonReport([]ImageVersionSnapshot{
		namespaceSnapshot("apollo", versionForCompare("service", "api", "7", "sha256:old")),
		namespaceSnapshot("emr2train", versionForCompare("service", "sidecar", "7", "sha256:new")),
	})
	if !strings.Contains(report, "comparable=0") || !strings.Contains(report, "unmatched=2") ||
		!strings.Contains(report, "No shared workload containers found") ||
		!strings.Contains(report, "sidecar") || !strings.Contains(report, "api") {
		t.Fatalf("unexpected no-overlap report:\n%s", report)
	}
}

func TestNamespaceComparisonDetectsMixedReplicaVersions(t *testing.T) {
	report := ImageNamespaceComparisonReport([]ImageVersionSnapshot{
		namespaceSnapshot("apollo", versionForCompare("api", "main", "200", "a"), versionForCompare("api", "main", "201", "b")),
		namespaceSnapshot("emr2train", versionForCompare("api", "main", "201", "c")),
	})
	for _, want := range []string{"a, b", "c", "DIFF"} {
		if !strings.Contains(report, want) {
			t.Fatalf("unexpected mixed rollout report:\n%s", report)
		}
	}
}

func TestNamespaceVersionComparisonRejectsDuplicateBeforeKubernetes(t *testing.T) {
	var output bytes.Buffer
	code := RunNamespaceVersionComparison(context.Background(), "tkgs-qa", []string{"apollo", "apollo"}, &output)
	if code != 2 || !strings.Contains(output.String(), "more than once") {
		t.Fatalf("expected duplicate argument error, code=%d output=%q", code, output.String())
	}
}

func TestNamespaceComparisonSameTagDifferentIDsAlwaysDiff(t *testing.T) {
	report := ImageNamespaceComparisonReport([]ImageVersionSnapshot{
		namespaceSnapshot("apollo",
			versionForCompare("service", "main", "200", "sha256:old"),
			versionForCompare("same-binary", "main", "100", "sha256:same"),
		),
		namespaceSnapshot("emr2train",
			versionForCompare("service", "main", "200", "sha256:new"),
			versionForCompare("same-binary", "main", "101", "sha256:same"),
		),
	})
	for _, expected := range []string{"matching=1", "differing=1", "IMAGE ID", "sha256:old", "sha256:new", "DIFF", "MATCH"} {
		if !strings.Contains(report, expected) {
			t.Fatalf("Image ID must be authoritative regardless of tag (%q): %s", expected, report)
		}
	}
}

func TestNamespaceComparisonMissingIDsNeverMatch(t *testing.T) {
	report := ImageNamespaceComparisonReport([]ImageVersionSnapshot{
		namespaceSnapshot("apollo",
			versionForCompare("missing-both", "api", "200", "unavailable"),
			versionForCompare("missing-one", "api", "201", "unavailable"),
			versionForCompare("mixed-replicas", "api", "202", "sha256:known"),
			versionForCompare("mixed-replicas", "api", "202", "unavailable"),
		),
		namespaceSnapshot("emr2train",
			versionForCompare("missing-both", "api", "200", "unavailable"),
			versionForCompare("missing-one", "api", "201", "sha256:yes"),
			versionForCompare("mixed-replicas", "api", "202", "sha256:known"),
		),
	})
	for _, expected := range []string{"matching=0", "differing=0", "unknown=3", "UNKNOWN", "IMAGE ID", "unavailable"} {
		if !strings.Contains(report, expected) {
			t.Fatalf("missing image ID cannot be reported as a match (%q): %s", expected, report)
		}
	}
}
