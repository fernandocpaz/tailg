package core

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	heartbeatGapWarning = 150 * time.Second
	heartbeatGapAlert   = 300 * time.Second
	batchAgeWarning     = 300 * time.Second
	batchAgeAlert       = 900 * time.Second
)

var heartbeatMarker = regexp.MustCompile(`(?i)\bHeartbeat\s*:`)
var heartbeatField = regexp.MustCompile(`\b([A-Za-z][A-Za-z0-9]*)=([^\s]+)`)
var heartbeatClock = regexp.MustCompile(`\[(\d{2}):(\d{2}):(\d{2})(?:\.\d+)?(?:\s+[A-Za-z]+)?\]`)

func IsHeartbeat(message string) bool { return heartbeatMarker.MatchString(message) }

func ParseHeartbeat(pod, container, message string, observedAt time.Time) (HeartbeatSample, bool) {
	marker := heartbeatMarker.FindStringIndex(message)
	if marker == nil {
		return HeartbeatSample{}, false
	}
	if observedAt.IsZero() {
		observedAt = time.Now()
	}
	timestamp := observedAt
	if clock := heartbeatClock.FindStringSubmatch(message); clock != nil {
		hour, _ := strconv.Atoi(clock[1])
		minute, _ := strconv.Atoi(clock[2])
		second, _ := strconv.Atoi(clock[3])
		timestamp = time.Date(observedAt.Year(), observedAt.Month(), observedAt.Day(), hour, minute, second, 0, observedAt.Location())
		if timestamp.Sub(observedAt) > 5*time.Minute {
			timestamp = timestamp.Add(-24 * time.Hour)
		}
	}
	fields := map[string]string{}
	for _, match := range heartbeatField.FindAllStringSubmatch(message[marker[1]:], -1) {
		fields[strings.ToLower(match[1])] = strings.TrimRight(match[2], ",;")
	}
	integer := func(name string) int {
		value, _ := strconv.Atoi(fields[name])
		return value
	}
	breaker := fields["breaker"]
	if breaker == "" && strings.Contains(strings.ToLower(message[marker[1]:]), "breaker") {
		breaker = "unknown"
	}
	uptime, hasUptime := ParseLogDuration(fields["uptime"])
	batchAge, hasBatchAge := ParseLogDuration(fields["batchage"])
	return HeartbeatSample{
		Timestamp: timestamp, Pod: pod, Container: container,
		Uptime: uptime, HasUptime: hasUptime, Inflight: integer("inflight"),
		OK: integer("ok"), Skipped: integer("skipped"), Failed: integer("failed"), DLQ: integer("dlq"),
		LastConsume: valueOr(fields["lastconsume"], "unknown"), LastCommit: valueOr(fields["lastcommit"], "unknown"),
		BatchAge: batchAge, HasBatchAge: hasBatchAge, BatchSize: integer("batchsize"),
		Breaker: strings.ToLower(valueOr(breaker, "unknown")),
	}, true
}

type heartbeatBucket struct {
	samples                            []HeartbeatSample
	okDelta, skippedDelta, failedDelta int
	dlqDelta                           int
	maxBatchAge                        time.Duration
	hasBatchAge, firstObservation      bool
	severity                           string
	reasons                            []string
}

