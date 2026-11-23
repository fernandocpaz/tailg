package core

import (
	"strings"
	"testing"
	"time"
)

func incidentFixture(at time.Time) NamespaceActivityTracker {
	var tracker NamespaceActivityTracker
	tracker.Apply(NamespaceActivitySnapshot{
		At: at,
		Complete: map[string]bool{"pods": true, "replicasets": true, "deployments": true, "events": true},
		Objects: []NamespaceObject{
			{Kind: "Deployment", Resource: "deployments", Name: "api", UID: "dep1"},
			{Kind: "ReplicaSet", Resource: "replicasets", Name: "api-7d", UID: "rs1", Owner: "Deployment/api"},
			{Kind: "Pod", Resource: "pods", Name: "api-7d-ab", UID: "pod1", Owner: "ReplicaSet/api-7d"},
			{Kind: "Deployment", Resource: "deployments", Name: "other", UID: "dep2"},
			{Kind: "Pod", Resource: "pods", Name: "other-1", UID: "pod2", Owner: "Deployment/other"},
		},
		Events: []NamespaceEvent{
			{ID: "warn", Type: "Warning", Reason: "BackOff", Object: "Pod/api-7d-ab", Count: 3,
				Message: "backing off", FirstAt: at.Add(-4 * time.Minute), LastAt: at.Add(-time.Minute)},
			{ID: "normal", Type: "Normal", Reason: "Started", Object: "Pod/api-7d-ab",
				Message: "started", LastAt: at.Add(-2 * time.Minute)},
		},
	})
	tracker.Record(NamespaceChange{
		ID: "api-rollout", At: at.Add(-5 * time.Minute), Object: "Deployment/api",
		Action: "Rollout revision created", Detail: "revision 12", Observed: false,
	})
	tracker.Record(NamespaceChange{
		ID: "unrelated", At: at.Add(-6 * time.Minute), Object: "Deployment/other",
		Action: "Image changed", Detail: "not related", Observed: true,
	})
	tracker.Record(NamespaceChange{
		ID: "error-scan", At: at.Add(-30 * time.Second), Object: "Application/api",
		Action: "Log errors observed", Detail: "4 errors in preceding minute", Observed: true,
	})
	return tracker
}

func TestIncidentTimelineCombinesSignalsAndResolvesVerifiedOwners(t *testing.T) {
	now := time.Date(2026, 11, 23, 16, 0, 0, 0, time.UTC)
	tracker := incidentFixture(now)
	rows := tracker.IncidentTimeline(now, false)
	if len(rows) != 4 { // two changes, one warning Event, one log scan
		t.Fatalf("unexpected timeline: %#v", rows)
	}
	if rows[0].Kind != "LOG" || !rows[0].Observed || rows[0].Heading != "Log errors observed" {
		t.Fatalf("newest signal missing or incorrectly sourced: %#v", rows[0])
	}
	var warning *IncidentTimelineEntry
	for i := range rows {
		if rows[i].Kind == "EVENT" {
			warning = &rows[i]
		}
	}
	if warning == nil || !warning.Warning || warning.Count != 3 || len(warning.Nearby) != 1 {
		t.Fatalf("expected related rollout on warning: %#v", warning)
	}
	if got := warning.Nearby[0]; got.Object != "Deployment/api" || got.Action != "Rollout revision created" || got.Observed {
		t.Fatalf("incorrect correlation: %#v", got)
	}
	if !strings.Contains(warning.Detail, "First:") || !strings.Contains(warning.Detail, "Last:") {
		t.Fatal("repeated Kubernetes Event lost first/last timestamps")
	}
	all := tracker.IncidentTimeline(now, true)
	if len(all) != 5 {
		t.Fatalf("normal Event toggle count=%d", len(all))
	}
	for _, e := range all {
		if e.ID == "change/unrelated" && len(e.Nearby) != 0 {
			t.Fatal("unrelated workload was assigned correlation")
		}
	}
}

func TestIncidentTimelineRecurringWarningUsesFirstOccurrenceForCorrelation(t *testing.T) {
	now := time.Date(2026, 11, 23, 16, 0, 0, 0, time.UTC)
	tracker := incidentFixture(now)
	// A rollout after the first failure must not be linked simply because the
	// warning continues to repeat.
	tracker.Record(NamespaceChange{
		ID: "late-rollout", At: now.Add(-2 * time.Minute), Object: "Deployment/api",
		Action: "Image changed", Detail: "post-warning rollout",
	})
	rows := tracker.IncidentTimeline(now, false)
	for _, entry := range rows {
		if entry.Kind == "EVENT" {
			if len(entry.Nearby) != 1 || entry.Nearby[0].Action != "Rollout revision created" {
				t.Fatalf("post-warning change falsely attributed: %#v", entry.Nearby)
			}
			return
		}
	}
	t.Fatal("warning missing")
}

func TestIncidentTimelineUsesExactOwnerGraphNotNamePrefixes(t *testing.T) {
	owners := map[string]string{
		"Pod/api-1": "ReplicaSet/api-7d",
		"ReplicaSet/api-7d": "Deployment/api",
		"Pod/api-another": "Deployment/other",
		"Pod/loop1": "Pod/loop2", "Pod/loop2": "Pod/loop1",
	}
	if !incidentSameWorkload(owners, "Pod/api-1", "Deployment/api") {
		t.Fatal("verified controller ancestry did not match")
	}
	for _, pair := range [][2]string{
		{"Pod/api-1", "Deployment/api-unrelated"},
		{"Pod/api-1", "Pod/api-another"},
		{"Application/api", "Deployment/api"},
		{"Pod/loop1", "Deployment/api"},
	} {
		if incidentSameWorkload(owners, pair[0], pair[1]) {
			t.Fatalf("invented workload relationship between %q and %q", pair[0], pair[1])
		}
	}
}

func TestIncidentTimelineRespectsHorizonAndIncompleteSources(t *testing.T) {
	now := time.Date(2026, 11, 23, 16, 0, 0, 0, time.UTC)
	tracker := incidentFixture(now)
	tracker.Apply(NamespaceActivitySnapshot{
		At: now.Add(time.Minute),
		Complete: map[string]bool{"events": false},
		Errors: []string{"events: Forbidden"},
	})
	rows := tracker.IncidentTimeline(now.Add(time.Minute), false)
	var eventRetained bool
	for _, row := range rows {
		eventRetained = eventRetained || row.ID == "event/warn"
	}
	if !eventRetained || len(tracker.Errors) == 0 {
		t.Fatal("partial collection silently erased retained Events")
	}
	// Window expiry is independent of the source's stale-retention failure.
	if got := tracker.IncidentTimeline(now.Add(25*time.Hour), false); len(got) != 0 {
		t.Fatalf("old timeline entries should age out: %v", got)
	}
}
