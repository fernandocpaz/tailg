package core

import (
	"testing"
	"time"
)

func TestClassifyIssueSlowRequestsUseStrictThreshold(t *testing.T) {
	tests := []struct {
		name string
		msg  string
		want bool
	}{
		{name: "below threshold", msg: "HTTP GET /orders responded 200 in 200ms", want: false},
		{name: "at threshold", msg: "HTTP GET /orders responded 200 in 250ms", want: false},
		{name: "above threshold", msg: "HTTP GET /orders responded 200 in 250.1ms", want: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			issue, ok := ClassifyIssue(LogEvent{Container: "api", Message: test.msg})
			if ok != test.want {
				t.Fatalf("ClassifyIssue ok=%t, want %t; issue=%#v", ok, test.want, issue)
			}
			if test.want && (issue.Kind != "SLOW REQUEST" || issue.Severity != IssueWarning) {
				t.Fatalf("slow issue=%#v", issue)
			}
		})
	}
}

func TestSlowRequestsPreserveErrorSeverityAndGroupEndpoint(t *testing.T) {
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	traceOne := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	traceTwo := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	radar := NewIssueRadar(10)
	radar.SetBaseline(now.Add(-time.Minute))
	radar.Observe(LogEvent{Container: "api", Pod: "api-1", ObservedAt: now, Message: "[" + traceOne + "] HTTP GET /orders?request_id=one responded 200 in 251ms"})
	radar.Observe(LogEvent{Container: "api", Pod: "api-2", ObservedAt: now.Add(time.Second), Message: "[" + traceTwo + "] HTTP GET /orders?request_id=two responded 503 in 300ms"})

	issues := radar.Issues(now.Add(time.Second), IssueActiveWindow)
	if len(issues) != 1 {
		t.Fatalf("issues=%#v, want one grouped endpoint", issues)
	}
	issue := issues[0]
	if issue.Kind != "SLOW REQUEST" || issue.Severity != IssueError || issue.Count != 2 || issue.TotalCount != 2 {
		t.Fatalf("grouped issue=%#v", issue)
	}
	if issue.Endpoint != "/orders" || issue.MaxDuration != 300*time.Millisecond || issue.TraceID != traceTwo {
		t.Fatalf("slow metadata=%#v", issue)
	}
	if issue.Key == "" || issue.SearchTerm == "" {
		t.Fatalf("slow issue lacks stable correlation fields=%#v", issue)
	}
}

func TestSlowRequestPathIDsShareFingerprint(t *testing.T) {
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	radar := NewIssueRadar(10)
	radar.SetBaseline(now.Add(-time.Minute))
	radar.Observe(LogEvent{Container: "api", ObservedAt: now, Message: "HTTP GET /orders/123?request_id=one responded 200 in 300ms"})
	radar.Observe(LogEvent{Container: "api", ObservedAt: now.Add(time.Second), Message: "HTTP GET /orders/456?request_id=two responded 200 in 301ms"})
	issues := radar.Issues(now.Add(time.Second), IssueActiveWindow)
	if len(issues) != 1 || issues[0].Count != 2 || issues[0].Endpoint != "/orders/456" {
		t.Fatalf("path-ID grouping=%#v", issues)
	}
	if issues[0].SearchTerm != "duration:>250ms method:GET path:/orders/456" {
		t.Fatalf("context search disagrees with representative endpoint: %s", issues[0].SearchTerm)
	}
}

func TestStructuredRequestErrorsPreserveSeverity(t *testing.T) {
	for _, message := range []string{
		`{"message":"request completed","Properties":{"RequestMethod":"GET","RequestPath":"/orders","StatusCode":503,"Elapsed":300}}`,
		`{"message":"request completed","Properties":{"RequestMethod":"GET","RequestPath":"/orders","StatusCode":503,"Elapsed":20}}`,
		`{"message":"operation stopped","Properties":{"Level":"Error"}}`,
	} {
		issue, ok := ClassifyIssue(LogEvent{Message: message})
		if !ok || issue.Severity != IssueError {
			t.Fatalf("structured error not classified: %s => %+v", message, issue)
		}
	}
}

func TestIssueRadarBaselineUsesObservedTimeAndBoundsGroups(t *testing.T) {
	baseline := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	radar := NewIssueRadar(2)
	radar.SetBaseline(baseline)
	historical := LogEvent{Container: "api", ObservedAt: baseline.Add(-time.Hour), Message: "HTTP GET /historical responded 200 in 251ms"}
	if !radar.Observe(historical) {
		t.Fatal("historical slow request was not observed")
	}
	newEvent := LogEvent{Container: "api", ObservedAt: baseline.Add(time.Second), Message: "HTTP GET /new responded 200 in 251ms"}
	if !radar.Observe(newEvent) {
		t.Fatal("new slow request was not observed")
	}
	if radar.Observe(LogEvent{Replayed: true, Container: "api", ObservedAt: baseline.Add(2 * time.Second), Message: "HTTP GET /replay responded 200 in 999ms"}) {
		t.Fatal("replayed control event should be ignored")
	}
	if radar.Observe(LogEvent{Started: true, Container: "api", ObservedAt: baseline.Add(2 * time.Second), Message: "HTTP GET /started responded 200 in 999ms"}) {
		t.Fatal("started control event should be ignored")
	}

	issues := radar.Issues(baseline.Add(2*time.Second), IssueActiveWindow)
	if len(issues) != 1 {
		t.Fatalf("issues=%#v, want historical and new groups active", issues)
	}
	// The historical event is outside the active window, so inspect the new
	// group and verify the summary count only includes active observations.
	if !issues[0].New || issues[0].Endpoint != "/new" {
		t.Fatalf("new issue=%#v", issues[0])
	}
	if stats := radar.Stats(baseline.Add(2*time.Second), IssueActiveWindow); stats.New != 1 {
		t.Fatalf("stats=%#v, want one new group", stats)
	}

	radar.SetBaseline(baseline.Add(2 * time.Second))
	issues = radar.Issues(baseline.Add(2*time.Second), IssueActiveWindow)
	if len(issues) != 1 || issues[0].New {
		t.Fatalf("after baseline reset=%#v, want recurring", issues)
	}

	// A bounded radar evicts the oldest group rather than retaining an
	// unbounded seen-key set.
	radar.Observe(LogEvent{Container: "api", ObservedAt: baseline.Add(3 * time.Second), Message: "HTTP GET /third responded 200 in 251ms"})
	radar.Observe(LogEvent{Container: "api", ObservedAt: baseline.Add(4 * time.Second), Message: "HTTP GET /fourth responded 251 in 251ms"})
	if got := len(radar.groups); got > 2 {
		t.Fatalf("groups=%d, want bounded at 2", got)
	}
}
