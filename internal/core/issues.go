package core

import (
	"encoding/json"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	IssueActiveWindow = 5 * time.Minute
	defaultIssueLimit = 200
)

type IssueSeverity string

const (
	IssueError   IssueSeverity = "ERR"
	IssueWarning IssueSeverity = "WRN"
)

type Issue struct {
	Key        string
	Severity   IssueSeverity
	Kind       string
	Summary    string
	SearchTerm string
	Service    string
	Pods       []string
	Count      int
	TotalCount int
	FirstSeen  time.Time
	LastSeen   time.Time
	Increasing bool
}

type IssueStats struct {
	Groups   int
	Events   int
	Errors   int
	Warnings int
}

type issueRecord struct {
	issue  Issue
	pods   map[string]struct{}
	events []time.Time
}

type IssueRadar struct {
	mu        sync.RWMutex
	maxGroups int
	groups    map[string]*issueRecord
}

type detectedIssue struct {
	severity IssueSeverity
	kind     string
	summary  string
	search   string
}

var (
	issueHTTP5xxPattern = regexp.MustCompile(`(?i)\b(http(/[0-9.]+)?\s+|status(\s+code)?\s*[=:]?\s*|responded\s+)(5\d\d)\b`)
	issueException      = regexp.MustCompile(`(?i)\b[A-Za-z_][A-Za-z0-9_.]*(Exception|Error)\b`)
	issueTimeout        = regexp.MustCompile(`(?i)\b(timeout|timed\s+out|deadline\s+exceeded)\b`)
	issueConnection     = regexp.MustCompile(`(?i)\b(connection|socket)\s+(refused|reset|failed|closed|aborted|unavailable)\b`)
	issuePanic          = regexp.MustCompile(`(?i)\b(panic|fatal|critical|unhandled)\b`)
	issueResource       = regexp.MustCompile(`(?i)\b(deadlock|out\s+of\s+memory|oomkilled|crashloopbackoff)\b`)
	issueFailure        = regexp.MustCompile(`(?i)\b(errors?|failed|failure|faulted|unavailable)\b`)
	issueRetry          = regexp.MustCompile(`(?i)\b(retry|retrying|backoff|throttled|throttling|rate\s+limit(ed)?)\b`)
	issueZeroCounter    = regexp.MustCompile(`(?i)\b(errors?|failed|failures?|faults?|dlq)\s*[=:]\s*0\b`)
	issueNegation       = regexp.MustCompile(`(?i)\bno\s+(errors?|failures?|faults?)\b`)
	issueLogPrefix      = regexp.MustCompile(`(?i)^(\[[^\]]+\]\s*)?(\[?\d{2}:\d{2}:\d{2}(\.\d+)?\s+(ERR|ERROR|WRN|WARN|WARNING|INF|INFO|DBG|DEBUG|VRB|VERBOSE|TRC|TRACE)\]?\s*)`)
	issueGUID           = regexp.MustCompile(`(?i)\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`)
	issueLongHex        = regexp.MustCompile(`(?i)\b[0-9a-f]{12,}\b`)
	issueNumber         = regexp.MustCompile(`\b\d+(\.\d+)?\b`)
	issueWhitespace     = regexp.MustCompile(`\s+`)
)

func NewIssueRadar(maxGroups int) *IssueRadar {
	if maxGroups <= 0 {
		maxGroups = defaultIssueLimit
	}
	return &IssueRadar{maxGroups: maxGroups, groups: map[string]*issueRecord{}}
}

