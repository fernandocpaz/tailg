package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/fernandocpaz/tailg/internal/core"
	"github.com/fernandocpaz/tailg/internal/kube"
)

type fakeKubernetesClient struct {
	snapshots map[string][]core.LogEvent
	pods      map[string]any
	events    map[string]any
}

func (f fakeKubernetesClient) Snapshot(_ context.Context, item core.InventoryItem, _ kube.LogOptions) ([]core.LogEvent, error) {
	return f.snapshots[item.Key()], nil
}

func (f fakeKubernetesClient) JSON(_ context.Context, args ...string) (map[string]any, error) {
	if len(args) > 1 && args[1] == "events" {
		return f.events, nil
	}
	return f.pods, nil
}

func TestCollectorProducesStableBoundedIssueContext(t *testing.T) {
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	item := core.InventoryItem{Pod: "api-abc-123", Container: "api"}
	client := fakeKubernetesClient{
		snapshots: map[string][]core.LogEvent{item.Key(): {
			{Pod: item.Pod, Container: item.Container, Message: "request started", ObservedAt: now.Add(-3 * time.Second)},
			{Pod: item.Pod, Container: item.Container, Message: "ERROR upstream timeout password=hunter2", ObservedAt: now.Add(-2 * time.Second)},
			{Pod: item.Pod, Container: item.Container, Message: "request finished", ObservedAt: now.Add(-time.Second)},
		}},
		pods: map[string]any{"items": []any{map[string]any{
			"metadata": map[string]any{"name": item.Pod}, "spec": map[string]any{"containers": []any{map[string]any{"name": "api"}}},
			"status": map[string]any{"phase": "Running", "containerStatuses": []any{map[string]any{
				"name": "api", "ready": true, "restartCount": float64(2),
				"state":     map[string]any{"running": map[string]any{"startedAt": "2026-08-27T11:59:00Z"}},
				"lastState": map[string]any{"terminated": map[string]any{"reason": "OOMKilled", "exitCode": float64(137), "finishedAt": "2026-08-27T11:58:59Z"}},
			}}},
		}}},
		events: map[string]any{"items": []any{}},
	}
	report, err := (Collector{Client: client}).Collect(context.Background(), []core.InventoryItem{item}, CollectOptions{
		Mode: ModeDiagnose, Namespace: "default", Context: "dev", Target: "api", Now: func() time.Time { return now },
		Limits: Limits{Tail: 500, MaxLines: 100, MaxIssues: 10, ContextLines: 1, MaxBytes: 64 * 1024},
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.SchemaVersion != SchemaVersion || report.Summary.Status != "error" || len(report.Issues) != 1 {
		t.Fatalf("unexpected report: %+v", report)
	}
	issue := report.Issues[0]
	if len(issue.ID) != 16 || issue.Kind != "TIMEOUT" {
		t.Fatalf("unexpected issue: %+v", issue)
	}
	if strings.Contains(issue.Context.Match.Message, "hunter2") || !strings.Contains(issue.Context.Match.Message, "[REDACTED]") {
		t.Fatalf("credential was not redacted: %q", issue.Context.Match.Message)
	}
	if len(issue.Context.Before) != 1 || len(issue.Context.After) != 1 {
		t.Fatalf("unexpected context: %+v", issue.Context)
	}
	if len(report.Pods) != 1 || len(report.Pods[0].Containers) != 1 || report.Pods[0].Containers[0].LastReason != "OOMKilled" || report.Pods[0].Containers[0].LastExitCode != 137 {
		t.Fatalf("missing container crash evidence: %+v", report.Pods)
	}
	foundOOMRecommendation := false
	for _, recommendation := range report.Recommendations {
		if strings.Contains(recommendation, "OOMKilled") {
			foundOOMRecommendation = true
			break
		}
	}
	if !foundOOMRecommendation {
		t.Fatalf("recommendations=%#v", report.Recommendations)
	}
	if ExitCode(report) != 2 {
		t.Fatalf("exit code = %d", ExitCode(report))
	}
}

func TestLimitReportHonorsMaximumEncodedBytes(t *testing.T) {
	report := Report{SchemaVersion: SchemaVersion, Kind: "DiagnosticReport", Limits: Limits{MaxBytes: 4096}, Issues: []Issue{{ID: "1", Severity: "error", Context: IssueContext{Match: LogLine{Message: strings.Repeat("x", 12000)}}}}}
	limited, err := LimitReport(report, "json")
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.MarshalIndent(limited, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if len(data)+1 > report.Limits.MaxBytes {
		t.Fatalf("encoded report is %d bytes", len(data)+1)
	}
	if !limited.Truncated {
		t.Fatal("expected truncation marker")
	}
}

func TestWriteReportProducesNDJSONRecords(t *testing.T) {
	report := Report{SchemaVersion: SchemaVersion, Kind: "DiagnosticReport", Limits: Limits{MaxBytes: 4096}, Issues: []Issue{{ID: "abc", Severity: "warning"}}, Pods: []Pod{}}
	var output strings.Builder
	if err := WriteReport(&output, report, "ndjson"); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d records: %s", len(lines), output.String())
	}
	for _, line := range lines {
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatal(err)
		}
		if record["schemaVersion"] != SchemaVersion {
			t.Fatalf("missing schema version: %v", record)
		}
	}
}

func TestWriteReportProducesHumanTroubleshootingText(t *testing.T) {
	report := Report{
		SchemaVersion: SchemaVersion, Kind: "DiagnosticReport", GeneratedAt: "2026-09-04T15:00:00Z", Window: "tail",
		Scope:  Scope{Namespace: "apollo", Target: "api", Pods: []string{"api-7d9"}},
		Limits: Limits{MaxBytes: 64 * 1024}, Summary: Summary{Status: "error", IssueGroups: 1, IssueEvents: 3, LogLines: 50},
		Pods:            []Pod{{Name: "api-7d9", Phase: "Running", Ready: 1, Total: 1, Restarts: 2, Containers: []Container{{Name: "api", Kind: "container", Ready: true, State: "running", Restarts: 2, LastReason: "OOMKilled", LastExitCode: 137}}}},
		Issues:          []Issue{{ID: "abc123", Severity: "error", Kind: "TIMEOUT", Count: 3, Service: "api", Summary: "upstream timeout", Context: IssueContext{Match: LogLine{Pod: "api-7d9", Container: "api", Message: "ERROR upstream timeout"}}}},
		Recommendations: []string{"Review memory limits."},
	}
	var output strings.Builder
	if err := WriteReport(&output, report, "text"); err != nil {
		t.Fatal(err)
	}
	text := output.String()
	for _, expected := range []string{"ERROR | DiagnosticReport", "PODS", "last=OOMKilled/exit=137", "LOG ISSUES", "TIMEOUT x3", "NEXT ACTIONS", "Review memory limits."} {
		if !strings.Contains(text, expected) {
			t.Fatalf("missing %q in:\n%s", expected, text)
		}
	}
}
