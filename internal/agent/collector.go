package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/fernandocpaz/tailg/internal/core"
	"github.com/fernandocpaz/tailg/internal/kube"
)

type KubernetesClient interface {
	Snapshot(context.Context, core.InventoryItem, kube.LogOptions) ([]core.LogEvent, error)
	JSON(context.Context, ...string) (map[string]any, error)
}

type Collector struct {
	Client KubernetesClient
}

type collectedEvent struct {
	event core.LogEvent
	key   string
}

func (c Collector) Collect(ctx context.Context, items []core.InventoryItem, options CollectOptions) (Report, error) {
	if c.Client == nil {
		return Report{}, fmt.Errorf("Kubernetes client is not configured")
	}
	now := time.Now().UTC()
	if options.Now != nil {
		now = options.Now().UTC()
	}
	kind := "IssueReport"
	if options.Mode == ModeDiagnose {
		kind = "DiagnosticReport"
	}
	report := Report{
		SchemaVersion: SchemaVersion, Kind: kind, GeneratedAt: timestamp(now),
		Window: valueOr(options.Since, "tail"), Limits: options.Limits,
		Scope: Scope{Context: options.Context, Namespace: options.Namespace, Target: options.Target, Pods: core.UniquePods(items)},
		Pods:  []Pod{}, Issues: []Issue{}, KubernetesEvents: []KubernetesEvent{}, Recommendations: []string{}, CollectionErrors: []CollectionError{},
	}
	items = uniqueStreams(items)
	report.Scope.Workloads = selectedWorkloads(items, options.Workloads)
	report.Coverage = Coverage{Status: "complete", ExpectedStreams: len(items), Reasons: []string{}}
	if len(items) == 0 {
		markCoverage(&report, "no_selected_streams", true)
	}
	selectedPods := make(map[string]bool, len(report.Scope.Pods))
	for _, pod := range report.Scope.Pods {
		selectedPods[pod] = true
	}

	if options.Mode == ModeDiagnose {
		payload, err := c.Client.JSON(ctx, "get", "pods")
		if err != nil {
			report.CollectionErrors = append(report.CollectionErrors, collectionError("pods", err))
			markCoverage(&report, "pod_metadata_collection_failed", false)
		} else {
			foundPods := make(map[string]bool)
			for _, status := range kube.PodStatusSummaries(payload) {
				if !selectedPods[status.Name] {
					continue
				}
				pod := Pod{Name: status.Name, Phase: status.Phase, Ready: status.Ready, Total: status.Total, Restarts: status.Restarts, Issues: redactStrings(status.Issues), Containers: []Container{}}
				for _, container := range status.Containers {
					pod.Containers = append(pod.Containers, Container{
						Name: container.Name, Kind: container.Kind, Ready: container.Ready, Restarts: container.Restarts,
						State: container.State, Reason: Redact(container.Reason), ExitCode: container.ExitCode,
						StartedAt: container.StartedAt, FinishedAt: container.FinishedAt,
						LastReason: Redact(container.LastReason), LastExitCode: container.LastExitCode, LastFinishedAt: container.LastFinishedAt,
					})
				}
				report.Pods = append(report.Pods, pod)
				foundPods[status.Name] = true
				if len(status.Issues) > 0 {
					report.Summary.UnhealthyPods++
				}
			}
			for pod := range selectedPods {
				if !foundPods[pod] {
					markCoverage(&report, "selected_pod_missing_from_metadata", false)
				}
			}
		}
		payload, err = c.Client.JSON(ctx, "get", "events", "--sort-by=.lastTimestamp")
		if err != nil {
			report.CollectionErrors = append(report.CollectionErrors, collectionError("events", err))
			markCoverage(&report, "event_collection_failed", false)
		} else {
			report.KubernetesEvents = warningEvents(payload, selectedPods, 50)
			if len(report.KubernetesEvents) >= 50 {
				markCoverage(&report, "event_limit_reached", false)
			}
		}
	}

	radar := core.NewIssueRadar(options.Limits.MaxIssues)
	var events []collectedEvent
	snapshotSuccess := 0
	issueKeys := make(map[string]struct{})
	for _, item := range items {
		if len(events) >= options.Limits.MaxLines {
			report.Truncated = true
			markCoverage(&report, "max_lines_reached", false)
			break
		}
		rows, err := c.Client.Snapshot(ctx, item, kube.LogOptions{Since: options.Since, Tail: options.Limits.Tail})
		if err != nil {
			report.CollectionErrors = append(report.CollectionErrors, collectionError(item.Pod+"/"+item.Container, err))
			markCoverage(&report, "log_collection_failed", false)
			continue
		}
		snapshotSuccess++
		report.Coverage.CollectedStreams++
		if options.Limits.Tail > 0 && len(rows) >= options.Limits.Tail {
			markCoverage(&report, "tail_limit_reached", false)
		}
		for _, event := range rows {
			if len(events) >= options.Limits.MaxLines {
				report.Truncated = true
				markCoverage(&report, "max_lines_reached", false)
				break
			}
			if options.IncludeEvent != nil && !options.IncludeEvent(event.Message) {
				continue
			}
			event.Workload = options.Workloads[event.Pod]
			classified, ok := core.ClassifyIssue(event)
			key := ""
			if ok {
				key = classified.Key
				radar.Observe(event)
				issueKeys[key] = struct{}{}
			}
			events = append(events, collectedEvent{event: event, key: key})
		}
		if options.Limits.MaxLines > 0 && len(events) >= options.Limits.MaxLines {
			report.Truncated = true
			markCoverage(&report, "max_lines_reached", false)
		}
	}
	issueLimit := options.Limits.MaxIssues
	if issueLimit <= 0 {
		issueLimit = 200
	}
	if len(issueKeys) > issueLimit {
		markCoverage(&report, "issue_group_limit_reached", false)
	}
	if snapshotSuccess == 0 && len(items) > 0 {
		markCoverage(&report, "no_log_streams_collected", true)
		refreshSummary(&report)
		return report, fmt.Errorf("could not collect logs from any selected container")
	}
	sort.SliceStable(events, func(i, j int) bool {
		left, right := events[i].event.ObservedAt, events[j].event.ObservedAt
		if left.Equal(right) {
			return events[i].event.Pod+"\x00"+events[i].event.Container < events[j].event.Pod+"\x00"+events[j].event.Container
		}
		if left.IsZero() {
			return false
		}
		if right.IsZero() {
			return true
		}
		return left.Before(right)
	})
	report.Summary.LogLines = len(events)
	analysisNow := now
	var latestObserved time.Time
	for _, item := range events {
		if item.event.ObservedAt.After(latestObserved) {
			latestObserved = item.event.ObservedAt
		}
	}
	if !latestObserved.IsZero() && latestObserved.Before(analysisNow.Add(-core.IssueActiveWindow)) {
		analysisNow = latestObserved
	}
	issues := radar.Issues(analysisNow, core.IssueActiveWindow)
	for _, issue := range issues {
		id := issueID(issue.Key)
		if options.IssueID != "" && id != options.IssueID {
			continue
		}
		report.Issues = append(report.Issues, Issue{
			ID: id, Severity: severityName(issue.Severity), Kind: issue.Kind,
			Workload: issue.Workload,
			Summary:  Redact(issue.Summary), SearchTerm: Redact(issue.SearchTerm), Service: issue.Service,
			Pods: issue.Pods, Count: issue.Count, TotalCount: issue.TotalCount,
			FirstSeen: timestamp(issue.FirstSeen), LastSeen: timestamp(issue.LastSeen), Increasing: issue.Increasing,
			New: issue.New, MaxDurationMs: durationMilliseconds(issue.MaxDuration),
			TraceID: Redact(issue.TraceID), Endpoint: Redact(issue.Endpoint),
			Context: contextFor(events, issue.Key, options.Limits.ContextLines),
		})
	}
	refreshSummary(&report)
	report.Recommendations = recommendations(report)
	return report, nil
}

