package core

import (
	"strings"
	"testing"
	"time"
)

func TestParseHeartbeatWithBareBreaker(t *testing.T) {
	observed := time.Date(2026, 8, 14, 16, 41, 30, 0, time.Local)
	sample, ok := ParseHeartbeat("pod", "worker", "[worker] [16:41:16 INF] Heartbeat: uptime=00:02:00 inflight=0 totals: ok=10 skipped=1 failed=0 dlq=0 lastConsume=never lastCommit=never batchAge=never batchSize=0 breaker", observed)
	if !ok {
		t.Fatal("heartbeat not parsed")
	}
	if sample.OK != 10 || sample.Skipped != 1 || sample.Breaker != "unknown" || sample.Timestamp.Hour() != 16 || sample.Timestamp.Minute() != 41 {
		t.Fatalf("sample=%+v", sample)
	}
}

func TestHeartbeatAnalysisAlertsOnFailureAndOpenBreaker(t *testing.T) {
	start := time.Date(2026, 8, 14, 17, 0, 0, 0, time.Local)
	samples := []HeartbeatSample{{Timestamp: start, Pod: "pod", Container: "worker", OK: 10, Failed: 0, DLQ: 0, Breaker: "closed"}, {Timestamp: start.Add(time.Minute), Pod: "pod", Container: "worker", OK: 12, Failed: 1, DLQ: 1, Breaker: "open"}}
	intervals := AnalyzeHeartbeatSamples(samples, 15*time.Minute, start.Add(2*time.Minute))
	if len(intervals) != 1 || intervals[0].Severity != "ALERT" || intervals[0].FailedDelta != 1 || intervals[0].DLQDelta != 1 {
		t.Fatalf("intervals=%+v", intervals)
	}
	report := HeartbeatReport(intervals)
	if !strings.Contains(report, "failed increased") || !strings.Contains(report, "circuit breaker") {
		t.Fatalf("report=%s", report)
	}
}

func TestHeartbeatAnalysisWarnsOnRestartAndGap(t *testing.T) {
	start := time.Date(2026, 8, 14, 17, 0, 0, 0, time.Local)
	samples := []HeartbeatSample{{Timestamp: start, Pod: "pod", Container: "worker", Uptime: 10 * time.Minute, HasUptime: true, OK: 10, Breaker: "closed"}, {Timestamp: start.Add(3 * time.Minute), Pod: "pod", Container: "worker", Uptime: time.Minute, HasUptime: true, OK: 1, Breaker: "closed"}}
	intervals := AnalyzeHeartbeatSamples(samples, 15*time.Minute, start.Add(3*time.Minute))
	if len(intervals) != 1 || intervals[0].Severity != "WARN" {
		t.Fatalf("intervals=%+v", intervals)
	}
	joined := strings.Join(intervals[0].Reasons, ";")
	if !strings.Contains(joined, "gap") || !strings.Contains(joined, "restarted") {
		t.Fatalf("reasons=%s", joined)
	}
}
