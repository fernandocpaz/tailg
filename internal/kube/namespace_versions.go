package kube

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
)

type namespaceVersionRow struct {
	key           imageComparisonKey
	values        []string
	basis, status string
}

// ImageNamespaceComparisonReport compares equivalent workload containers in
// multiple namespaces of one context. Non-overlapping workloads are reported
// separately rather than incorrectly counting as image-version mismatches.
func ImageNamespaceComparisonReport(snapshots []ImageVersionSnapshot) string {
	if len(snapshots) < 2 {
		return "IMAGE VERSION COMPARISON requires at least two namespaces\n"
	}
	namespaces := make([]string, len(snapshots))
	byKey := map[imageComparisonKey]map[string][]ImageVersion{}
	for i, snapshot := range snapshots {
		namespaces[i] = snapshot.Namespace
		for _, version := range snapshot.Versions {
			key := imageComparisonKey{Workload: version.Workload, Type: version.Type, Container: version.Container}
			if byKey[key] == nil {
				byKey[key] = map[string][]ImageVersion{}
			}
			byKey[key][snapshot.Namespace] = append(byKey[key][snapshot.Namespace], version)
		}
	}

	var comparable, unmatched []namespaceVersionRow
	matches, differences, unknown := 0, 0, 0
	for key, versions := range byKey {
		usesID := comparisonUsesImageID(versions)
		basis := "TAG"
		if usesID {
			basis = "IMAGE ID"
		}
		row := namespaceVersionRow{key: key, basis: basis, values: make([]string, len(namespaces))}
		available := true
		for i, namespace := range namespaces {
			pods := versions[namespace]
			row.values[i] = joinedComparisonValues(pods, usesID)
			if len(pods) == 0 {
				available = false
			}
		}
		if !available {
			row.status = "UNMATCHED"
			unmatched = append(unmatched, row)
			continue
		}
		switch {
		case usesID && hasUnavailableImageID(row.values):
			row.status = "UNKNOWN"
			unknown++
		case !usesID && hasUnknownTag(row.values):
			row.status = "UNKNOWN"
			unknown++
		case differingValues(row.values):
			row.status = "DIFF"
			differences++
		default:
			row.status = "MATCH"
			matches++
		}
		comparable = append(comparable, row)
	}

	sort.Slice(comparable, func(i, j int) bool {
		order := map[string]int{"DIFF": 0, "UNKNOWN": 1, "MATCH": 2}
		if order[comparable[i].status] != order[comparable[j].status] {
			return order[comparable[i].status] < order[comparable[j].status]
		}
		return lessNamespaceVersionKey(comparable[i].key, comparable[j].key)
	})
	sort.Slice(unmatched, func(i, j int) bool {
		return lessNamespaceVersionKey(unmatched[i].key, unmatched[j].key)
	})

	var report strings.Builder
	fmt.Fprintf(&report, "IMAGE VERSION COMPARISON | context=%s | namespaces=%s | comparable=%d | matching=%d | differing=%d | unknown=%d | unmatched=%d\n",
		valueOrUnknown(snapshots[0].Context), strings.Join(namespaces, ","), len(comparable), matches, differences, unknown, len(unmatched))
	if len(comparable) == 0 {
		report.WriteString("\nNo shared workload containers found across all requested namespaces.\n")
	} else {
		headers := append([]string{"WORKLOAD", "CONTAINER", "COMPARE"}, namespaces...)
		headers = append(headers, "RESULT")
		rows := make([][]string, 0, len(comparable))
		for _, item := range comparable {
			row := append([]string{item.key.Workload, item.key.Container, item.basis}, item.values...)
			rows = append(rows, append(row, item.status))
		}
		report.WriteString("\n")
		writeImageGrid(&report, headers, rows)
	}
	if len(unmatched) > 0 {
		report.WriteString("\nNOT COMPARABLE (workload/container absent in one or more namespaces)\n")
		headers := append([]string{"WORKLOAD", "CONTAINER"}, namespaces...)
		rows := make([][]string, 0, len(unmatched))
		for _, item := range unmatched {
			rows = append(rows, append([]string{item.key.Workload, item.key.Container}, item.values...))
		}
		writeImageGrid(&report, headers, rows)
	}
	return report.String()
}

func lessNamespaceVersionKey(left, right imageComparisonKey) bool {
	if left.Workload != right.Workload {
		return left.Workload < right.Workload
	}
	if imageTypeOrder(left.Type) != imageTypeOrder(right.Type) {
		return imageTypeOrder(left.Type) < imageTypeOrder(right.Type)
	}
	return left.Container < right.Container
}

func hasUnavailableImageID(values []string) bool {
	for _, value := range values {
		for _, item := range strings.Split(value, ", ") {
			if item == "unavailable" || item == "unknown" || item == "" {
				return true
			}
		}
	}
	return false
}

func hasUnknownTag(values []string) bool {
	for _, value := range values {
		for _, item := range strings.Split(value, ", ") {
			if item == "unknown" || item == "" {
				return true
			}
		}
	}
	return false
}

// RunNamespaceVersionComparison reads every namespace from the same explicitly
// selected (or resolved current) kubectl context. No namespace inventory is
// reused between scopes and a partial failed comparison is never printed.
func RunNamespaceVersionComparison(ctx context.Context, kubeContext string, namespaces []string, output io.Writer) int {
	if len(namespaces) < 2 {
		fmt.Fprintln(output, "specify at least two --namespace values to compare")
		return 2
	}
	seen := map[string]bool{}
	for _, namespace := range namespaces {
		if namespace == "" || strings.TrimSpace(namespace) != namespace {
			fmt.Fprintln(output, "namespace names must not be empty or padded with whitespace")
			return 2
		}
		if seen[namespace] {
			fmt.Fprintf(output, "namespace %q was specified more than once\n", namespace)
			return 2
		}
		seen[namespace] = true
	}
	if kubeContext == "" {
		var err error
		kubeContext, _, err = NewRunner("", "").CurrentContext(ctx)
		if err != nil {
			fmt.Fprintf(output, "resolve current kubectl context: %v\n", err)
			return 1
		}
		if kubeContext == "" {
			fmt.Fprintln(output, "could not determine the current Kubernetes context")
			return 1
		}
	}
	snapshots := make([]ImageVersionSnapshot, 0, len(namespaces))
	for _, namespace := range namespaces {
		runner := NewRunner(namespace, kubeContext)
		payload, err := runner.JSON(ctx, "get", "pods")
		if err != nil {
			fmt.Fprintf(output, "context %s / namespace %s: %v\n", kubeContext, namespace, err)
			return 1
		}
		snapshots = append(snapshots, ImageVersionSnapshot{
			Context: kubeContext, Namespace: namespace, Versions: PodImageVersions(payload),
		})
	}
	fmt.Fprint(output, ImageNamespaceComparisonReport(snapshots))
	return 0
}