func durationMilliseconds(value time.Duration) float64 {
	if value <= 0 {
		return 0
	}
	return float64(value) / float64(time.Millisecond)
}

func refreshSummary(report *Report) {
	report.Summary.IssueGroups = len(report.Issues)
	report.Summary.IssueEvents = 0
	report.Summary.Errors = 0
	report.Summary.Warnings = 0
	for _, issue := range report.Issues {
		report.Summary.IssueEvents += issue.Count
		if issue.Severity == "error" {
			report.Summary.Errors++
		} else {
			report.Summary.Warnings++
		}
	}
	switch {
	case report.Summary.Errors > 0 || report.Summary.UnhealthyPods > 0:
		report.Summary.Status = "error"
	case report.Summary.Warnings > 0 || len(report.CollectionErrors) > 0:
		report.Summary.Status = "warning"
	case report.Coverage.Status != "complete":
		report.Summary.Status = "unknown"
	default:
		report.Summary.Status = "healthy"
	}
}

func uniqueStreams(items []core.InventoryItem) []core.InventoryItem {
	seen := make(map[string]bool, len(items))
	result := make([]core.InventoryItem, 0, len(items))
	for _, item := range items {
		if seen[item.Key()] {
			continue
		}
		seen[item.Key()] = true
		result = append(result, item)
	}
	return result
}

