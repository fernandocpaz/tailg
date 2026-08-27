package core

import (
	"errors"
	"testing"
	"time"
)

func TestIssueRadarGroupsDynamicValues(t *testing.T) {
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	radar := NewIssueRadar(20)
	for index, message := range []string{
		"[12:00:01 ERR] request 45 timed out after 3000 ms",
		"[12:00:02 ERR] request 91 timed out after 5000 ms",
		"[12:00:03 ERR] request 104 timed out after 8000 ms",
	} {
		radar.Observe(LogEvent{Pod: "api-a", Container: "checkout", Message: message, ObservedAt: now.Add(time.Duration(index) * time.Second)})
	}

	issues := radar.Issues(now.Add(3*time.Second), IssueActiveWindow)
	if len(issues) != 1 {
		t.Fatalf("issues = %d, want one grouped issue: %#v", len(issues), issues)
	}
	if issues[0].Kind != "TIMEOUT" || issues[0].Severity != IssueError || issues[0].Count != 3 || !issues[0].Increasing {
		t.Fatalf("grouped issue = %#v", issues[0])
	}
}

func TestIssueRadarDetectsOperationalSignals(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name    string
		event   LogEvent
		kind    string
		severity IssueSeverity
	}{
		{name: "json error", event: LogEvent{Message: `{"level":"Error","message":"database unavailable"}`}, kind: "FAILURE", severity: IssueError},
		{name: "http", event: LogEvent{Message: "HTTP POST /orders responded 503 in 12 ms"}, kind: "HTTP 5XX", severity: IssueError},
		{name: "retry", event: LogEvent{Message: "retrying request after backoff"}, kind: "RETRY", severity: IssueWarning},
		{name: "stream", event: LogEvent{Closed: true, Err: errors.New("kubectl connection reset")}, kind: "STREAM", severity: IssueError},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			test.event.ObservedAt = now
			detected, ok := detectIssue(test.event)
			if !ok || detected.kind != test.kind || detected.severity != test.severity {
				t.Fatalf("detected = %#v, ok=%t", detected, ok)
			}
		})
	}
}

func TestIssueRadarIgnoresHealthyCountersAndHeartbeat(t *testing.T) {
	for _, message := range []string{
		"Heartbeat: uptime=00:02:00 failed=0 dlq=0 breaker=closed",
		"request completed with errors=0 failures=0",
		"no errors detected",
	} {
		if detected, ok := detectIssue(LogEvent{Message: message}); ok {
			t.Fatalf("healthy message %q detected as %#v", message, detected)
		}
	}
}

func TestIssueRadarClearEstablishesNewBaseline(t *testing.T) {
	now := time.Now()
	radar := NewIssueRadar(10)
	radar.Observe(LogEvent{Container: "api", Message: "operation failed", ObservedAt: now})
	if stats := radar.Stats(now, IssueActiveWindow); stats.Groups != 1 || stats.Events != 1 {
		t.Fatalf("stats before clear = %#v", stats)
	}
	radar.Clear()
	if stats := radar.Stats(now, IssueActiveWindow); stats.Groups != 0 || stats.Events != 0 {
		t.Fatalf("stats after clear = %#v", stats)
	}
}

func TestIssueRadarKeepsTimeBoundsWhenStreamsInterleave(t *testing.T) {
	now := time.Now()
	radar := NewIssueRadar(10)
	radar.Observe(LogEvent{Container: "api", Message: "request timeout after 5 seconds", ObservedAt: now})
	radar.Observe(LogEvent{Container: "api", Message: "request timeout after 3 seconds", ObservedAt: now.Add(-time.Minute)})
	issues := radar.Issues(now, IssueActiveWindow)
	if len(issues) != 1 || !issues[0].FirstSeen.Equal(now.Add(-time.Minute)) || !issues[0].LastSeen.Equal(now) {
		t.Fatalf("interleaved issue bounds = %#v", issues)
	}
}