func AnalyzeHeartbeatSamples(samples []HeartbeatSample, window time.Duration, now time.Time) []HeartbeatInterval {
	if window <= 0 || len(samples) == 0 {
		return nil
	}
	ordered := append([]HeartbeatSample(nil), samples...)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].Timestamp.Equal(ordered[j].Timestamp) {
			return ordered[i].Pod+ordered[i].Container < ordered[j].Pod+ordered[j].Container
		}
		return ordered[i].Timestamp.Before(ordered[j].Timestamp)
	})
	buckets := map[string]*heartbeatBucket{}
	starts := map[string]time.Time{}
	previous := map[string]HeartbeatSample{}
	latest := map[string]HeartbeatSample{}
	inflightSince := map[string]time.Time{}

	for _, sample := range ordered {
		source := sample.Pod + "\x00" + sample.Container
		day := time.Date(sample.Timestamp.Year(), sample.Timestamp.Month(), sample.Timestamp.Day(), 0, 0, 0, 0, sample.Timestamp.Location())
		start := day.Add(sample.Timestamp.Sub(day) / window * window)
		key := source + "\x00" + start.Format(time.RFC3339Nano)
		bucket := buckets[key]
		if bucket == nil {
			bucket = &heartbeatBucket{severity: "OK"}
			buckets[key] = bucket
			starts[key] = start
		}
		bucket.samples = append(bucket.samples, sample)
		if sample.HasBatchAge && (!bucket.hasBatchAge || sample.BatchAge > bucket.maxBatchAge) {
			bucket.maxBatchAge, bucket.hasBatchAge = sample.BatchAge, true
		}

		prior, hasPrior := previous[source]
		restarted := false
		if !hasPrior {
			bucket.firstObservation = true
		} else {
			gap := sample.Timestamp.Sub(prior.Timestamp)
			if gap >= heartbeatGapAlert {
				addHeartbeatReason(bucket, "ALERT", "heartbeat gap "+FormatDuration(gap, true))
			} else if gap >= heartbeatGapWarning {
				addHeartbeatReason(bucket, "WARN", "heartbeat gap "+FormatDuration(gap, true))
			}
			if sample.HasUptime && prior.HasUptime && sample.Uptime < prior.Uptime {
				restarted = true
				addHeartbeatReason(bucket, "WARN", "uptime reset; process restarted")
			} else if sample.OK < prior.OK || sample.Skipped < prior.Skipped || sample.Failed < prior.Failed || sample.DLQ < prior.DLQ {
				restarted = true
				addHeartbeatReason(bucket, "WARN", "heartbeat totals reset; process likely restarted")
			}
		}
		if hasPrior && !restarted {
			bucket.okDelta += max(0, sample.OK-prior.OK)
			bucket.skippedDelta += max(0, sample.Skipped-prior.Skipped)
			bucket.failedDelta += max(0, sample.Failed-prior.Failed)
			bucket.dlqDelta += max(0, sample.DLQ-prior.DLQ)
		} else if hasPrior && restarted {
			bucket.okDelta += sample.OK
			bucket.skippedDelta += sample.Skipped
			bucket.failedDelta += sample.Failed
			bucket.dlqDelta += sample.DLQ
		}
		if sample.Inflight > 0 {
			started, ok := inflightSince[source]
			if !ok {
				started = sample.Timestamp
				inflightSince[source] = started
			}
			age := sample.Timestamp.Sub(started)
			if age >= heartbeatGapAlert {
				addHeartbeatReason(bucket, "ALERT", "inflight work persisted for "+FormatDuration(age, true))
			} else if age >= 2*time.Minute {
				addHeartbeatReason(bucket, "WARN", "inflight work persisted for "+FormatDuration(age, true))
			}
		} else {
			delete(inflightSince, source)
		}
		previous[source], latest[source] = sample, sample
	}

	if now.IsZero() {
		now = time.Now()
	}
	intervals := make([]HeartbeatInterval, 0, len(buckets))
	for key, bucket := range buckets {
		last := bucket.samples[len(bucket.samples)-1]
		source := last.Pod + "\x00" + last.Container
		if bucket.dlqDelta > 0 {
			addHeartbeatReason(bucket, "ALERT", fmt.Sprintf("DLQ increased by %d", bucket.dlqDelta))
		}
		if bucket.failedDelta > 0 {
			addHeartbeatReason(bucket, "ALERT", fmt.Sprintf("failed increased by %d", bucket.failedDelta))
		} else if bucket.firstObservation && last.Failed > 0 {
			addHeartbeatReason(bucket, "ALERT", fmt.Sprintf("failed total is %d at first observed heartbeat", last.Failed))
		}
		if bucket.firstObservation && last.DLQ > 0 && bucket.dlqDelta == 0 {
			addHeartbeatReason(bucket, "ALERT", fmt.Sprintf("DLQ total is %d at first observed heartbeat", last.DLQ))
		}
		if bucket.skippedDelta > 0 {
			addHeartbeatReason(bucket, "WARN", fmt.Sprintf("skipped increased by %d", bucket.skippedDelta))
		}
		for _, sample := range bucket.samples {
			switch sample.Breaker {
			case "open", "tripped", "broken":
				addHeartbeatReason(bucket, "ALERT", "circuit breaker is "+last.Breaker)
			case "half-open", "halfopen":
				addHeartbeatReason(bucket, "WARN", "circuit breaker is half-open")
			}
		}
		if bucket.hasBatchAge && bucket.maxBatchAge >= batchAgeAlert {
			addHeartbeatReason(bucket, "ALERT", "batch age reached "+FormatDuration(bucket.maxBatchAge, true))
		} else if bucket.hasBatchAge && bucket.maxBatchAge >= batchAgeWarning {
			addHeartbeatReason(bucket, "WARN", "batch age reached "+FormatDuration(bucket.maxBatchAge, true))
		} else if last.BatchSize > 0 && !last.HasBatchAge {
			addHeartbeatReason(bucket, "WARN", "batch is pending but batchAge is unavailable")
		}
		if latest[source].Timestamp.Equal(last.Timestamp) {
			stale := now.Sub(last.Timestamp)
			if stale >= heartbeatGapAlert {
				addHeartbeatReason(bucket, "ALERT", "latest heartbeat is "+FormatDuration(stale, true)+" old")
			} else if stale >= heartbeatGapWarning {
				addHeartbeatReason(bucket, "WARN", "latest heartbeat is "+FormatDuration(stale, true)+" old")
			}
		}
		intervals = append(intervals, HeartbeatInterval{
			Start: starts[key], End: starts[key].Add(window), Pod: last.Pod, Container: last.Container,
			SampleCount: len(bucket.samples), Uptime: last.Uptime, HasUptime: last.HasUptime, Inflight: last.Inflight,
			OKTotal: last.OK, SkippedTotal: last.Skipped, FailedTotal: last.Failed, DLQTotal: last.DLQ,
			OKDelta: bucket.okDelta, SkippedDelta: bucket.skippedDelta, FailedDelta: bucket.failedDelta, DLQDelta: bucket.dlqDelta,
			LastConsume: last.LastConsume, LastCommit: last.LastCommit, BatchAge: bucket.maxBatchAge,
			HasBatchAge: bucket.hasBatchAge, BatchSize: last.BatchSize, Breaker: last.Breaker,
			Severity: bucket.severity, Reasons: append([]string(nil), bucket.reasons...),
		})
	}
	sort.Slice(intervals, func(i, j int) bool {
		if intervals[i].Start.Equal(intervals[j].Start) {
			return intervals[i].Pod+intervals[i].Container > intervals[j].Pod+intervals[j].Container
		}
		return intervals[i].Start.After(intervals[j].Start)
	})
	return intervals
}

