package agent

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fernandocpaz/tailg/internal/core"
	"github.com/fernandocpaz/tailg/internal/kube"
)

type coverageClient struct {
	rows       map[string][]core.LogEvent
	logErrors  map[string]error
	jsonByArgs map[string]map[string]any
	jsonErrors map[string]error
}

func (c coverageClient) Snapshot(_ context.Context, item core.InventoryItem, _ kube.LogOptions) ([]core.LogEvent, error) {
	if err := c.logErrors[item.Key()]; err != nil {
		return nil, err
	}
	return c.rows[item.Key()], nil
}

func (c coverageClient) JSON(_ context.Context, args ...string) (map[string]any, error) {
	key := strings.Join(args, " ")
	if err := c.jsonErrors[key]; err != nil {
		return nil, err
	}
	return c.jsonByArgs[key], nil
}

func coverageOptions(limits Limits) CollectOptions {
	return CollectOptions{Mode: ModeIssues, Namespace: "default", Target: "api", Limits: limits,
		Now: func() time.Time { return time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC) }}
}

func TestCoverageMarksLaterContainerStarvationPartial(t *testing.T) {
	first := core.InventoryItem{Pod: "api-1", Container: "api"}
	second := core.InventoryItem{Pod: "api-1", Container: "sidecar"}
	client := coverageClient{rows: map[string][]core.LogEvent{
		first.Key():  {{Pod: first.Pod, Container: first.Container, Message: "started"}},
		second.Key(): {{Pod: second.Pod, Container: second.Container, Message: "started"}},
	}}
	report, err := (Collector{Client: client}).Collect(context.Background(), []core.InventoryItem{first, second}, coverageOptions(Limits{Tail: 10, MaxLines: 1, MaxIssues: 10}))
	if err != nil {
		t.Fatal(err)
	}
	if report.Coverage.Status != "partial" || report.Coverage.ExpectedStreams != 2 || report.Coverage.CollectedStreams != 1 {
		t.Fatalf("coverage=%+v", report.Coverage)
	}
	if report.Summary.Status != "unknown" || ExitCode(report) != 3 {
		t.Fatalf("incomplete clean report must be unknown (exit 3): %+v", report.Summary)
	}
}

func TestCoverageMarksPartialPermissionsAndTailLimit(t *testing.T) {
	good := core.InventoryItem{Pod: "api-1", Container: "api"}
	denied := core.InventoryItem{Pod: "api-2", Container: "api"}
	client := coverageClient{
		rows: map[string][]core.LogEvent{good.Key(): {
			{Pod: good.Pod, Container: good.Container, Message: "one"},
			{Pod: good.Pod, Container: good.Container, Message: "two"},
		}},
		logErrors: map[string]error{denied.Key(): errors.New("forbidden")},
	}
	report, err := (Collector{Client: client}).Collect(context.Background(), []core.InventoryItem{good, denied}, coverageOptions(Limits{Tail: 2, MaxLines: 20, MaxIssues: 10}))
	if err != nil {
		t.Fatal(err)
	}
	if report.Coverage.Status != "partial" || report.Coverage.CollectedStreams != 1 || report.Summary.Status != "warning" {
		t.Fatalf("unexpected partial report: coverage=%+v summary=%+v", report.Coverage, report.Summary)
	}
	if !hasCoverageReason(report.Coverage, "log_collection_failed") || !hasCoverageReason(report.Coverage, "tail_limit_reached") {
		t.Fatalf("coverage reasons=%v", report.Coverage.Reasons)
	}
}

func TestCoverageMarksSelectedPodMissingFromMetadata(t *testing.T) {
	item := core.InventoryItem{Pod: "api-1", Container: "api"}
	client := coverageClient{
		rows: map[string][]core.LogEvent{item.Key(): {{Pod: item.Pod, Container: item.Container, Message: "started"}}},
		jsonByArgs: map[string]map[string]any{
			"get pods":                            {"items": []any{}},
			"get events --sort-by=.lastTimestamp": {"items": []any{}},
		},
	}
	options := coverageOptions(Limits{Tail: 10, MaxLines: 20, MaxIssues: 10})
	options.Mode = ModeDiagnose
	report, err := (Collector{Client: client}).Collect(context.Background(), []core.InventoryItem{item}, options)
	if err != nil {
		t.Fatal(err)
	}
	if report.Coverage.Status != "partial" || !hasCoverageReason(report.Coverage, "selected_pod_missing_from_metadata") || report.Summary.Status != "unknown" {
		t.Fatalf("unexpected report: coverage=%+v summary=%+v", report.Coverage, report.Summary)
	}
}

func TestCoveragePreservesAggregateSummaryWhenByteLimited(t *testing.T) {
	issue := Issue{ID: "issue-1", Severity: "error", Kind: "TIMEOUT", Count: 7, Summary: strings.Repeat("large issue detail ", 500),
		Context: IssueContext{Match: LogLine{Message: strings.Repeat("large context ", 500)}}}
	report := Report{SchemaVersion: SchemaVersion, Kind: "IssueReport", Limits: Limits{MaxBytes: 1600},
		Summary:  Summary{Status: "error", IssueGroups: 1, IssueEvents: 7, Errors: 1},
		Coverage: Coverage{Status: "complete", ExpectedStreams: 1, CollectedStreams: 1, Reasons: []string{}},
		Issues:   []Issue{issue}}
	limited, err := LimitReport(report, "json")
	if err != nil {
		t.Fatal(err)
	}
	if !limited.Truncated || limited.Coverage.Status != "partial" || !hasCoverageReason(limited.Coverage, "output_byte_limit_reached") {
		t.Fatalf("truncation was not reflected in coverage: %+v", limited)
	}
	if limited.Summary.Status != "error" || limited.Summary.IssueGroups != 1 || limited.Summary.IssueEvents != 7 || limited.Summary.Errors != 1 {
		t.Fatalf("aggregate summary changed after detail trimming: %+v", limited.Summary)
	}
	if len(limited.Issues) != 0 {
		t.Fatalf("fixture should require dropping issue detail, kept %d", len(limited.Issues))
	}
	var decoded Report
	data, err := json.Marshal(limited)
	if err != nil || json.Unmarshal(data, &decoded) != nil {
		t.Fatalf("coverage report JSON roundtrip failed: %v", err)
	}
}

