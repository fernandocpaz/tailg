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
			"status": map[string]any{"phase": "Running", "containerStatuses": []any{map[string]any{"name": "api", "ready": true, "restartCount": float64(0)}}},
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