func selectedWorkloads(items []core.InventoryItem, workloads map[string]string) []string {
	seen := make(map[string]bool)
	result := make([]string, 0)
	for _, item := range items {
		workload := strings.TrimSpace(workloads[item.Pod])
		if workload == "" || seen[workload] {
			continue
		}
		seen[workload] = true
		result = append(result, workload)
	}
	sort.Strings(result)
	return result
}

func markCoverage(report *Report, reason string, unavailable bool) {
	for _, existing := range report.Coverage.Reasons {
		if existing == reason {
			if unavailable {
				report.Coverage.Status = "unavailable"
			}
			return
		}
	}
	report.Coverage.Reasons = append(report.Coverage.Reasons, reason)
	if unavailable {
		report.Coverage.Status = "unavailable"
	} else if report.Coverage.Status != "unavailable" {
		report.Coverage.Status = "partial"
	}
}

func recommendations(report Report) []string {
	result := make([]string, 0, 8)
	seen := map[string]bool{}
	add := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] || len(result) >= 8 {
			return
		}
		seen[value] = true
		result = append(result, value)
	}
	for _, pod := range report.Pods {
		for _, container := range pod.Containers {
			currentReason := strings.ToLower(container.Reason)
			lastReason := strings.ToLower(container.LastReason)
			switch lastReason {
			case "oomkilled":
				add(fmt.Sprintf("%s/%s was OOMKilled (exit %d); capture a --dump bundle to preserve previous logs and review memory requests, limits, and usage.", pod.Name, container.Name, container.LastExitCode))
			case "error":
				if container.Restarts > 0 {
					add(fmt.Sprintf("%s/%s restarted %d time(s) after an error; inspect previous-container logs and the first failure before the restart.", pod.Name, container.Name, container.Restarts))
				}
			}
			switch currentReason {
			case "crashloopbackoff":
				add(fmt.Sprintf("%s/%s is in CrashLoopBackOff; inspect its last termination and previous logs before changing the deployment.", pod.Name, container.Name))
			case "imagepullbackoff", "errimagepull":
				add(fmt.Sprintf("%s/%s cannot pull its image; verify the image/tag, registry reachability, and imagePullSecrets using the Warning event details.", pod.Name, container.Name))
			case "createcontainerconfigerror", "createcontainererror":
				add(fmt.Sprintf("%s/%s cannot start because of container configuration; inspect referenced ConfigMaps, Secrets, volumes, and Warning events.", pod.Name, container.Name))
			}
		}
		for _, issue := range pod.Issues {
			lower := strings.ToLower(issue)
			switch {
			case strings.Contains(lower, "not fully ready") || strings.Contains(lower, "not ready"):
				add("A selected pod is not ready; inspect readiness/liveness probe configuration, endpoint health, dependencies, and recent Warning events.")
			case strings.Contains(lower, "scheduling"):
				add("A selected pod is not scheduling; inspect resource requests, node selectors/affinity, taints/tolerations, and PVC binding.")
			}
		}
	}
	if len(report.KubernetesEvents) > 0 {
		add("Review the recent Kubernetes Warning events alongside the first matching log error; event reasons often identify scheduling, probe, mount, or image failures before the application logs do.")
	}
	for _, issue := range report.Issues {
		if issue.Increasing {
			add(fmt.Sprintf("Issue %s (%s) is increasing; use its stable issue ID with `tailg issue` to inspect bounded context before the signal scrolls out of retention.", issue.ID, issue.Kind))
		}
	}
	return result
}