func TestCoverageIsCompleteForHealthyFullCollection(t *testing.T) {
	item := core.InventoryItem{Pod: "api-1", Container: "api"}
	client := coverageClient{rows: map[string][]core.LogEvent{item.Key(): {{Pod: item.Pod, Container: item.Container, Message: "request completed"}}}}
	report, err := (Collector{Client: client}).Collect(context.Background(), []core.InventoryItem{item}, coverageOptions(Limits{Tail: 10, MaxLines: 20, MaxIssues: 10}))
	if err != nil {
		t.Fatal(err)
	}
	if report.Coverage.Status != "complete" || report.Coverage.ExpectedStreams != 1 || report.Coverage.CollectedStreams != 1 || report.Summary.Status != "healthy" {
		t.Fatalf("unexpected healthy report: coverage=%+v summary=%+v", report.Coverage, report.Summary)
	}
}

func TestNDJSONSummaryIncludesCoverage(t *testing.T) {
	report := Report{SchemaVersion: SchemaVersion, Kind: "IssueReport", Coverage: Coverage{
		Status: "partial", ExpectedStreams: 3, CollectedStreams: 2, Reasons: []string{"log_collection_failed"},
	}}
	var output strings.Builder
	if err := WriteReport(&output, report, "ndjson"); err != nil {
		t.Fatal(err)
	}
	var record struct {
		Type string `json:"type"`
		Data struct {
			Coverage Coverage `json:"coverage"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(output.String())), &record); err != nil {
		t.Fatal(err)
	}
	if record.Type != "summary" || record.Data.Coverage.Status != "partial" || record.Data.Coverage.ExpectedStreams != 3 {
		t.Fatalf("NDJSON header lost coverage: %+v", record)
	}
}

func TestCoverageUnavailableWhenEveryLogStreamFails(t *testing.T) {
	item := core.InventoryItem{Pod: "api-1", Container: "api"}
	client := coverageClient{logErrors: map[string]error{item.Key(): errors.New("forbidden")}}
	report, err := (Collector{Client: client}).Collect(context.Background(), []core.InventoryItem{item}, coverageOptions(Limits{Tail: 10, MaxLines: 20}))
	if err == nil {
		t.Fatal("expected collection error")
	}
	if report.Coverage.Status != "unavailable" || report.Coverage.ExpectedStreams != 1 || report.Coverage.CollectedStreams != 0 {
		t.Fatalf("coverage=%+v", report.Coverage)
	}
}

func TestKnownIncidentIsNotResolvedWhenLaterLogCollectionFails(t *testing.T) {
	item := core.InventoryItem{Pod: "api-1", Container: "api"}
	client := coverageClient{rows: map[string][]core.LogEvent{item.Key(): {{
		Pod: item.Pod, Container: item.Container, Message: "ERROR upstream timeout",
		ObservedAt: time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC),
	}}}}
	options := coverageOptions(Limits{Tail: 20, MaxLines: 100, MaxIssues: 10})
	options.Context, options.Namespace, options.Target = "staging", "payments", "deployment/api"
	options.Mode = ModeIssues
	options.Workloads = map[string]string{item.Pod: "Deployment/api"}
	report, err := (Collector{Client: client}).Collect(context.Background(), []core.InventoryItem{item}, options)
	if err != nil || len(report.Issues) != 1 {
		t.Fatalf("known incident was not classified: issues=%+v err=%v", report.Issues, err)
	}

	spec := testMonitorSpec()
	spec.Context, spec.Namespace = "staging", "payments"
	store := openTestMonitor(t, filepath.Join(t.TempDir(), "state.json"), spec)
	defer store.Close()
	t0 := time.Date(2026, 9, 1, 12, 0, 1, 0, time.UTC)
	if changes, err := store.Apply(report, t0); err != nil || !containsMonitorEvent(changes, "incident.opened") {
		t.Fatalf("known incident was not persisted: changes=%+v err=%v", changes, err)
	}

	client.logErrors = map[string]error{item.Key(): errors.New("logs forbidden")}
	missed, err := (Collector{Client: client}).Collect(context.Background(), []core.InventoryItem{item}, options)
	if err == nil || missed.Coverage.Status != "unavailable" {
		t.Fatalf("fixture did not simulate missed logs: coverage=%+v err=%v", missed.Coverage, err)
	}
	if _, err := store.Apply(missed, t0.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if onlyMonitorIncident(t, store.state).Status != "open" || store.state.Status == "healthy" {
		t.Fatalf("failed log collection falsely resolved or healed the incident: state=%+v", store.state)
	}
}

func containsMonitorEvent(events []MonitorEvent, kind string) bool {
	for _, event := range events {
		if event.Type == kind {
			return true
		}
	}
	return false
}

func hasCoverageReason(coverage Coverage, reason string) bool {
	for _, item := range coverage.Reasons {
		if item == reason {
			return true
		}
	}
	return false
}
