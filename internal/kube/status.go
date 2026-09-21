package kube

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/fernandocpaz/tailg/internal/core"
)

type StatusOptions struct {
	Lookback         time.Duration
	Interval         time.Duration
	Timeout          time.Duration
	Output           io.Writer
	Input            io.Reader
	Decorated        bool
	Color            bool
	Width            int
	OpenConsoles     func([]string) error
	ExamineRepos     func([]string) error
	OpenRecentErrors func([]StatusErrorSelection) error
}

type StatusErrorSelection struct {
	Count      int
	Kind       string
	Service    string
	Pod        string
	Container  string
	Summary    string
	SearchTerm string
	LastSeen   time.Time
}

type ContainerStatusSummary struct {
	Name           string
	Kind           string
	Ready          bool
	Restarts       int
	State          string
	Reason         string
	ExitCode       int
	StartedAt      string
	FinishedAt     string
	LastReason     string
	LastExitCode   int
	LastFinishedAt string
}

type PodStatusSummary struct {
	Name       string
	Phase      string
	Ready      int
	Total      int
	Restarts   int
	Issues     []string
	Containers []ContainerStatusSummary
}

func PodStatusSummaries(payload map[string]any) []PodStatusSummary {
	var result []PodStatusSummary
	for _, raw := range sliceValue(payload["items"]) {
		pod := mapValue(raw)
		ready, total := readyCounts(pod)
		result = append(result, PodStatusSummary{
			Name: podName(pod), Phase: podPhase(pod), Ready: ready, Total: total,
			Restarts: restartCount(pod), Issues: PodHealthIssues(pod), Containers: ContainerStatusSummaries(pod),
		})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result
}

func ContainerStatusSummaries(pod map[string]any) []ContainerStatusSummary {
	status := mapValue(pod["status"])
	groups := []struct {
		kind   string
		values []any
	}{{"container", sliceValue(status["containerStatuses"])}, {"init", sliceValue(status["initContainerStatuses"])}}
	var result []ContainerStatusSummary
	for _, group := range groups {
		for _, raw := range group.values {
			container := mapValue(raw)
			state, reason, exitCode, startedAt, finishedAt := summarizeContainerState(mapValue(container["state"]))
			_, lastReason, lastExitCode, _, lastFinishedAt := summarizeContainerState(mapValue(container["lastState"]))
			result = append(result, ContainerStatusSummary{
				Name: valueOr(stringValue(container["name"]), "unknown"), Kind: group.kind,
				Ready: boolValue(container["ready"]), Restarts: intValue(container["restartCount"]),
				State: state, Reason: reason, ExitCode: exitCode, StartedAt: startedAt, FinishedAt: finishedAt,
				LastReason: lastReason, LastExitCode: lastExitCode, LastFinishedAt: lastFinishedAt,
			})
		}
	}
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].Kind != result[j].Kind {
			return result[i].Kind < result[j].Kind
		}
		return result[i].Name < result[j].Name
	})
	return result
}

func summarizeContainerState(state map[string]any) (name, reason string, exitCode int, startedAt, finishedAt string) {
	if running := mapValue(state["running"]); len(running) > 0 {
		return "running", "", 0, stringValue(running["startedAt"]), ""
	}
	if waiting := mapValue(state["waiting"]); len(waiting) > 0 {
		return "waiting", stringValue(waiting["reason"]), 0, "", ""
	}
	if terminated := mapValue(state["terminated"]); len(terminated) > 0 {
		return "terminated", stringValue(terminated["reason"]), intValue(terminated["exitCode"]), stringValue(terminated["startedAt"]), stringValue(terminated["finishedAt"])
	}
	return "unknown", "", 0, "", ""
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
	return namespaceStatusReportAt(payload, namespace, time.Now())
}

func namespaceStatusReportAt(payload map[string]any, namespace string, checkedAt time.Time) (string, int) {
	return namespaceStatusReportAtStyled(payload, namespace, checkedAt, statusVisuals{})
}

