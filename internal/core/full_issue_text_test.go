package core

import (
	"strings"
	"testing"
	"time"
)

func TestClassifyIssuePreservesFullSummaryAndStableCompactKey(t *testing.T) {
	message := "request timeout: " + strings.Repeat("detail ", 30) + "SUFFIX"
	issue, ok := ClassifyIssue(LogEvent{Container: "api", Message: message})
	if !ok {
		t.Fatal("long timeout issue was not classified")
	}
	clean := cleanIssueSummary(message)
	if issue.FullSummary != clean {
		t.Fatalf("FullSummary = %q, want complete cleaned text %q", issue.FullSummary, clean)
	}
	if !strings.HasSuffix(issue.FullSummary, "SUFFIX") {
		t.Fatalf("FullSummary lost suffix: %q", issue.FullSummary)
	}
	if issue.Summary != truncateIssueSummary(clean) || len([]rune(issue.Summary)) != 110 {
		t.Fatalf("compact Summary = %q, want unchanged 110-rune compact form", issue.Summary)
	}
	// Group keys continue to use the compact summary so adding full text does
	// not change correlation for existing reports.
	wantKey := "api\x00TIMEOUT\x00" + issueFingerprint(truncateIssueSummary(clean))
	if issue.Key != wantKey {
		t.Fatalf("Key = %q, want stable key %q", issue.Key, wantKey)
	}
}

func TestIssueRadarCarriesFullSummaryAcrossRepresentativeSlowRequest(t *testing.T) {
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	radar := NewIssueRadar(10)
	radar.Observe(LogEvent{Container: "api", ObservedAt: now, Message: "HTTP GET /orders/123 responded 200 in 300ms"})
	radar.Observe(LogEvent{Container: "api", ObservedAt: now.Add(time.Second), Message: "HTTP GET /orders/456 responded 200 in 301ms"})
	issues := radar.Issues(now.Add(time.Second), IssueActiveWindow)
	if len(issues) != 1 || issues[0].FullSummary != "slow GET /orders/456 (>250ms)" {
		t.Fatalf("representative slow issue = %#v", issues)
	}
}
