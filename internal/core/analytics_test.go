package core

import (
	"fmt"
	"testing"
	"time"
)

func TestRollingAnalyticsCountsAndLatencyByEndpoint(t *testing.T) {
	now := time.Date(2026, 11, 23, 16, 0, 30, 0, time.UTC)
	a := &RollingAnalytics{}
	for _, item := range []struct {
		message string
		age     time.Duration
	}{
		{"HTTP GET /orders/123?access_token=secret responded 200 in 10 ms", 0},
		{"HTTP GET /orders/456 responded 503 in 700 ms", time.Minute},
		{"HTTP GET /orders/789 responded 404 in 20 ms", 2 * time.Minute},
		{"HTTP POST /orders responded 201 in 120 ms", 6 * time.Minute},
	} {
		if !a.ObserveAt(LogEvent{Pod: "api-a", Container: "api", Message: item.message, ObservedAt: now.Add(-item.age)}, now) {
			t.Fatalf("HTTP completion ignored: %s", item.message)
		}
	}
	snap := a.Snapshot(now)
	if got := snap.Total; got.Requests != 4 || got.ClientErrors != 1 || got.ServerErrors != 1 || got.Slow != 1 || got.Timed != 4 {
		t.Fatalf("total counts: %+v", got)
	}
	if snap.LastFive.Requests != 3 || snap.PreviousFive.Requests != 1 {
		t.Fatalf("five-minute windows: recent=%d previous=%d", snap.LastFive.Requests, snap.PreviousFive.Requests)
	}
	if snap.Total.ApproxPercentile(.95) != time.Second {
		t.Fatalf("approximate p95 = %v, want 1s histogram upper bound", snap.Total.ApproxPercentile(.95))
	}
	matched := false
	for _, endpoint := range snap.Endpoints {
		if endpoint.Method == "GET" && endpoint.Path == "/orders/{id}" {
			matched = true
			if endpoint.Counts.Requests != 3 || endpoint.Counts.ServerErrors != 1 {
				t.Fatalf("GET endpoint counts: %+v", endpoint.Counts)
			}
		}
		if endpoint.Path != "(other endpoints)" && (len(endpoint.Path) > 0 && endpoint.Path[0] == '?') {
			t.Fatalf("unredacted endpoint: %q", endpoint.Path)
		}
	}
	if !matched {
		t.Fatalf("GET endpoint not grouped: %+v", snap.Endpoints)
	}
}

func TestRollingAnalyticsSkipsReplayNonCompletionAndOldLogs(t *testing.T) {
	now := time.Date(2026, 11, 23, 16, 0, 0, 0, time.UTC)
	a := &RollingAnalytics{}
	base := LogEvent{Message: "HTTP GET /items responded 200 in 12 ms", ObservedAt: now}
	if !a.ObserveAt(base, now) {
		t.Fatal("new completion ignored")
	}
	replay := base
	replay.Replayed = true
	if a.ObserveAt(replay, now) {
		t.Fatal("replayed completion counted twice")
	}
	for _, event := range []LogEvent{
		{Message: "HTTP GET /items responded 200 in 12 ms"},
		{Message: "HTTP GET /items responded 200 in 12 ms", ObservedAt: now.Add(-16 * time.Minute)},
		{Message: "HTTP GET /items responded 200 in 12 ms", ObservedAt: now.Add(time.Minute)},
		{Message: "HTTP GET /items", ObservedAt: now},
		{Message: "Heartbeat: healthy", ObservedAt: now},
		{Message: base.Message, ObservedAt: now, Closed: true},
	} {
		if a.ObserveAt(event, now) {
			t.Fatalf("invalid event counted: %+v", event)
		}
	}
	if got := a.Snapshot(now).Total.Requests; got != 1 {
		t.Fatalf("HTTP completions = %d, want 1", got)
	}
	if got := a.Snapshot(now.Add(16 * time.Minute)).Total.Requests; got != 0 {
		t.Fatalf("expired completions = %d, want zero", got)
	}
}

func TestRollingAnalyticsCardinalityBoundAndAccurateTotals(t *testing.T) {
	now := time.Date(2026, 11, 23, 16, 0, 0, 0, time.UTC)
	a := &RollingAnalytics{}
	for i := 0; i < 500; i++ {
		message := fmt.Sprintf("HTTP GET /route-%d responded 500 in 350 ms", i)
		if !a.ObserveAt(LogEvent{Message: message, ObservedAt: now}, now) {
			t.Fatal("expected completion")
		}
	}
	snap := a.Snapshot(now)
	if snap.Total.Requests != 500 || snap.Total.ServerErrors != 500 || snap.Total.Slow != 500 {
		t.Fatalf("cardinality changed totals: %+v", snap.Total)
	}
	if len(snap.Endpoints) > analyticsEndpointLimit+1 {
		t.Fatalf("too many endpoint groups: %d", len(snap.Endpoints))
	}
	var sum int
	var hasOther bool
	for _, endpoint := range snap.Endpoints {
		sum += endpoint.Counts.Requests
		hasOther = hasOther || endpoint.Path == analyticsOtherEndpoint
	}
	if sum != 500 || !hasOther {
		t.Fatalf("endpoint totals = %d, other group = %t", sum, hasOther)
	}
}

func TestRollingAnalyticsIndependentMinuteAndTimeBounds(t *testing.T) {
	now := time.Date(2026, 11, 23, 16, 20, 10, 0, time.UTC)
	a := &RollingAnalytics{}
	for _, age := range []time.Duration{0, 4 * time.Minute, 9 * time.Minute, 14 * time.Minute} {
		if !a.ObserveAt(LogEvent{ObservedAt: now.Add(-age), Message: "HTTP GET /ready responded 200 in 1 ms"}, now) {
			t.Fatalf("missing age %s", age)
		}
	}
	got := a.Snapshot(now)
	if got.Total.Requests != 4 || got.LastFive.Requests != 2 || got.PreviousFive.Requests != 1 || len(got.Minutes) != analyticsMinutes {
		t.Fatalf("incorrect buckets: %+v", got)
	}
	if got := a.Snapshot(now.Add(15 * time.Minute)).Total.Requests; got != 0 {
		t.Fatalf("stale buckets persisted: %d", got)
	}
}
