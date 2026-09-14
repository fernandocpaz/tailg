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
	Type      string
	Container string
	Tag       string
	Image     string
}

func PodImageVersions(payload map[string]any) []ImageVersion {
	var result []ImageVersion
	for _, rawPod := range sliceValue(payload["items"]) {
		pod := mapValue(rawPod)
		podName := stringValue(mapValue(pod["metadata"])["name"])
		spec := mapValue(pod["spec"])
		result = appendImageVersions(result, podName, "init", spec["initContainers"])
		result = appendImageVersions(result, podName, "app", spec["containers"])
		result = appendImageVersions(result, podName, "ephemeral", spec["ephemeralContainers"])
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

func appendImageVersions(result []ImageVersion, podName, containerType string, containers any) []ImageVersion {
	for _, rawContainer := range sliceValue(containers) {
		container := mapValue(rawContainer)
		image := stringValue(container["image"])
		result = append(result, ImageVersion{
			Pod:       valueOrUnknown(podName),
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

	headings := []string{"POD", "TYPE", "CONTAINER", "TAG / DIGEST", "IMAGE"}
	rows := make([][]string, 0, len(versions))
	widths := make([]int, len(headings))
	for i, heading := range headings {
		widths[i] = len(heading)
	}
	for _, version := range versions {
		row := []string{version.Pod, version.Type, version.Container, version.Tag, version.Image}
		for i, value := range row {
			widths[i] = max(widths[i], len(value))
		}
		rows = append(rows, row)
	}

	report.WriteString("\n")
	writeImageGridSeparator(&report, widths)
	writeImageGridRow(&report, widths, headings)
	writeImageGridSeparator(&report, widths)
	for _, row := range rows {
		writeImageGridRow(&report, widths, row)
	}
	writeImageGridSeparator(&report, widths)
	return report.String()
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

func valueOrUnknown(value string) string {
	if value == "" {
		return "unknown"
	}
	return value
}
