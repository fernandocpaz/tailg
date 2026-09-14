package kube

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
)

type ImageVersion struct {
	Pod       string
	Workload  string
	Type      string
	Container string
	Tag       string
	Image     string
}

type ImageVersionSnapshot struct {
	Context   string
	Namespace string
	Versions  []ImageVersion
}

type imageComparisonKey struct {
	Workload  string
	Type      string
	Container string
}

func PodImageVersions(payload map[string]any) []ImageVersion {
	var result []ImageVersion
	for _, rawPod := range sliceValue(payload["items"]) {
		pod := mapValue(rawPod)
		podName := stringValue(mapValue(pod["metadata"])["name"])
		workload, _ := appIdentity(pod)
		spec := mapValue(pod["spec"])
		result = appendImageVersions(result, podName, workload, "init", spec["initContainers"])
		result = appendImageVersions(result, podName, workload, "app", spec["containers"])
		result = appendImageVersions(result, podName, workload, "ephemeral", spec["ephemeralContainers"])
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Pod != result[j].Pod {
			return result[i].Pod < result[j].Pod
		}
		if imageTypeOrder(result[i].Type) != imageTypeOrder(result[j].Type) {
			return imageTypeOrder(result[i].Type) < imageTypeOrder(result[j].Type)
		}
		return result[i].Container < result[j].Container
	})
	return result
}

func appendImageVersions(result []ImageVersion, podName, workload, containerType string, containers any) []ImageVersion {
	for _, rawContainer := range sliceValue(containers) {
		container := mapValue(rawContainer)
		image := stringValue(container["image"])
		result = append(result, ImageVersion{
			Pod:       valueOrUnknown(podName),
			Workload:  valueOrUnknown(workload),
			Type:      containerType,
			Container: valueOrUnknown(stringValue(container["name"])),
			Tag:       imageTag(image),
			Image:     valueOrUnknown(image),
		})
	}
	return result
}

func imageTypeOrder(containerType string) int {
	switch containerType {
	case "init":
		return 0
	case "app":
		return 1
	default:
		return 2
	}
}

func imageTag(image string) string {
	if image == "" {
		return "unknown"
	}
	if at := strings.LastIndex(image, "@"); at >= 0 && at+1 < len(image) {
		return image[at+1:]
	}
	lastSegment := image[strings.LastIndex(image, "/")+1:]
	if colon := strings.LastIndex(lastSegment, ":"); colon >= 0 && colon+1 < len(lastSegment) {
		return lastSegment[colon+1:]
	}
	return "latest"
}

func ImageVersionsReport(payload map[string]any, namespace string) string {
	versions := PodImageVersions(payload)
	var report strings.Builder
	fmt.Fprintf(&report, "IMAGE VERSIONS | namespace=%s | pods=%d | containers=%d\n", valueOrUnknown(namespace), len(sliceValue(payload["items"])), len(versions))
	if len(versions) == 0 {
		report.WriteString("\nNo pod containers found.\n")
		return report.String()
	}

	rows := make([][]string, 0, len(versions))
	for _, version := range versions {
		rows = append(rows, []string{version.Pod, version.Type, version.Container, version.Tag, version.Image})
	}
	report.WriteString("\n")
	writeImageGrid(&report, []string{"POD", "TYPE", "CONTAINER", "TAG / DIGEST", "IMAGE"}, rows)
	return report.String()
}

