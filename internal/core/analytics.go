package core

import (
	"math"
	"sort"
	"strings"
	"time"
)

const (
	// AnalyticsWindow is a bounded set of fifteen wall-clock minute buckets.
	// Partial first/last minutes mean that these figures are log observations,
	// not authoritative service-level request rates.
	AnalyticsWindow = 15 * time.Minute
	analyticsMinutes = 15
	analyticsEndpointLimit = 64
	analyticsOtherEndpoint = "(other endpoints)"
)

// Histogram upper bounds are deliberately coarse and bounded. Reported
// percentiles are approximations, not exact request latency quantiles.
var analyticsLatencyBounds = [...]time.Duration{
	time.Millisecond, 2 * time.Millisecond, 4 * time.Millisecond,
	8 * time.Millisecond, 16 * time.Millisecond, 32 * time.Millisecond,
	64 * time.Millisecond, 125 * time.Millisecond, 250 * time.Millisecond,
	500 * time.Millisecond, time.Second, 2 * time.Second, 4 * time.Second,
	8 * time.Second, 16 * time.Second, 30 * time.Second, time.Minute,
	2 * time.Minute, 5 * time.Minute, time.Duration(math.MaxInt64),
}

// AnalyticsCounts describes only observed HTTP completion log events.
// A log line needs method, path, and either HTTP status or duration.
// It is not evidence that all incoming requests were logged.
type AnalyticsCounts struct {
	Requests     int
	StatusKnown  int
	ClientErrors int
	ServerErrors int
	Slow         int
	Timed        int
	latencies    [20]uint64
}

func (c *AnalyticsCounts) add(other AnalyticsCounts) {
	c.Requests += other.Requests
	c.StatusKnown += other.StatusKnown
	c.ClientErrors += other.ClientErrors
	c.ServerErrors += other.ServerErrors
	c.Slow += other.Slow
	c.Timed += other.Timed
	for i, count := range other.latencies {
		c.latencies[i] += count
	}
}

func (c *AnalyticsCounts) addRequest(fields LogFields) {
	c.Requests++
	if fields.StatusCode >= 100 && fields.StatusCode <= 599 {
		c.StatusKnown++
	}
	switch {
	case fields.StatusCode >= 500 && fields.StatusCode <= 599:
		c.ServerErrors++
	case fields.StatusCode >= 400 && fields.StatusCode <= 499:
		c.ClientErrors++
	}
	if fields.HasDuration && fields.Duration >= 0 {
		c.Timed++
		if fields.Duration > DefaultSlowRequestThreshold {
			c.Slow++
		}
		for i, bound := range analyticsLatencyBounds {
			if fields.Duration <= bound {
				c.latencies[i]++
				break
			}
		}
	}
}

// ApproxPercentile returns an inclusive histogram upper bound for p (0..1).
// Zero means no latency samples. The value must be labeled approximate in UI.
func (c AnalyticsCounts) ApproxPercentile(p float64) time.Duration {
	if c.Timed == 0 {
		return 0
	}
	p = math.Max(0, math.Min(1, p))
	target := uint64(math.Max(1, math.Ceil(float64(c.Timed)*p)))
	var seen uint64
	for i, count := range c.latencies {
		seen += count
		if seen >= target {
			return analyticsLatencyBounds[i]
		}
	}
	return analyticsLatencyBounds[len(analyticsLatencyBounds)-1]
}

type AnalyticsMinute struct {
	Minute time.Time
	Counts AnalyticsCounts
}

type AnalyticsEndpoint struct {
	Method string
	Path   string
	Counts AnalyticsCounts
}

type AnalyticsSnapshot struct {
	Window       time.Duration
	Total        AnalyticsCounts
	LastFive     AnalyticsCounts
	PreviousFive AnalyticsCounts
	Minutes      []AnalyticsMinute
	Endpoints    []AnalyticsEndpoint
}

type analyticsBucket struct {
	minute    int64
	counts    AnalyticsCounts
	endpoints map[string]*AnalyticsCounts
}

// RollingAnalytics uses a fixed minute-ring and at most 65 endpoint groups
// per minute, regardless of request rate or URL cardinality.
type RollingAnalytics struct {
	buckets [analyticsMinutes]analyticsBucket
}

func NewRollingAnalytics() *RollingAnalytics { return &RollingAnalytics{} }