func (r *IssueRadar) Observe(event LogEvent) bool {
	if r == nil {
		return false
	}
	detected, ok := detectIssue(event)
	if !ok {
		return false
	}
	observed := event.ObservedAt
	if observed.IsZero() {
		observed = time.Now()
	}
	service := strings.TrimSpace(event.Container)
	if service == "" {
		service = "logs"
	}
	key := service + "\x00" + detected.kind + "\x00" + issueFingerprint(detected.summary)

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.groups == nil {
		r.groups = map[string]*issueRecord{}
	}
	record := r.groups[key]
	if record == nil {
		if len(r.groups) >= r.maxGroups {
			r.evictOldestLocked()
		}
		record = &issueRecord{
			issue: Issue{
				Key:        key,
				Severity:   detected.severity,
				Kind:       detected.kind,
				Summary:    detected.summary,
				SearchTerm: detected.search,
				Service:    service,
				FirstSeen:  observed,
			},
			pods: map[string]struct{}{},
		}
		r.groups[key] = record
	}
	record.issue.TotalCount++
	record.issue.LastSeen = observed
	if event.Pod != "" {
		record.pods[event.Pod] = struct{}{}
	}
	record.events = append(record.events, observed)
	if len(record.events) > 512 {
		record.events = append([]time.Time(nil), record.events[len(record.events)-512:]...)
	}
	return true
}

func (r *IssueRadar) Issues(now time.Time, window time.Duration) []Issue {
	if r == nil {
		return nil
	}
	if window <= 0 {
		window = IssueActiveWindow
	}
	cutoff := now.Add(-window)
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]Issue, 0, len(r.groups))
	for _, record := range r.groups {
		count := countIssueEvents(record.events, cutoff, now)
		if count == 0 {
			continue
		}
		issue := record.issue
		issue.Count = count
		issue.Pods = issuePods(record.pods)
		issue.Increasing = issueIncreasing(record.events, now)
		result = append(result, issue)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Severity != result[j].Severity {
			return result[i].Severity == IssueError
		}
		if result[i].Increasing != result[j].Increasing {
			return result[i].Increasing
		}
		if result[i].Count != result[j].Count {
			return result[i].Count > result[j].Count
		}
		return result[i].LastSeen.After(result[j].LastSeen)
	})
	return result
}

func (r *IssueRadar) Stats(now time.Time, window time.Duration) IssueStats {
	issues := r.Issues(now, window)
	stats := IssueStats{Groups: len(issues)}
	for _, issue := range issues {
		stats.Events += issue.Count
		if issue.Severity == IssueError {
			stats.Errors++
		} else {
			stats.Warnings++
		}
	}
	return stats
}

func (r *IssueRadar) Clear() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.groups = map[string]*issueRecord{}
}

func (r *IssueRadar) evictOldestLocked() {
	oldestKey := ""
	var oldest time.Time
	for key, record := range r.groups {
		if oldestKey == "" || record.issue.LastSeen.Before(oldest) {
			oldestKey = key
			oldest = record.issue.LastSeen
		}
	}
	delete(r.groups, oldestKey)
}

func detectIssue(event LogEvent) (detectedIssue, bool) {
	if event.Closed {
		summary := "log stream ended"
		if event.Err != nil {
			summary = strings.TrimSpace(event.Err.Error())
		}
		return detectedIssue{severity: IssueError, kind: "STREAM", summary: truncateIssueSummary(summary), search: issueSearchTerm(summary, "STREAM")}, true
	}
	message := strings.TrimSpace(StripANSI(event.Message))
	if message == "" || IsHeartbeat(message) {
		return detectedIssue{}, false
	}

	level := ""
	summary := message
	var object map[string]any
	if json.Unmarshal([]byte(message), &object) == nil && object != nil {
		level = firstString(object, "level", "lvl", "@l")
		if rendered := firstString(object, "msg", "message", "RenderedMessage", "@m"); rendered != "" {
			summary = rendered
		}
		if exception := firstString(object, "exception", "Exception", "@x", "error", "Error"); exception != "" {
			if firstLine := strings.SplitN(exception, "\n", 2)[0]; !strings.Contains(summary, firstLine) {
				summary += " " + firstLine
			}
		}
		if status := firstString(object, "status", "statusCode", "StatusCode", "status_code", "http.status_code"); status != "" {
			summary += " status " + status
		}
	}
	if level == "" {
		level, _ = textLogStyle(message)
	}
	summary = cleanIssueSummary(summary)
	signal := issueNegation.ReplaceAllString(issueZeroCounter.ReplaceAllString(summary, ""), "")

	severity, kind := IssueSeverity(""), ""
	switch normalizeIssueLevel(level) {
	case "ERR":
		severity, kind = IssueError, "ERROR"
	case "WRN":
		severity, kind = IssueWarning, "WARNING"
	}
	if issueHTTP5xxPattern.MatchString(signal) {
		severity, kind = IssueError, "HTTP 5XX"
	} else if issuePanic.MatchString(signal) {
		severity, kind = IssueError, "PANIC"
	} else if issueException.MatchString(signal) {
		severity, kind = IssueError, "EXCEPTION"
	} else if issueTimeout.MatchString(signal) {
		severity, kind = IssueError, "TIMEOUT"
	} else if issueConnection.MatchString(signal) {
		severity, kind = IssueError, "CONNECTION"
	} else if issueResource.MatchString(signal) {
		severity, kind = IssueError, "RESOURCE"
	} else if issueFailure.MatchString(signal) {
		severity, kind = IssueError, "FAILURE"
	} else if issueRetry.MatchString(signal) {
		severity, kind = IssueWarning, "RETRY"
	}
	if severity == "" {
		return detectedIssue{}, false
	}
	return detectedIssue{severity: severity, kind: kind, summary: truncateIssueSummary(summary), search: issueSearchTerm(summary, kind)}, true
}

