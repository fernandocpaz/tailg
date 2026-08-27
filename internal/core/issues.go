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

// ClassifyIssue returns the normalized issue represented by a log event. The
// returned key is stable for events that IssueRadar would group together.
func ClassifyIssue(event LogEvent) (Issue, bool) {
	detected, ok := detectIssue(event)
	if !ok {
		return Issue{}, false
	}
	service := strings.TrimSpace(event.Container)
	if service == "" {
		service = "logs"
	}
	key := service + "\x00" + detected.kind + "\x00" + issueFingerprint(detected.summary)
	return Issue{Key: key, Severity: detected.severity, Kind: detected.kind, Summary: detected.summary, SearchTerm: detected.search, Service: service}, true
}

type issueRecord struct {
	issue   Issue
	buckets map[int64]*issueBucket
}

type issueBucket struct {
	count int
	pods  map[string]struct{}
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
	issueLeadingLevel   = regexp.MustCompile(`(?i)^\s*(ERR|ERROR|FATAL|CRIT|CRITICAL|WRN|WARN|WARNING)(\s+|:\s*|$)`)
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
	classified, ok := ClassifyIssue(event)
	if !ok {
		return false
	}
	observed := event.ObservedAt
	if observed.IsZero() {
		observed = time.Now()
	}
	key := classified.Key

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
				Severity:   classified.Severity,
				Kind:       classified.Kind,
				Summary:    classified.Summary,
				SearchTerm: classified.SearchTerm,
				Service:    classified.Service,
				FirstSeen:  observed,
			},
			buckets: map[int64]*issueBucket{},
		}
		r.groups[key] = record
	}
	record.issue.TotalCount++
	if observed.Before(record.issue.FirstSeen) {
		record.issue.FirstSeen = observed
	}
	if record.issue.LastSeen.IsZero() || observed.After(record.issue.LastSeen) {
		record.issue.LastSeen = observed
	}
	r.pruneBucketsLocked(record)
	second := observed.Unix()
	if second >= record.issue.LastSeen.Add(-IssueActiveWindow).Unix() {
		bucket := record.buckets[second]
		if bucket == nil {
			bucket = &issueBucket{pods: map[string]struct{}{}}
			record.buckets[second] = bucket
		}
		bucket.count++
		if event.Pod != "" {
			bucket.pods[event.Pod] = struct{}{}
		}
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
	if window > IssueActiveWindow {
		window = IssueActiveWindow
	}
	cutoff := now.Add(-window).Unix()
	nowSecond := now.Unix()
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]Issue, 0, len(r.groups))
	for _, record := range r.groups {
		count := 0
		activePods := map[string]struct{}{}
		for second, bucket := range record.buckets {
			if second < cutoff || second > nowSecond {
				continue
			}
			count += bucket.count
			for pod := range bucket.pods {
				activePods[pod] = struct{}{}
			}
		}
		if count == 0 {
			continue
		}
		issue := record.issue
		issue.Count = count
		issue.Pods = issuePods(activePods)
		issue.Increasing = issueIncreasing(record.buckets, now)
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

func (r *IssueRadar) pruneBucketsLocked(record *issueRecord) {
	cutoff := record.issue.LastSeen.Add(-IssueActiveWindow).Unix()
	for second := range record.buckets {
		if second < cutoff {
			delete(record.buckets, second)
		}
	}
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
	if strings.HasPrefix(message, "{") && json.Unmarshal([]byte(message), &object) == nil && object != nil {
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
		if match := bracketedLevel.FindStringSubmatch(message); len(match) > 1 {
			level = match[1]
		} else if match := issueLeadingLevel.FindStringSubmatch(message); len(match) > 1 {
			level = match[1]
		}
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

func issueIncreasing(buckets map[int64]*issueBucket, now time.Time) bool {
	recentStart := now.Add(-30 * time.Second).Unix()
	previousStart := now.Add(-60 * time.Second).Unix()
	nowSecond := now.Unix()
	recent, previous := 0, 0
	for second, bucket := range buckets {
		switch {
		case second >= recentStart && second <= nowSecond:
			recent += bucket.count
		case second >= previousStart && second < recentStart:
			previous += bucket.count
		}
	}
	return recent >= 3 && recent > previous*2
}
