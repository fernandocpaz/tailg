package kube

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/fernandocpaz/tailg/internal/core"
)

type StatusOptions struct {
	Interval     time.Duration
	Timeout      time.Duration
	Output       io.Writer
	Input        io.Reader
	OpenConsoles func([]string) error
	ExamineRepos func([]string) error
}

type PodStatusSummary struct {
	Name     string
	Phase    string
	Ready    int
	Total    int
	Restarts int
	Issues   []string
}

func PodStatusSummaries(payload map[string]any) []PodStatusSummary {
	var result []PodStatusSummary
	for _, raw := range sliceValue(payload["items"]) {
		pod := mapValue(raw)
		ready, total := readyCounts(pod)
		result = append(result, PodStatusSummary{
			Name: podName(pod), Phase: podPhase(pod), Ready: ready, Total: total,
			Restarts: restartCount(pod), Issues: PodHealthIssues(pod),
		})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result
}

func PodHealthIssues(pod map[string]any) []string {
	metadata, spec, status := mapValue(pod["metadata"]), mapValue(pod["spec"]), mapValue(pod["status"])
	phase := podPhase(pod)
	if phase == "Succeeded" {
		return nil
	}
	var issues []string
	if phase != "Running" {
		issues = append(issues, "phase is "+phase)
	}
	ready, total := readyCounts(pod)
	if total == 0 {
		issues = append(issues, "no application container status is available")
	} else if ready < total {
		issues = append(issues, fmt.Sprintf("not fully ready (%d/%d)", ready, total))
	}
	if reason := stringValue(status["reason"]); reason != "" {
		detail := ""
		if message := stringValue(status["message"]); message != "" {
			detail = ": " + message
		}
		issues = append(issues, "pod reason "+reason+detail)
	}
	for _, raw := range sliceValue(status["conditions"]) {
		condition := mapValue(raw)
		if stringValue(condition["type"]) == "PodScheduled" && stringValue(condition["status"]) == "False" {
			reason := valueOr(stringValue(condition["reason"]), "not scheduled")
			detail := ""
			if message := stringValue(condition["message"]); message != "" {
				detail = ": " + message
			}
			issues = append(issues, "scheduling "+reason+detail)
		}
	}
	containerNames := map[string]bool{}
	for _, raw := range sliceValue(spec["containers"]) {
		name := stringValue(mapValue(raw)["name"])
		if name != "" {
			containerNames[name] = true
		}
	}
	groups := []struct {
		name   string
		values []any
	}{{"container", sliceValue(status["containerStatuses"])}, {"init container", sliceValue(status["initContainerStatuses"])}}
	for _, group := range groups {
		for _, raw := range group.values {
			container := mapValue(raw)
			name := valueOr(stringValue(container["name"]), "unknown")
			state := mapValue(container["state"])
			waiting := mapValue(state["waiting"])
			terminated := mapValue(state["terminated"])
			if len(waiting) > 0 {
				reason := valueOr(stringValue(waiting["reason"]), "Waiting")
				detail := ""
				if message := stringValue(waiting["message"]); message != "" {
					detail = ": " + message
				}
				issues = append(issues, fmt.Sprintf("%s %s waiting: %s%s", group.name, name, reason, detail))
			} else if len(terminated) > 0 {
				exitCode := intValue(terminated["exitCode"])
				reason := valueOr(stringValue(terminated["reason"]), "Terminated")
				if group.name == "container" || exitCode != 0 {
					issues = append(issues, fmt.Sprintf("%s %s terminated: %s exitCode=%d", group.name, name, reason, exitCode))
				}
			} else if group.name == "container" && containerNames[name] && !boolValue(container["ready"]) {
				issues = append(issues, "container "+name+" is not ready")
			}
		}
	}
	if stringValue(metadata["deletionTimestamp"]) != "" && phase != "Running" && phase != "Succeeded" {
		issues = append(issues, "pod is terminating")
	}
	return uniqueStrings(issues)
}

func NamespaceStatusReport(payload map[string]any, namespace string) (string, int) {
	pods := sliceValue(payload["items"])
	sort.Slice(pods, func(i, j int) bool { return podName(mapValue(pods[i])) < podName(mapValue(pods[j])) })
	if len(pods) == 0 {
		return fmt.Sprintf("NO PODS | namespace=%s | nothing to scan\n", namespace), 1
	}
	type unhealthyPod struct {
		pod    map[string]any
		issues []string
	}
	var unhealthy []unhealthyPod
	for _, raw := range pods {
		pod := mapValue(raw)
		if issues := PodHealthIssues(pod); len(issues) > 0 {
			unhealthy = append(unhealthy, unhealthyPod{pod, issues})
		}
	}
	if len(unhealthy) == 0 {
		return fmt.Sprintf("OK | namespace=%s | pods=%d | all pods healthy\n", namespace, len(pods)), 0
	}
	lines := []string{fmt.Sprintf("ALERT | namespace=%s | unhealthy=%d/%d pods", namespace, len(unhealthy), len(pods)), ""}
	for _, item := range unhealthy {
		ready, total := readyCounts(item.pod)
		lines = append(lines, fmt.Sprintf("%s | phase=%s | ready=%d/%d | restarts=%d", valueOr(podName(item.pod), "unknown"), podPhase(item.pod), ready, total, restartCount(item.pod)))
		for _, issue := range item.issues {
			lines = append(lines, "  - "+issue)
		}
		lines = append(lines, "")
	}
	return strings.TrimRight(strings.Join(lines, "\n"), "\n") + "\n", len(unhealthy)
}

func UnhealthyPodNames(payload map[string]any) []string {
	var result []string
	for _, raw := range sliceValue(payload["items"]) {
		pod := mapValue(raw)
		if len(PodHealthIssues(pod)) > 0 && podName(pod) != "" {
			result = append(result, podName(pod))
		}
	}
	sort.Strings(result)
	return uniqueStrings(result)
}
func UnhealthyWorkloadNames(payload map[string]any) []string {
	var result []string
	for _, raw := range sliceValue(payload["items"]) {
		pod := mapValue(raw)
		if len(PodHealthIssues(pod)) > 0 {
			name, _ := appIdentity(pod)
			if name != "" {
				result = append(result, name)
			}
		}
	}
	sort.Strings(result)
	return uniqueStrings(result)
}

func (r Runner) RunStatus(ctx context.Context, namespace string, options StatusOptions) int {
	if options.Interval <= 0 {
		options.Interval = core.DefaultStatusInterval
	}
	if options.Timeout <= 0 {
		options.Timeout = core.DefaultStatusTimeout
	}
	if options.Output == nil {
		options.Output = io.Discard
	}
	statusRunner := r
	statusRunner.Namespace = namespace
	started := time.Now()
	offeredPods := map[string]bool{}
	offeredWorkloads := map[string]bool{}
	for {
		payload, err := statusRunner.JSON(ctx, "get", "pods")
		if err != nil {
			fmt.Fprintln(options.Output, err)
			return 1
		}
		report, count := NamespaceStatusReport(payload, namespace)
		fmt.Fprint(options.Output, report)
		if count == 0 {
			if time.Since(started) > time.Second {
				fmt.Fprintf(options.Output, "RECOVERED | namespace=%s is healthy after %s\n", namespace, core.FormatDuration(time.Since(started), true))
			}
			return 0
		}
		newPods := difference(UnhealthyPodNames(payload), offeredPods)
		for _, name := range newPods {
			offeredPods[name] = true
		}
		if len(newPods) > 0 && options.OpenConsoles != nil && confirm(options.Input, options.Output, fmt.Sprintf("Open console windows for %s?", strings.Join(newPods, ", "))) {
			if err := options.OpenConsoles(newPods); err != nil {
				fmt.Fprintln(options.Output, "Could not open unhealthy pod consoles:", err)
			}
		}
		newWorkloads := difference(UnhealthyWorkloadNames(payload), offeredWorkloads)
		for _, name := range newWorkloads {
			offeredWorkloads[name] = true
		}
		if len(newWorkloads) > 0 && options.ExamineRepos != nil && confirm(options.Input, options.Output, fmt.Sprintf("Examine recent Git changes for %s?", strings.Join(newWorkloads, ", "))) {
			if err := options.ExamineRepos(newWorkloads); err != nil {
				fmt.Fprintln(options.Output, "Could not examine repository changes:", err)
			}
		}
		elapsed := time.Since(started)
		if elapsed >= options.Timeout {
			fmt.Fprintf(options.Output, "TIMEOUT | namespace=%s did not become healthy within %s\n", namespace, core.FormatDuration(options.Timeout, true))
			return 1
		}
		fmt.Fprintf(options.Output, "WAIT | namespace=%s | elapsed=%s | timeout-in=%s | unhealthy=%d\n", namespace, core.FormatDuration(elapsed, true), core.FormatDuration(options.Timeout-elapsed, true), count)
		select {
		case <-ctx.Done():
			fmt.Fprintf(options.Output, "STOPPED | namespace=%s status wait cancelled\n", namespace)
			return 130
		case <-time.After(min(options.Interval, options.Timeout-elapsed)):
		}
	}
}

func confirm(input io.Reader, output io.Writer, prompt string) bool {
	if input == nil {
		return false
	}
	fmt.Fprint(output, prompt+" [y/N] ")
	scanner := bufio.NewScanner(input)
	if !scanner.Scan() {
		return false
	}
	answer := strings.ToLower(strings.TrimSpace(scanner.Text()))
	return answer == "y" || answer == "yes"
}
func difference(values []string, seen map[string]bool) []string {
	var result []string
	for _, value := range values {
		if !seen[value] {
			result = append(result, value)
		}
	}
	return result
}
