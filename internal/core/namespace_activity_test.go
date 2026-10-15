package core

import (
	"fmt"
	"testing"
	"time"
)

func activityFixture(at time.Time, images string) NamespaceActivitySnapshot {
	return NamespaceActivitySnapshot{At: at, Complete: map[string]bool{"deployments": true, "events": true}, Objects: []NamespaceObject{{Resource: "deployments", Kind: "Deployment", Name: "api", UID: "deployment-1", CreatedAt: at.Add(-time.Hour), Generation: 1, Images: images, Replicas: 1, Seeds: []NamespaceChange{{ID: "created/api", At: at.Add(-time.Hour), Object: "Deployment/api", Action: "Created", Detail: images}}}}}
}
func TestNamespaceActivityPreservesHistoryAndFindsObservedChanges(t *testing.T) {
	now := time.Date(2026, 10, 15, 13, 0, 0, 0, time.UTC)
	var tracker NamespaceActivityTracker
	tracker.Apply(activityFixture(now, "api=v1"))
	next := activityFixture(now.Add(time.Minute), "api=v2")
	next.Objects[0].Generation = 2
	next.Objects[0].Replicas = 2
	next.Objects[0].Configuration = "Restart requested"
	tracker.Apply(next)
	tracker.Apply(next)
	actions := map[string]int{}
	for _, change := range tracker.Changes() {
		actions[change.Action]++
		if change.Action == "Created" && change.Detail != "api=v1" {
			t.Fatal("seed history rewritten by new image")
		}
		if change.Action == "Image changed" && (!change.Observed || change.Detail != "api=v1 → api=v2") {
			t.Fatalf("wrong evidence: %+v", change)
		}
	}
	for _, action := range []string{"Created", "Image changed", "Scaled", "Configuration changed"} {
		if actions[action] != 1 {
			t.Fatalf("action %s count=%d", action, actions[action])
		}
	}
	if actions["Spec updated"] != 0 {
		t.Fatal("duplicate generic spec marker")
	}
	if got := SortedImages(map[string]string{"z": "v1", "a": "v2"}); got != "a=v2, z=v1" {
		t.Fatal(got)
	}
}
func TestNamespaceActivityPartialListsDoNotFabricateRemovalsOrClearEvents(t *testing.T) {
	now := time.Now()
	var tracker NamespaceActivityTracker
	first := activityFixture(now, "v1")
	first.Events = []NamespaceEvent{{ID: "e", LastAt: now, Count: 4}}
	tracker.Apply(first)
	tracker.Apply(NamespaceActivitySnapshot{At: now.Add(time.Minute), Complete: map[string]bool{}, Errors: []string{"Forbidden"}})
	if len(tracker.Events) != 1 || tracker.EventTotal != 1 || len(tracker.Errors) != 1 {
		t.Fatal("partial refresh cleared data")
	}
	for _, change := range tracker.Changes() {
		if change.Action == "Removed" {
			t.Fatal("permission failure reported as deletion")
		}
	}
	next := activityFixture(now.Add(2*time.Minute), "v2")
	tracker.Apply(next)
	found := false
	for _, change := range tracker.Changes() {
		if change.Action == "Image changed" {
			found = true
		}
	}
	if !found {
		t.Fatal("last successful snapshot lost after failure")
	}
	tracker.Apply(NamespaceActivitySnapshot{At: now.Add(3 * time.Minute), Complete: map[string]bool{"deployments": true, "events": true}})
	if len(tracker.Events) != 0 {
		t.Fatal("successful empty event response did not clear old events")
	}
	found = false
	for _, change := range tracker.Changes() {
		if change.Action == "Removed" {
			found = true
		}
	}
	if !found {
		t.Fatal("successful removal not detected")
	}
}
func TestNamespaceActivityRecreationAndRetention(t *testing.T) {
	now := time.Now()
	var tracker NamespaceActivityTracker
	tracker.Apply(activityFixture(now, "v1"))
	next := activityFixture(now.Add(time.Minute), "v1")
	next.Objects[0].UID = "deployment-2"
	tracker.Apply(next)
	found := false
	for _, change := range tracker.Changes() {
		if change.Action == "Recreated" {
			found = true
		}
	}
	if !found {
		t.Fatal("same name UID replacement missed")
	}
	for i := 0; i < NamespaceChangeLimit+10; i++ {
		tracker.Record(NamespaceChange{ID: fmt.Sprint(i), At: now.Add(time.Duration(i) * time.Second), Object: "api", Action: "Observed"})
	}
	snapshot := NamespaceActivitySnapshot{At: now.Add(time.Hour), Complete: map[string]bool{"events": true}}
	for i := 0; i < NamespaceEventLimit+10; i++ {
		snapshot.Events = append(snapshot.Events, NamespaceEvent{ID: fmt.Sprint(i), LastAt: now.Add(time.Duration(i) * time.Second)})
	}
	tracker.Apply(snapshot)
	if len(tracker.Changes()) != NamespaceChangeLimit || !tracker.ChangesTruncated || len(tracker.Events) != NamespaceEventLimit || tracker.EventTotal != NamespaceEventLimit+10 {
		t.Fatal("history limits not enforced")
	}
	if tracker.Events[0].ID != fmt.Sprint(NamespaceEventLimit+9) {
		t.Fatal("newest event not retained")
	}
	tracker.Apply(NamespaceActivitySnapshot{At: now.Add(26 * time.Hour), Complete: map[string]bool{}})
	if len(tracker.Changes()) != 0 {
		t.Fatal("expired changes retained")
	}
}

func TestNamespaceRestartWithoutTimestampUsesObservedTime(t *testing.T) {
	now := time.Now()
	var tracker NamespaceActivityTracker
	snapshot := NamespaceActivitySnapshot{At: now, Complete: map[string]bool{"pods": true}, Objects: []NamespaceObject{{Resource: "pods", Kind: "Pod", Name: "api", UID: "p", Restarts: map[string]int{"api": 0}}}}
	tracker.Apply(snapshot)
	snapshot.At = now.Add(time.Minute)
	snapshot.Objects[0].Restarts = map[string]int{"api": 1}
	tracker.Apply(snapshot)
	rows := tracker.Changes()
	if len(rows) != 1 || !rows[0].Observed || rows[0].Action != "Container restart observed" || !rows[0].At.Equal(snapshot.At) {
		t.Fatalf("restart fallback: %+v", rows)
	}
}