func namespaceStatusReportAtStyled(payload map[string]any, namespace string, checkedAt time.Time, visuals statusVisuals) (string, int) {
	pods := sliceValue(payload["items"])
	sort.Slice(pods, func(i, j int) bool { return podName(mapValue(pods[i])) < podName(mapValue(pods[j])) })
	if len(pods) == 0 {
		return statusTable(namespace, checkedAt, "ALERT", 0, 0, nil, visuals), 1
	}
	unhealthy := 0
	var rows [][]string
	for _, raw := range pods {
		pod := mapValue(raw)
		issues := PodHealthIssues(pod)
		if len(issues) > 0 {
			unhealthy++
			ready, total := readyCounts(pod)
			rows = append(rows, []string{valueOr(podName(pod), "unknown"), podPhase(pod), fmt.Sprintf("%d/%d", ready, total), fmt.Sprint(restartCount(pod)), strings.Join(issues, "; ")})
		}
	}
	if unhealthy == 0 {
		return statusTable(namespace, checkedAt, "OK", 0, len(pods), rows, visuals), 0
	}
	return statusTable(namespace, checkedAt, "ALERT", unhealthy, len(pods), rows, visuals), unhealthy
}

func statusTable(namespace string, checkedAt time.Time, result string, unhealthy, pods int, rows [][]string, visuals statusVisuals) string {
	message := "Review unhealthy pods"
	if pods == 0 {
		message = "No pods found"
	} else if result == "OK" {
		message = "all pods healthy"
	}
	healthy := max(0, pods-unhealthy)
	statusRows := [][]string{{result, namespace, formatStatusTime(checkedAt), fmt.Sprint(pods), fmt.Sprintf("%d/%d", healthy, pods), fmt.Sprint(unhealthy), message}}
	statusHeaders := []string{"Result", "Namespace", "Checked At", "Pods", "Healthy", "Unhealthy", "Message"}
	statusMaximums := []int{7, 24, 25, 6, 9, 9, 24}
	if narrowStatusLayout(visuals) {
		statusHeaders = []string{"Status", "Value"}
		statusRows = [][]string{{"Result", result}, {"Namespace", namespace}, {"Checked At", formatStatusTime(checkedAt)}, {"Pods", fmt.Sprint(pods)}, {"Healthy", fmt.Sprintf("%d/%d", healthy, pods)}, {"Unhealthy", fmt.Sprint(unhealthy)}, {"Message", message}}
		statusMaximums = []int{12, max(20, visuals.Width-18)}
	}
	tone := toneAlert
	if result == "OK" {
		tone = toneHealthy
	}
	report := statusSection("TAILG STATUS", visuals, tone) + formatTableStyled(statusHeaders, statusRows, statusMaximums, visuals, tone, false)
	if len(rows) > 0 {
		headers := []string{"Pod", "Phase", "Ready", "Restarts", "Problem"}
		maximums := []int{32, 12, 7, 8, 44}
		if narrowStatusLayout(visuals) {
			headers = []string{"Pod", "Health", "Problem"}
			maximums = []int{28, 24, max(24, visuals.Width-60)}
			compact := make([][]string, 0, len(rows))
			for _, row := range rows {
				compact = append(compact, []string{row[0], fmt.Sprintf("%s | ready %s | restarts %s", row[1], row[2], row[3]), row[4]})
			}
			rows = compact
		}
		report += "\n" + statusSection(fmt.Sprintf("%d UNHEALTHY POD(S)", unhealthy), visuals, toneAlert) + formatTableStyled(headers, rows, maximums, visuals, toneAlert, true)
	}
	return report
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

type recentErrorGroup struct {
	issue         core.Issue
	count         int
	lastSeen      time.Time
	latestPod     string
	latestContainer string
	sources       map[string]bool
}

// RecentErrorReport scans the current application containers. Collection
// failures are returned with a useful partial report.
func (r Runner) RecentErrorReport(ctx context.Context, pods map[string]any, lookback time.Duration) (string, error) {
	return r.recentErrorReport(ctx, pods, lookback, statusVisuals{})
}

func (r Runner) recentErrorReport(ctx context.Context, pods map[string]any, lookback time.Duration, visuals statusVisuals) (string, error) {
	groups, streams, failed, scanErr := r.scanRecentErrors(ctx, pods, lookback)
	return formatRecentErrorReportAtStyled(groups, lookback, streams, failed, time.Now(), visuals), scanErr
}

func (r Runner) scanRecentErrors(ctx context.Context, pods map[string]any, lookback time.Duration) (map[string]*recentErrorGroup, int, int, error) {
	if lookback <= 0 {
		lookback = core.DefaultStatusLookback
	}
	items := inventoryFromPods(sliceValue(pods["items"]))
	groups := map[string]*recentErrorGroup{}
	var scanErrors []error
	succeeded := 0
	for _, item := range items {
		if ctx.Err() != nil {
			scanErrors = append(scanErrors, ctx.Err())
			break
		}
		events, err := r.Snapshot(ctx, item, LogOptions{Since: statusLookbackArgument(lookback), Tail: -1})
		if err != nil {
			scanErrors = append(scanErrors, fmt.Errorf("%s/%s: %w", item.Pod, item.Container, err))
			continue
		}
		succeeded++
		for _, event := range events {
			issue, ok := core.ClassifyIssue(event)
			if !ok || issue.Severity != core.IssueError {
				continue
			}
			group := groups[issue.Key]
			if group == nil {
				group = &recentErrorGroup{issue: issue, sources: map[string]bool{}}
				groups[issue.Key] = group
			}
			group.count++
			group.sources[item.Pod+"/"+item.Container] = true
			if group.latestPod == "" || event.ObservedAt.After(group.lastSeen) {
				group.lastSeen = event.ObservedAt
				group.latestPod = item.Pod
				group.latestContainer = item.Container
			}
		}
	}
	failed := len(items) - succeeded
	if len(scanErrors) > 0 {
		return groups, len(items), failed, errors.Join(scanErrors...)
	}
	return groups, len(items), failed, nil
}

func statusLookbackArgument(lookback time.Duration) string {
	minutes := max(int64(1), int64(lookback/time.Minute))
	return fmt.Sprintf("%dm", minutes)
}

func FormatRecentErrorReport(groups map[string]*recentErrorGroup, lookback time.Duration, streams, failed int) string {
	return formatRecentErrorReportAt(groups, lookback, streams, failed, time.Now())
}

func formatRecentErrorReportAt(groups map[string]*recentErrorGroup, lookback time.Duration, streams, failed int, checkedAt time.Time) string {
	return formatRecentErrorReportAtStyled(groups, lookback, streams, failed, checkedAt, statusVisuals{})
}

func formatRecentErrorReportAtStyled(groups map[string]*recentErrorGroup, lookback time.Duration, streams, failed int, checkedAt time.Time, visuals statusVisuals) string {
	minutes := max(int64(1), int64(lookback/time.Minute))
	header := fmt.Sprintf("checked=%s | lookback=%dm | streams=%d | failed=%d", formatStatusTime(checkedAt), minutes, streams, failed)
	if len(groups) == 0 {
		return "\n" + statusSection("RECENT ERRORS", visuals, toneHealthy) +
			formatTableStyled([]string{"Result", "Window"}, [][]string{{"No recent errors found", header + " | events=0"}}, []int{44, 72}, visuals, toneHealthy, false)
	}
	ordered := orderedRecentErrorGroups(groups)
	total := 0
	for _, group := range ordered {
		total += group.count
	}
	rows := make([][]string, 0, len(ordered))
	for index, group := range ordered {
		lastSeen := "-"
		if !group.lastSeen.IsZero() {
			lastSeen = formatStatusTime(group.lastSeen) + "\n" + formatStatusAge(checkedAt, group.lastSeen)
		}
		summary := group.issue.FullSummary
		if summary == "" {
			summary = group.issue.Summary
		}
		rows = append(rows, []string{fmt.Sprintf("[%d]", index+1), fmt.Sprint(group.count), lastSeen, group.issue.Kind, group.issue.Service, recentErrorSources(group), summary})
	}
	headers := []string{"Select", "Count", "Last Seen", "Type", "Service", "Pod / Container", "Summary"}
	maximums := []int{7, 5, 25, 16, 18, 32, 48}
	if narrowStatusLayout(visuals) {
		headers = []string{"Select", "Count", "Last Seen", "Error"}
		maximums = []int{7, 5, 25, max(28, visuals.Width-50)}
		compact := make([][]string, 0, len(rows))
		for _, row := range rows {
			compact = append(compact, []string{row[0], row[1], row[2], row[3] + " | " + row[4] + " | " + row[5] + " | " + row[6]})
		}
		rows = compact
	}
	return "\n" + statusSection(fmt.Sprintf("RECENT ERRORS · %d EVENT(S) IN %dm", total, minutes), visuals, toneAlert) +
		statusMetadata(header+fmt.Sprintf(" | groups=%d | events=%d", len(ordered), total), visuals) +
		formatTableStyled(headers, rows, maximums, visuals, toneAlert, true)
}

func orderedRecentErrorGroups(groups map[string]*recentErrorGroup) []*recentErrorGroup {
	ordered := make([]*recentErrorGroup, 0, len(groups))
	for _, group := range groups {
		ordered = append(ordered, group)
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].count != ordered[j].count {
			return ordered[i].count > ordered[j].count
		}
		return ordered[i].issue.Key < ordered[j].issue.Key
	})
	return ordered
}