func LatestHeartbeatSeverity(intervals []HeartbeatInterval) string {
	latest := map[string]HeartbeatInterval{}
	for _, interval := range intervals {
		key := interval.Pod + "\x00" + interval.Container
		if current, ok := latest[key]; !ok || interval.Start.After(current.Start) {
			latest[key] = interval
		}
	}
	if len(latest) == 0 {
		return "UNKNOWN"
	}
	worst := "OK"
	for _, interval := range latest {
		if severityRank(interval.Severity) > severityRank(worst) {
			worst = interval.Severity
		}
	}
	return worst
}

type HeartbeatAnalyzer struct {
	mu      sync.RWMutex
	samples []HeartbeatSample
}

func (a *HeartbeatAnalyzer) Add(pod, container, message string, observedAt time.Time) bool {
	sample, ok := ParseHeartbeat(pod, container, message, observedAt)
	if !ok {
		return false
	}
	a.mu.Lock()
	a.samples = append(a.samples, sample)
	a.mu.Unlock()
	return true
}

func (a *HeartbeatAnalyzer) Intervals(window time.Duration, now time.Time) []HeartbeatInterval {
	a.mu.RLock()
	snapshot := append([]HeartbeatSample(nil), a.samples...)
	a.mu.RUnlock()
	return AnalyzeHeartbeatSamples(snapshot, window, now)
}

func (a *HeartbeatAnalyzer) Severity(window time.Duration) string {
	return LatestHeartbeatSeverity(a.Intervals(window, time.Now()))
}

func HeartbeatReport(intervals []HeartbeatInterval) string {
	if len(intervals) == 0 {
		return "No Heartbeat: messages were found in the buffered pod logs."
	}
	var lines []string
	for _, interval := range intervals {
		lines = append(lines,
			fmt.Sprintf("%-5s %s-%s %s/%s | beats=%d up=%s inflight=%d", interval.Severity, interval.Start.Format("15:04"), interval.End.Format("15:04"), interval.Pod, interval.Container, interval.SampleCount, FormatDuration(interval.Uptime, interval.HasUptime), interval.Inflight),
			fmt.Sprintf("      totals ok=%d skip=%d fail=%d dlq=%d | delta +%d/+%d/+%d/+%d", interval.OKTotal, interval.SkippedTotal, interval.FailedTotal, interval.DLQTotal, interval.OKDelta, interval.SkippedDelta, interval.FailedDelta, interval.DLQDelta),
			fmt.Sprintf("      consume=%s commit=%s batch=%d/%s breaker=%s", interval.LastConsume, interval.LastCommit, interval.BatchSize, FormatDuration(interval.BatchAge, interval.HasBatchAge), interval.Breaker),
		)
		if len(interval.Reasons) > 0 {
			lines = append(lines, "      reason: "+strings.Join(interval.Reasons, "; "))
		}
	}
	return strings.Join(lines, "\n")
}

func addHeartbeatReason(bucket *heartbeatBucket, severity, reason string) {
	if severityRank(severity) > severityRank(bucket.severity) {
		bucket.severity = severity
	}
	for _, existing := range bucket.reasons {
		if existing == reason {
			return
		}
	}
	bucket.reasons = append(bucket.reasons, reason)
}

func severityRank(value string) int {
	switch value {
	case "ALERT":
		return 2
	case "WARN":
		return 1
	default:
		return 0
	}
}

func valueOr(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