func ImageVersionDifferencesReport(snapshots []ImageVersionSnapshot) string {
	contexts := make([]string, 0, len(snapshots))
	versionsByKey := map[imageComparisonKey]map[string]map[string]bool{}
	for _, snapshot := range snapshots {
		contexts = append(contexts, snapshot.Context)
		for _, version := range snapshot.Versions {
			key := imageComparisonKey{Workload: version.Workload, Type: version.Type, Container: version.Container}
			if versionsByKey[key] == nil {
				versionsByKey[key] = map[string]map[string]bool{}
			}
			if versionsByKey[key][snapshot.Context] == nil {
				versionsByKey[key][snapshot.Context] = map[string]bool{}
			}
			versionsByKey[key][snapshot.Context][version.Tag] = true
		}
	}

	keys := make([]imageComparisonKey, 0, len(versionsByKey))
	for key := range versionsByKey {
		values := make([]string, len(contexts))
		for i, contextName := range contexts {
			values[i] = joinedImageTags(versionsByKey[key][contextName])
		}
		if differingValues(values) {
			keys = append(keys, key)
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].Workload != keys[j].Workload {
			return keys[i].Workload < keys[j].Workload
		}
		if imageTypeOrder(keys[i].Type) != imageTypeOrder(keys[j].Type) {
			return imageTypeOrder(keys[i].Type) < imageTypeOrder(keys[j].Type)
		}
		return keys[i].Container < keys[j].Container
	})

	var report strings.Builder
	report.WriteString("IMAGE DIFFERENCES")
	for _, snapshot := range snapshots {
		fmt.Fprintf(&report, " | %s=%s", snapshot.Context, valueOrUnknown(snapshot.Namespace))
	}
	fmt.Fprintf(&report, " | mismatches=%d\n", len(keys))
	if len(keys) == 0 {
		report.WriteString("\nAll compared workload container image versions match.\n")
		return report.String()
	}

	headings := append([]string{"WORKLOAD", "TYPE", "CONTAINER"}, contexts...)
	rows := make([][]string, 0, len(keys))
	for _, key := range keys {
		row := []string{key.Workload, key.Type, key.Container}
		for _, contextName := range contexts {
			row = append(row, joinedImageTags(versionsByKey[key][contextName]))
		}
		rows = append(rows, row)
	}
	report.WriteString("\n")
	writeImageGrid(&report, headings, rows)
	return report.String()
}

func joinedImageTags(tags map[string]bool) string {
	if len(tags) == 0 {
		return "missing"
	}
	values := make([]string, 0, len(tags))
	for tag := range tags {
		values = append(values, tag)
	}
	sort.Strings(values)
	return strings.Join(values, ", ")
}

func differingValues(values []string) bool {
	if len(values) < 2 {
		return false
	}
	for _, value := range values[1:] {
		if value != values[0] {
			return true
		}
	}
	return false
}

func writeImageGrid(output *strings.Builder, headings []string, rows [][]string) {
	widths := make([]int, len(headings))
	for i, heading := range headings {
		widths[i] = len(heading)
	}
	for _, row := range rows {
		for i, value := range row {
			widths[i] = max(widths[i], len(value))
		}
	}
	writeImageGridSeparator(output, widths)
	writeImageGridRow(output, widths, headings)
	writeImageGridSeparator(output, widths)
	for _, row := range rows {
		writeImageGridRow(output, widths, row)
	}
	writeImageGridSeparator(output, widths)
}

func writeImageGridSeparator(output *strings.Builder, widths []int) {
	output.WriteByte('+')
	for _, width := range widths {
		output.WriteString(strings.Repeat("-", width+2))
		output.WriteByte('+')
	}
	output.WriteByte('\n')
}

func writeImageGridRow(output *strings.Builder, widths []int, values []string) {
	output.WriteByte('|')
	for i, value := range values {
		fmt.Fprintf(output, " %-*s |", widths[i], value)
	}
	output.WriteByte('\n')
}

func (r Runner) RunVersions(ctx context.Context, namespace string, output io.Writer) int {
	payload, err := r.JSON(ctx, "get", "pods")
	if err != nil {
		fmt.Fprintln(output, err)
		return 1
	}
	fmt.Fprint(output, ImageVersionsReport(payload, namespace))
	return 0
}

func RunVersionComparison(ctx context.Context, contexts []string, namespace string, output io.Writer) int {
	seen := map[string]bool{}
	snapshots := make([]ImageVersionSnapshot, 0, len(contexts))
	for _, contextName := range contexts {
		if contextName == "" {
			fmt.Fprintln(output, "context names cannot be empty")
			return 2
		}
		if seen[contextName] {
			fmt.Fprintf(output, "context %q was specified more than once\n", contextName)
			return 2
		}
		seen[contextName] = true
		runner := NewRunner(namespace, contextName)
		effectiveNamespace := namespace
		if effectiveNamespace == "" {
			var err error
			_, effectiveNamespace, err = runner.CurrentContext(ctx)
			if err != nil {
				fmt.Fprintf(output, "context %s: %v\n", contextName, err)
				return 1
			}
		}
		runner.Namespace = effectiveNamespace
		payload, err := runner.JSON(ctx, "get", "pods")
		if err != nil {
			fmt.Fprintf(output, "context %s: %v\n", contextName, err)
			return 1
		}
		snapshots = append(snapshots, ImageVersionSnapshot{
			Context: contextName, Namespace: effectiveNamespace, Versions: PodImageVersions(payload),
		})
	}
	fmt.Fprint(output, ImageVersionDifferencesReport(snapshots))
	return 0
}

func valueOrUnknown(value string) string {
	if value == "" {
		return "unknown"
	}
	return value
}