func cleanIssueSummary(message string) string {
	message = StripTrailingStructuredProperties(message)
	message = issueLogPrefix.ReplaceAllString(strings.TrimSpace(message), "")
	return issueWhitespace.ReplaceAllString(strings.TrimSpace(message), " ")
}

func truncateIssueSummary(message string) string {
	message = cleanIssueSummary(message)
	runes := []rune(message)
	if len(runes) > 110 {
		return string(runes[:109]) + "…"
	}
	return message
}

func issueSearchTerm(summary, kind string) string {
	switch kind {
	case "HTTP 5XX":
		if match := issueHTTP5xxPattern.FindString(summary); match != "" {
			return match
		}
	case "PANIC":
		if match := issuePanic.FindString(summary); match != "" {
			return match
		}
	case "EXCEPTION":
		if match := issueException.FindString(summary); match != "" {
			return match
		}
	case "TIMEOUT":
		if match := issueTimeout.FindString(summary); match != "" {
			return match
		}
	case "CONNECTION":
		if match := issueConnection.FindString(summary); match != "" {
			return match
		}
	case "RESOURCE":
		if match := issueResource.FindString(summary); match != "" {
			return match
		}
	case "RETRY":
		if match := issueRetry.FindString(summary); match != "" {
			return match
		}
	}
	runes := []rune(cleanIssueSummary(summary))
	if len(runes) > 72 {
		runes = runes[:72]
	}
	return strings.TrimSpace(string(runes))
}

func issueFingerprint(summary string) string {
	value := strings.ToLower(cleanIssueSummary(summary))
	value = issueGUID.ReplaceAllString(value, "{id}")
	value = issueLongHex.ReplaceAllString(value, "{id}")
	value = issueNumber.ReplaceAllString(value, "#")
	return issueWhitespace.ReplaceAllString(strings.TrimSpace(value), " ")
}

func normalizeIssueLevel(level string) string {
	label, _ := levelStyle(level)
	return label
}

func issuePods(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for pod := range values {
		result = append(result, pod)
	}
	sort.Strings(result)
	return result
}

func countIssueEvents(events []time.Time, cutoff, now time.Time) int {
	count := 0
	for _, observed := range events {
		if !observed.Before(cutoff) && !observed.After(now) {
			count++
		}
	}
	return count
}

func issueIncreasing(events []time.Time, now time.Time) bool {
	recentStart := now.Add(-30 * time.Second)
	previousStart := now.Add(-60 * time.Second)
	recent, previous := 0, 0
	for _, observed := range events {
		switch {
		case !observed.Before(recentStart) && !observed.After(now):
			recent++
		case !observed.Before(previousStart) && observed.Before(recentStart):
			previous++
		}
	}
	return recent >= 3 && recent > previous*2
}
