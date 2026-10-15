package core

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

const NamespaceChangeLimit = 1000
const NamespaceEventLimit = 500
const NamespaceChangeWindow = 24 * time.Hour

type NamespaceObject struct {
	Resource, Kind, Name, UID string
	CreatedAt                 time.Time
	Generation                int
	Images                    string
	Owner                     string
	Restarts                  map[string]int
	RestartTimes              map[string]time.Time
	Replicas                  int
	Configuration             string // selected non-secret fields: restart annotation, schedule, suspension
	Seeds                     []NamespaceChange
}

func (o NamespaceObject) Key() string { return o.Resource + "/" + o.Name }

type NamespaceChange struct {
	ID                     string
	At                     time.Time
	Object, Action, Detail string
	Observed               bool // true when timestamp is tailg's observation, not a Kubernetes timestamp
}

type NamespaceEvent struct {
	ID, Type, Reason, Object, Message, Source string
	FirstAt, LastAt                           time.Time
	Count                                     int
}

type NamespaceActivitySnapshot struct {
	At       time.Time
	Objects  []NamespaceObject
	Events   []NamespaceEvent
	Complete map[string]bool // success for each independently listed resource
	Errors   []string
}

// NamespaceActivityTracker belongs to one context/namespace picker session.
// A failed resource list never becomes evidence that its objects were removed.
type NamespaceActivityTracker struct {
	previous         map[string]NamespaceObject
	initialized      map[string]bool
	changes          map[string]NamespaceChange
	Events           []NamespaceEvent
	EventsAt         time.Time
	EventTotal       int
	LastSnapshot     time.Time
	Errors           []string
	ChangesTruncated bool
}

func (t *NamespaceActivityTracker) Apply(snapshot NamespaceActivitySnapshot) {
	if t.previous == nil {
		t.previous = map[string]NamespaceObject{}
		t.initialized = map[string]bool{}
		t.changes = map[string]NamespaceChange{}
	}
	t.LastSnapshot = snapshot.At
	t.Errors = append([]string(nil), snapshot.Errors...)
	next := map[string]NamespaceObject{}
	for _, object := range snapshot.Objects {
		if !snapshot.Complete[object.Resource] {
			continue
		}
		key := object.Key()
		next[key] = object
		for _, seed := range object.Seeds {
			t.add(seed, snapshot.At)
		}
		old, found := t.previous[key]
		same := found && (old.UID == object.UID || old.UID == "" || object.UID == "")
		add := func(action, detail string) {
			t.add(NamespaceChange{ID: fmt.Sprintf("%s/%s/%s/%d", object.UID, key, action, snapshot.At.UnixNano()), At: snapshot.At, Object: object.Kind + "/" + object.Name, Action: action, Detail: detail, Observed: true}, snapshot.At)
		}
		if found && !same {
			add("Recreated", "Object UID changed: "+old.UID+" → "+object.UID)
		}
		if same {
			for container, count := range object.Restarts {
				if count > old.Restarts[container] && object.RestartTimes[container].IsZero() {
					add("Container restart observed", fmt.Sprintf("%s restart count: %d → %d; Kubernetes did not provide a restart timestamp", container, old.Restarts[container], count))
				}
			}
			changed := false
			if old.Images != object.Images {
				add("Image changed", old.Images+" → "+object.Images)
				changed = true
			}
			if old.Replicas != object.Replicas {
				add("Scaled", fmt.Sprintf("Desired replicas: %d → %d", old.Replicas, object.Replicas))
				changed = true
			}
			if old.Configuration != object.Configuration {
				add("Configuration changed", old.Configuration+" → "+object.Configuration)
				changed = true
			}
			if old.Generation != object.Generation && !changed {
				add("Spec updated", fmt.Sprintf("Generation %d → %d; field history is unavailable", old.Generation, object.Generation))
			}
		} else if !found && t.initialized[object.Resource] && (object.CreatedAt.IsZero() || object.CreatedAt.Before(snapshot.At.Add(-NamespaceChangeWindow))) {
			add("Discovered", "First observed in this session; creation time is unavailable or older than 24h")
		}
	}
	for key, old := range t.previous {
		if !snapshot.Complete[old.Resource] {
			continue
		}
		if _, found := next[key]; !found {
			t.add(NamespaceChange{ID: fmt.Sprintf("%s/%s/removed/%d", old.UID, key, snapshot.At.UnixNano()), At: snapshot.At, Object: old.Kind + "/" + old.Name, Action: "Removed", Detail: "No longer returned by a successful namespace inventory; removal observed by tailg. Last known controller: " + old.Owner, Observed: true}, snapshot.At)
			delete(t.previous, key)
		}
	}
	for key, object := range next {
		t.previous[key] = object
	}
	for resource, complete := range snapshot.Complete {
		if complete {
			t.initialized[resource] = true
		}
	}
	if snapshot.Complete["events"] {
		t.Events = append([]NamespaceEvent(nil), snapshot.Events...)
		sort.SliceStable(t.Events, func(i, j int) bool {
			if t.Events[i].LastAt.Equal(t.Events[j].LastAt) {
				return t.Events[i].ID < t.Events[j].ID
			}
			return t.Events[i].LastAt.After(t.Events[j].LastAt)
		})
		t.EventTotal = len(t.Events)
		if len(t.Events) > NamespaceEventLimit {
			t.Events = t.Events[:NamespaceEventLimit]
		}
		t.EventsAt = snapshot.At
	}
	for id, change := range t.changes {
		if change.At.Before(snapshot.At.Add(-NamespaceChangeWindow)) {
			delete(t.changes, id)
		}
	}
	ordered := t.Changes()
	if len(ordered) > NamespaceChangeLimit {
		t.ChangesTruncated = true
		for _, change := range ordered[NamespaceChangeLimit:] {
			delete(t.changes, change.ID)
		}
	}
}

// Record adds timestamped observations such as a monitored application's log
// error count. It does not claim when the first error or root cause occurred.
func (t *NamespaceActivityTracker) Record(change NamespaceChange) {
	if t.previous == nil {
		t.previous = map[string]NamespaceObject{}
		t.initialized = map[string]bool{}
		t.changes = map[string]NamespaceChange{}
	}
	t.add(change, change.At)
}

func (t *NamespaceActivityTracker) add(change NamespaceChange, now time.Time) {
	if change.At.IsZero() || change.At.Before(now.Add(-NamespaceChangeWindow)) || change.At.After(now.Add(time.Minute)) {
		return
	}
	if _, exists := t.changes[change.ID]; !exists {
		t.changes[change.ID] = change
	}
}
func (t NamespaceActivityTracker) Changes() []NamespaceChange {
	result := make([]NamespaceChange, 0, len(t.changes))
	for _, change := range t.changes {
		result = append(result, change)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].At.Equal(result[j].At) {
			return result[i].ID < result[j].ID
		}
		return result[i].At.After(result[j].At)
	})
	return result
}

// SortedImages makes container reordering irrelevant to image comparisons.
func SortedImages(images map[string]string) string {
	var result []string
	for name, image := range images {
		result = append(result, name+"="+image)
	}
	sort.Strings(result)
	return strings.Join(result, ", ")
}