// Observe accepts one actual streamed event. Replayed lines, stream status,
// old history, and records lacking HTTP completion metadata are ignored.
func (a *RollingAnalytics) Observe(event LogEvent) bool {
	return a.ObserveAt(event, time.Now())
}

// ObserveAt permits deterministic clock-aware operation and testing.
func (a *RollingAnalytics) ObserveAt(event LogEvent, now time.Time) bool {
	if a == nil || event.Started || event.Replayed || event.Closed || event.Err != nil || event.ObservedAt.IsZero() {
		return false
	}
	minute := event.ObservedAt.UTC().Truncate(time.Minute)
	current := now.UTC().Truncate(time.Minute)
	if minute.Before(current.Add(-(analyticsMinutes-1)*time.Minute)) || minute.After(current) {
		return false
	}
	fields := ParseLogFields(event)
	method := normalizeHTTPMethod(fields.Method)
	path := endpointFingerprint(normalizeEndpoint(fields.Path))
	if method == "" || path == "" || (fields.StatusCode == 0 && !fields.HasDuration) {
		return false
	}
	index := int((minute.Unix() / 60) % analyticsMinutes)
	if index < 0 {
		index += analyticsMinutes
	}
	bucket := &a.buckets[index]
	if bucket.endpoints == nil || bucket.minute != minute.Unix() {
		*bucket = analyticsBucket{minute: minute.Unix(), endpoints: make(map[string]*AnalyticsCounts)}
	}
	bucket.counts.addRequest(fields)

	// Do not allow unbounded high-cardinality paths to exhaust TUI memory.
	runes := []rune(path)
	if len(runes) > 120 {
		path = string(runes[:120]) + "…"
	}
	key := method + " " + path
	group := bucket.endpoints[key]
	if group == nil {
		if len(bucket.endpoints) >= analyticsEndpointLimit {
			key = analyticsOtherEndpoint
		}
		group = bucket.endpoints[key]
		if group == nil {
			group = &AnalyticsCounts{}
			bucket.endpoints[key] = group
		}
	}
	group.addRequest(fields)
	return true
}

func (a *RollingAnalytics) Snapshot(now time.Time) AnalyticsSnapshot {
	snapshot := AnalyticsSnapshot{Window: AnalyticsWindow, Minutes: make([]AnalyticsMinute, 0, analyticsMinutes)}
	if a == nil {
		return snapshot
	}
	current := now.UTC().Truncate(time.Minute)
	endpoints := make(map[string]*AnalyticsCounts)
	for offset := analyticsMinutes - 1; offset >= 0; offset-- {
		minute := current.Add(-time.Duration(offset) * time.Minute)
		index := int((minute.Unix() / 60) % analyticsMinutes)
		if index < 0 {
			index += analyticsMinutes
		}
		var counts AnalyticsCounts
		bucket := &a.buckets[index]
		if bucket.endpoints != nil && bucket.minute == minute.Unix() {
			counts = bucket.counts
			for key, partial := range bucket.endpoints {
				counter := endpoints[key]
				if counter == nil {
					counter = &AnalyticsCounts{}
					endpoints[key] = counter
				}
				counter.add(*partial)
			}
		}
		snapshot.Minutes = append(snapshot.Minutes, AnalyticsMinute{Minute: minute, Counts: counts})
		snapshot.Total.add(counts)
		switch {
		case offset < 5:
			snapshot.LastFive.add(counts)
		case offset < 10:
			snapshot.PreviousFive.add(counts)
		}
	}
	for key, counts := range endpoints {
		if key == analyticsOtherEndpoint {
			snapshot.Endpoints = append(snapshot.Endpoints, AnalyticsEndpoint{Path: key, Counts: *counts})
			continue
		}
		method, path, ok := strings.Cut(key, " ")
		if ok {
			snapshot.Endpoints = append(snapshot.Endpoints, AnalyticsEndpoint{Method: method, Path: path, Counts: *counts})
		}
	}
	sort.Slice(snapshot.Endpoints, func(i, j int) bool {
		a, b := snapshot.Endpoints[i], snapshot.Endpoints[j]
		if a.Counts.Requests != b.Counts.Requests {
			return a.Counts.Requests > b.Counts.Requests
		}
		if a.Counts.ServerErrors != b.Counts.ServerErrors {
			return a.Counts.ServerErrors > b.Counts.ServerErrors
		}
		return a.Method+" "+a.Path < b.Method+" "+b.Path
	})
	return snapshot
}
