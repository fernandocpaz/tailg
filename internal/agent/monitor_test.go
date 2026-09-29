package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMonitorPersistsAndUnchangedSnapshotIsSilent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	spec := testMonitorSpec()
	store := openTestMonitor(t, path, spec)
	first := testMonitorReport(spec, testMonitorIssue("issue-1", "Deployment/api", "api-1", 3, "2026-01-01T00:00:00Z"))
	now := time.Date(2026, 1, 1, 0, 0, 1, 0, time.UTC)
	changes, err := store.Apply(first, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 2 { // coverage and incident.opened
		t.Fatalf("initial changes = %d, want coverage and opened", len(changes))
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store = openTestMonitor(t, path, spec)
	defer store.Close()
	changes, err = store.Apply(first, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 0 {
		t.Fatalf("unchanged persisted snapshot emitted duplicate events: %+v", changes)
	}
	incident := onlyMonitorIncident(t, store.state)
	if incident.Occurrence != 1 {
		t.Fatalf("occurrence after restart = %d, want 1", incident.Occurrence)
	}
}

func TestMonitorWindowCountsAreSnapshotsAndIncidentsKeyByWorkload(t *testing.T) {
	spec := testMonitorSpec()
	store := openTestMonitor(t, filepath.Join(t.TempDir(), "state.json"), spec)
	defer store.Close()
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	_, err := store.Apply(testMonitorReport(spec, testMonitorIssue("issue-1", "Deployment/api", "api-1", 8, "2026-01-01T00:00:00Z")), t0)
	if err != nil {
		t.Fatal(err)
	}
	// A newer polling window reports its own count; it is not added to the
	// overlapping previous window. A pod replacement keeps the same issue ID.
	replacement := testMonitorIssue("issue-1", "Deployment/api", "api-2", 2, "2026-01-01T00:00:02Z")
	changes, err := store.Apply(testMonitorReport(spec, replacement), t0.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || changes[0].Type != "incident.updated" {
		t.Fatalf("replacement update = %+v, want one update", changes)
	}
	if replacementID := changes[0].Incident.ID; replacementID != changes[0].Incident.ID {
		t.Fatalf("replacement incident ID changed: %q", replacementID)
	}
	if len(store.state.Incidents) != 1 {
		t.Fatalf("replacement created another incident: %+v", store.state.Incidents)
	}
	incident := onlyMonitorIncident(t, store.state)
	if incident.WindowCount != 2 || incident.Occurrence != 1 || strings.Join(incident.Pods, ",") != "api-2" {
		t.Fatalf("replacement snapshot = %+v; count should be latest window only", incident)
	}

	other := testMonitorIssue("issue-1", "Deployment/worker", "worker-1", 4, "2026-01-01T00:00:03Z")
	_, err = store.Apply(testMonitorReport(spec, replacement, other), t0.Add(3*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if len(store.state.Incidents) != 2 {
		t.Fatalf("same issue ID on another workload was conflated: %+v", store.state.Incidents)
	}
}

func TestMonitorCoverageAndPollingGapResetQuietPeriod(t *testing.T) {
	spec := testMonitorSpec()
	spec.Since = "1s"
	spec.ResolveAfterSeconds = 1
	store := openTestMonitor(t, filepath.Join(t.TempDir(), "state.json"), spec)
	defer store.Close()
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	issue := testMonitorIssue("issue-1", "Deployment/api", "api-1", 1, "2026-01-01T00:00:00Z")
	if _, err := store.Apply(testMonitorReport(spec, issue), t0); err != nil {
		t.Fatal(err)
	}
	absent := func(status string) Report {
		report := testMonitorReport(spec)
		report.Coverage.Status = status
		report.Scope.Workloads = []string{"Deployment/api"}
		return report
	}
	apply := func(offset time.Duration, report Report) {
		t.Helper()
		if _, err := store.Apply(report, t0.Add(offset)); err != nil {
			t.Fatal(err)
		}
	}
	apply(time.Second, absent("complete"))
	if quiet := onlyMonitorIncident(t, store.state).QuietSince; quiet == "" {
		t.Fatal("first complete absent scan did not start quiet period")
	}
	apply(2*time.Second, absent("partial"))
	if quiet := onlyMonitorIncident(t, store.state).QuietSince; quiet != "" {
		t.Fatalf("partial coverage retained quiet timer %q", quiet)
	}
	apply(3*time.Second, absent("unavailable"))
	if quiet := onlyMonitorIncident(t, store.state).QuietSince; quiet != "" {
		t.Fatalf("unavailable coverage retained quiet timer %q", quiet)
	}
	apply(4*time.Second, absent("complete")) // start a fresh quiet period
	apply(6*time.Second, absent("complete")) // polling gap exceeds the 1s window
	if store.state.Coverage.Status != "partial" || onlyMonitorIncident(t, store.state).QuietSince != "" {
		t.Fatalf("polling gap did not mark partial and reset quiet period: coverage=%+v incident=%+v", store.state.Coverage, onlyMonitorIncident(t, store.state))
	}
	apply(7*time.Second, absent("complete"))
	apply(20*time.Second, absent("complete")) // gap itself must not count as quiet time
	if onlyMonitorIncident(t, store.state).Status == "resolved" {
		t.Fatal("polling gap incorrectly counted toward quiet period")
	}
	apply(21*time.Second, absent("complete"))
	apply(22*time.Second, absent("complete"))
	if onlyMonitorIncident(t, store.state).Status != "resolved" {
		t.Fatal("incident did not resolve after a continuous complete quiet interval")
	}
}

func TestMonitorRequiresWorkloadAndCompleteAbsenceToResolve(t *testing.T) {
	spec := testMonitorSpec()
	store := openTestMonitor(t, filepath.Join(t.TempDir(), "state.json"), spec)
	defer store.Close()
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	issue := testMonitorIssue("issue-1", "Deployment/api", "api-1", 1, "2026-01-01T00:00:00Z")
	if _, err := store.Apply(testMonitorReport(spec, issue), t0); err != nil {
		t.Fatal(err)
	}
	missing := testMonitorReport(spec)
	missing.Scope.Workloads = []string{"Deployment/other"}
	missing.Scope.Pods = []string{"other-1"}
	for _, offset := range []time.Duration{time.Second, 20 * time.Second} {
		if _, err := store.Apply(missing, t0.Add(offset)); err != nil {
			t.Fatal(err)
		}
	}
	if onlyMonitorIncident(t, store.state).Status != "open" {
		t.Fatal("incident resolved while its workload was absent from scope")
	}
	emptyTarget := testMonitorReport(spec)
	emptyTarget.Coverage = Coverage{Status: "complete", ExpectedStreams: 0, CollectedStreams: 0}
	emptyTarget.Scope.Workloads = nil
	for _, offset := range []time.Duration{21 * time.Second, 40 * time.Second} {
		if _, err := store.Apply(emptyTarget, t0.Add(offset)); err != nil {
			t.Fatal(err)
		}
	}
	if onlyMonitorIncident(t, store.state).Status != "open" {
		t.Fatal("incident resolved while the monitored target had no matching streams")
	}

	unknownWorkload := testMonitorIssue("missing-owner", "", "api-1", 1, "2026-01-01T00:00:02Z")
	_, err := store.Apply(testMonitorReport(spec, unknownWorkload), t0.Add(21*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	unknown := store.state.Incidents[monitorIncidentID(spec, unknownWorkload)]
	if unknown.Status != "open" {
		t.Fatalf("missing-workload incident was not retained open: %+v", unknown)
	}
}

func TestMonitorResolvesAndReopensOnlyForFreshEvidence(t *testing.T) {
	spec := testMonitorSpec()
	store := openTestMonitor(t, filepath.Join(t.TempDir(), "state.json"), spec)
	defer store.Close()
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	issue := testMonitorIssue("issue-1", "Deployment/api", "api-1", 1, "2026-01-01T00:00:00Z")
	if _, err := store.Apply(testMonitorReport(spec, issue), t0); err != nil {
		t.Fatal(err)
	}
	absent := testMonitorReport(spec)
	absent.Scope.Workloads = []string{"Deployment/api"}
	if _, err := store.Apply(absent, t0.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	changes, err := store.Apply(absent, t0.Add(7*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || changes[0].Type != "incident.resolved" {
		t.Fatalf("resolution changes = %+v", changes)
	}
	resolved := onlyMonitorIncident(t, store.state)
	if resolved.Status != "resolved" || resolved.Occurrence != 1 {
		t.Fatalf("resolved incident = %+v", resolved)
	}
	// Retained replay data predates ResolvedAt and must not reopen the incident.
	changes, err = store.Apply(testMonitorReport(spec, issue), t0.Add(8*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range changes {
		if change.Type == "incident.reopened" || change.Type == "incident.opened" {
			t.Fatalf("historical replay reopened incident: changes=%+v incident=%+v", changes, onlyMonitorIncident(t, store.state))
		}
	}
	if onlyMonitorIncident(t, store.state).Status != "resolved" {
		t.Fatalf("historical replay changed incident status: %+v", onlyMonitorIncident(t, store.state))
	}
	fresh := testMonitorIssue("issue-1", "Deployment/api", "api-2", 1, "2026-01-01T00:00:09Z")
	changes, err = store.Apply(testMonitorReport(spec, fresh), t0.Add(9*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	incident := onlyMonitorIncident(t, store.state)
	if len(changes) != 1 || changes[0].Type != "incident.reopened" || incident.Occurrence != 2 || incident.Status != "open" {
		t.Fatalf("fresh evidence did not reopen occurrence 2: changes=%+v incident=%+v", changes, incident)
	}
}

func TestMonitorChangesPaginationAndCursorValidation(t *testing.T) {
	spec := testMonitorSpec()
	store := openTestMonitor(t, filepath.Join(t.TempDir(), "state.json"), spec)
	defer store.Close()
	issue := testMonitorIssue("issue-1", "Deployment/api", "api-1", 1, "2026-01-01T00:00:00Z")
	if _, err := store.Apply(testMonitorReport(spec, issue), time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	first, err := store.Changes("", 1)
	if err != nil || len(first.Events) != 1 || !first.HasMore {
		t.Fatalf("first page = %+v, err=%v", first, err)
	}
	second, err := store.Changes(first.NextCursor, 1)
	if err != nil || len(second.Events) != 1 || second.HasMore || second.Events[0].Type != "incident.opened" {
		t.Fatalf("second page = %+v, err=%v", second, err)
	}
	if _, err := store.Changes("different-store:0", 10); err == nil || !strings.Contains(err.Error(), "different monitor") {
		t.Fatalf("different-store cursor error = %v", err)
	}
	stale := store.state
	stale.Sequence = 1001
	stale.Events = make([]MonitorEvent, monitorEventRetention)
	for i := range stale.Events {
		stale.Events[i] = MonitorEvent{Cursor: stale.cursor(uint64(i + 2))}
	}
	page, err := stale.Changes(stale.cursor(0), 10)
	if err != nil || !page.ResetRequired {
		t.Fatalf("expired cursor page = %+v, err=%v; want reset", page, err)
	}
}

func TestMonitorWriterLockScopeAndCorruptStateSafety(t *testing.T) {
	spec := testMonitorSpec()
	path := filepath.Join(t.TempDir(), "state.json")
	first := openTestMonitor(t, path, spec)
	if _, err := first.Apply(testMonitorReport(spec), time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenMonitorStore(path, spec); err == nil || !strings.Contains(err.Error(), "already in use") {
		t.Fatalf("second open while locked error = %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second := openTestMonitor(t, path, spec)
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
	otherScope := spec
	otherScope.Namespace = "other"
	if _, err := OpenMonitorStore(path, otherScope); err == nil || !strings.Contains(err.Error(), "different scope") {
		t.Fatalf("scope mismatch error = %v", err)
	}

	corruptPath := filepath.Join(t.TempDir(), "corrupt.json")
	original := []byte("not valid monitor JSON\n")
	if err := os.WriteFile(corruptPath, original, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenMonitorStore(corruptPath, spec); err == nil {
		t.Fatal("opening corrupt monitor state unexpectedly succeeded")
	}
	after, err := os.ReadFile(corruptPath)
	if err != nil || string(after) != string(original) {
		t.Fatalf("corrupt state was changed: %q, err=%v", after, err)
	}
}

func testMonitorSpec() MonitorSpec {
	return MonitorSpec{Context: "cluster-a", Namespace: "payments", Target: "apps", Container: "api", Since: "30s", ResolveAfterSeconds: 5}
}

func openTestMonitor(t *testing.T, path string, spec MonitorSpec) *MonitorStore {
	t.Helper()
	store, err := OpenMonitorStore(path, spec)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func testMonitorReport(spec MonitorSpec, issues ...Issue) Report {
	workloads := []string{}
	pods := []string{}
	status := "healthy"
	if len(issues) > 0 {
		status = "error"
	}
	for _, issue := range issues {
		if issue.Workload != "" {
			workloads = append(workloads, issue.Workload)
		}
		pods = append(pods, issue.Pods...)
	}
	return Report{
		SchemaVersion: SchemaVersion, Kind: "DiagnosticReport", Scope: Scope{Context: spec.Context, Namespace: spec.Namespace, Target: spec.Target, Workloads: workloads, Pods: pods},
		Summary:  Summary{Status: status, Errors: len(issues)},
		Coverage: Coverage{Status: "complete", ExpectedStreams: 1, CollectedStreams: 1},
		Issues:   issues, CollectionErrors: []CollectionError{},
	}
}

func testMonitorIssue(id, workload, pod string, count int, lastSeen string) Issue {
	return Issue{ID: id, Workload: workload, Severity: "error", Kind: "application_error", Summary: "request failed", Service: "api", Pods: []string{pod}, Count: count,
		FirstSeen: "2026-01-01T00:00:00Z", LastSeen: lastSeen}
}

func onlyMonitorIncident(t *testing.T, state MonitorState) Incident {
	t.Helper()
	if len(state.Incidents) != 1 {
		t.Fatalf("want exactly one incident, got %+v", state.Incidents)
	}
	for _, incident := range state.Incidents {
		return incident
	}
	panic("unreachable")
}

func monitorIncidentID(spec MonitorSpec, issue Issue) string {
	keyData := spec.Context + "\x00" + spec.Namespace + "\x00" + issue.Workload + "\x00" + issue.ID
	hash := sha256.Sum256([]byte(keyData))
	return hex.EncodeToString(hash[:16])
}