func severityName(value core.IssueSeverity) string {
	if value == core.IssueError {
		return "error"
	}
	return "warning"
}

func ExitCode(report Report) int {
	switch report.Summary.Status {
	case "error":
		return 2
	case "warning":
		return 1
	case "unknown":
		return 3
	default:
		return 0
	}
}

func contextFor(events []collectedEvent, key string, radius int) IssueContext {
	match := -1
	for index := range events {
		if events[index].key == key {
			match = index
		}
	}
	if match < 0 {
		return IssueContext{Before: []LogLine{}, After: []LogLine{}}
	}
	result := IssueContext{Before: []LogLine{}, Match: logLine(events[match].event), After: []LogLine{}}
	for index := match - 1; index >= 0 && len(result.Before) < radius; index-- {
		if sameStream(events[index].event, events[match].event) {
			result.Before = append([]LogLine{logLine(events[index].event)}, result.Before...)
		}
	}
	for index := match + 1; index < len(events) && len(result.After) < radius; index++ {
		if sameStream(events[index].event, events[match].event) {
			result.After = append(result.After, logLine(events[index].event))
		}
	}
	return result
}

func sameStream(left, right core.LogEvent) bool {
	return left.Pod == right.Pod && left.Container == right.Container
}
func logLine(event core.LogEvent) LogLine {
	return LogLine{Timestamp: timestamp(event.ObservedAt), Pod: event.Pod, Container: event.Container, Message: Redact(event.Message)}
}
func timestamp(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}
func issueID(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:8])
}
func collectionError(source string, err error) CollectionError {
	return CollectionError{Source: source, Message: Redact(err.Error())}
}
func redactStrings(values []string) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = Redact(value)
	}
	return result
}
func valueOr(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func warningEvents(payload map[string]any, selectedPods map[string]bool, limit int) []KubernetesEvent {
	var result []KubernetesEvent
	for _, raw := range anySlice(payload["items"]) {
		item := anyMap(raw)
		involved := anyMap(item["involvedObject"])
		objectName := anyString(involved["name"])
		if len(selectedPods) > 0 && !selectedPods[objectName] {
			continue
		}
		eventType := anyString(item["type"])
		if !strings.EqualFold(eventType, "Warning") {
			continue
		}
		result = append(result, KubernetesEvent{
			Timestamp: valueOr(anyString(item["lastTimestamp"]), anyString(anyMap(item["eventTime"])["time"])),
			Type:      eventType, Reason: anyString(item["reason"]), Object: anyString(involved["kind"]) + "/" + objectName,
			Message: Redact(anyString(item["message"])),
		})
	}
	if len(result) > limit {
		result = result[len(result)-limit:]
	}
	return result
}

func anyMap(value any) map[string]any {
	if result, ok := value.(map[string]any); ok {
		return result
	}
	return map[string]any{}
}
func anySlice(value any) []any {
	if result, ok := value.([]any); ok {
		return result
	}
	return nil
}
func anyString(value any) string {
	if value == nil {
		return ""
	}
	return fmt.Sprint(value)
}