func recentErrorSelections(groups map[string]*recentErrorGroup) []StatusErrorSelection {
	ordered := orderedRecentErrorGroups(groups)
	result := make([]StatusErrorSelection, 0, len(ordered))
	for _, group := range ordered {
		summary := group.issue.FullSummary
		if summary == "" {
			summary = group.issue.Summary
		}
		result = append(result, StatusErrorSelection{
			Count: group.count, Kind: group.issue.Kind, Service: group.issue.Service,
			Pod: group.latestPod, Container: group.latestContainer,
			Summary: summary, SearchTerm: group.issue.SearchTerm, LastSeen: group.lastSeen,
		})
	}
	return result
}

func recentErrorSources(group *recentErrorGroup) string {
	if group == nil || len(group.sources) == 0 {
		return "-"
	}
	values := make([]string, 0, len(group.sources))
	for source := range group.sources {
		values = append(values, source)
	}
	sort.Strings(values)
	return strings.Join(values, ", ")
}

func formatStatusTime(value time.Time) string {
	if value.IsZero() {
		return "-"
	}
	return value.Format(time.RFC3339)
}

func formatStatusAge(checkedAt, observedAt time.Time) string {
	age := checkedAt.Sub(observedAt)
	if age < 0 {
		age = 0
	}
	switch {
	case age < time.Minute:
		return "just now"
	case age < time.Hour:
		return fmt.Sprintf("%dm ago", int(age/time.Minute))
	case age < 24*time.Hour:
		return fmt.Sprintf("%dh %dm ago", int(age/time.Hour), int(age%time.Hour/time.Minute))
	default:
		return fmt.Sprintf("%dd %dh ago", int(age/(24*time.Hour)), int(age%(24*time.Hour)/time.Hour))
	}
}

