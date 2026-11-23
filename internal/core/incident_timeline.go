package core

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// IncidentTimelineWindow is an investigation horizon, not guaranteed Kubernetes
// retention. Kubernetes Events are aggregated and may have expired already.
const IncidentTimelineWindow = 24 * time.Hour
const IncidentCorrelationWindow = 15 * time.Minute

// IncidentTimelineEntry combines Kubernetes-provided timestamps and Tailg
// observations. Nearby is a verified owner-chain relationship with a preceding
// change, not evidence that the change caused the incident.
type IncidentTimelineEntry struct {
	ID       string
	At       time.Time
	Kind     string // CHANGE, EVENT or LOG
	Object   string
	Heading  string
	Detail   string
	Observed bool
	Warning  bool
	Count    int
	FirstAt  time.Time
	Nearby   []IncidentTimelineChange
}

type IncidentTimelineChange struct {
	At       time.Time
	Object   string
	Action   string
	Detail   string
	Observed bool
}

// IncidentTimeline merges retained changes, Kubernetes Events and session
// log-error scan markers. Normal events are opt-in to keep the initial view
// useful even in noisy namespaces. Entries are sorted newest-first.
func (t *NamespaceActivityTracker) IncidentTimeline(now time.Time, includeNormalEvents bool) []IncidentTimelineEntry {
	if t == nil {
		return nil
	}
	since := now.Add(-IncidentTimelineWindow)
	changes := t.Changes()
	timeline := make([]IncidentTimelineEntry, 0, len(changes)+len(t.Events))
	for _, change := range changes {
		if !incidentValidTime(change.At, since, now) {
			continue
		}
		kind := "CHANGE"
		warning := false
		if change.Action == "Log errors observed" {
			kind = "LOG"
			warning = true
		}
		timeline = append(timeline, IncidentTimelineEntry{
			ID: "change/" + change.ID, At: change.At, Kind: kind,
			Object: change.Object, Heading: change.Action, Detail: change.Detail,
			Observed: change.Observed, Warning: warning,
		})
	}
	for _, event := range t.Events {
		warning := strings.EqualFold(event.Type, "Warning")
		if !warning && !includeNormalEvents {
			continue
		}
		if !incidentValidTime(event.LastAt, since, now) {
			continue
		}
		detail := fmt.Sprintf("Type: %s\nReason: %s\nObject: %s\nOccurrences: %d\nFirst: %s\nLast: %s\nSource: %s\n\n%s",
			event.Type, event.Reason, event.Object, event.Count,
			incidentTime(event.FirstAt), incidentTime(event.LastAt), event.Source, event.Message)
		timeline = append(timeline, IncidentTimelineEntry{
			ID: "event/" + event.ID, At: event.LastAt, Kind: "EVENT",
			Object: event.Object, Heading: event.Type + " · " + event.Reason,
			Detail: detail, Warning: warning, Count: event.Count, FirstAt: event.FirstAt,
		})
	}
	owners := t.incidentOwners()
	for i := range timeline {
		entry := &timeline[i]
		if !entry.Warning {
			continue
		}
		anchor := entry.At
		if entry.Kind == "EVENT" && !entry.FirstAt.IsZero() && !entry.FirstAt.After(entry.At) {
			// The last occurrence of a recurring Event cannot establish its
			// start. Correlate against the first observed occurrence instead.
			anchor = entry.FirstAt
		}
		for _, change := range changes {
			if change.At.After(anchor) || anchor.Sub(change.At) > IncidentCorrelationWindow ||
				change.Action == "Log errors observed" || !incidentValidTime(change.At, since, now) {
				continue
			}
			if !incidentSameWorkload(owners, entry.Object, change.Object) {
				continue
			}
			entry.Nearby = append(entry.Nearby, IncidentTimelineChange{
				At: change.At, Object: change.Object, Action: change.Action,
				Detail: change.Detail, Observed: change.Observed,
			})
			if len(entry.Nearby) == 3 {
				break
			}
		}
	}
	sort.SliceStable(timeline, func(i, j int) bool {
		if timeline[i].At.Equal(timeline[j].At) {
			return timeline[i].ID < timeline[j].ID
		}
		return timeline[i].At.After(timeline[j].At)
	})
	return timeline
}

func incidentValidTime(at, since, now time.Time) bool {
	return !at.IsZero() && !at.Before(since) && !at.After(now.Add(time.Minute))
}

func incidentTime(t time.Time) string {
	if t.IsZero() {
		return "unknown"
	}
	return t.Local().Format("2006-01-02 15:04:05 MST")
}

// Construct a verifiable resource graph from Kubernetes ownerReferences.
// No guessing of Deployment names from pod name prefixes.
func (t *NamespaceActivityTracker) incidentOwners() map[string]string {
	owners := make(map[string]string, len(t.previous))
	for _, object := range t.previous {
		if object.Owner != "" {
			owners[object.Kind+"/"+object.Name] = object.Owner
		}
	}
	return owners
}

func incidentSameWorkload(owners map[string]string, left, right string) bool {
	if left == "" || right == "" {
		return false
	}
	seen := make(map[string]struct{}, 8)
	current := left
	for i := 0; i < 8 && current != ""; i++ {
		if _, ok := seen[current]; ok {
			break
		}
		seen[current] = struct{}{}
		current = owners[current]
	}
	current = right
	visited := make(map[string]struct{}, 8)
	for i := 0; i < 8 && current != ""; i++ {
		if _, loop := visited[current]; loop {
			break
		}
		visited[current] = struct{}{}
		if _, match := seen[current]; match {
			return true
		}
		current = owners[current]
	}
	return false
}