func (r Runner) RunStatus(ctx context.Context, namespace string, options StatusOptions) int {
	if options.Lookback <= 0 {
		options.Lookback = core.DefaultStatusLookback
	}
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
	visuals := statusVisuals{Decorated: options.Decorated, Color: options.Color && options.Decorated, Width: options.Width}
	started := time.Now()
	offeredPods := map[string]bool{}
	offeredWorkloads := map[string]bool{}
	errorsScanned := false
	for {
		payload, err := statusRunner.JSON(ctx, "get", "pods")
		if err != nil {
			fmt.Fprintln(options.Output, err)
			return 1
		}
		report, count := namespaceStatusReportAtStyled(payload, namespace, time.Now(), visuals)
		fmt.Fprint(options.Output, report)
		if !errorsScanned {
			groups, streams, failed, scanErr := statusRunner.scanRecentErrors(ctx, payload, options.Lookback)
			errorReport := formatRecentErrorReportAtStyled(groups, options.Lookback, streams, failed, time.Now(), visuals)
			fmt.Fprint(options.Output, errorReport)
			if scanErr != nil {
				fmt.Fprintln(options.Output, "RECENT ERRORS INCOMPLETE |", scanErr)
			}
			if options.OpenRecentErrors != nil {
				selections := recentErrorSelections(groups)
				if len(selections) > 0 {
					if openErr := options.OpenRecentErrors(selections); openErr != nil {
						fmt.Fprintln(options.Output, "Could not open selected recent error:", openErr)
					}
				}
			}
			errorsScanned = true
		}
		if ctx.Err() != nil {
			fmt.Fprintln(options.Output, "STOPPED | status scan canceled")
			return 130
		}
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
