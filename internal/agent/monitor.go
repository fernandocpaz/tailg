package agent

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"
)

const MonitorSchemaVersion = "tailg.monitor/v1"
const monitorEventRetention = 1000

// MonitorSpec binds one state file to an explicit scope and detection policy.
// Changing the current kubectl context never changes an existing monitor.
type MonitorSpec struct {
	Context             string   `json:"context"`
	Namespace           string   `json:"namespace"`
	Target              string   `json:"target"`
	Selector            string   `json:"selector,omitempty"`
	Container           string   `json:"container"`
	Since               string   `json:"since"`
	Tail                int      `json:"tail"`
	MaxLines            int      `json:"maxLines"`
	MaxIssues           int      `json:"maxIssues"`
	MaxBytes            int      `json:"maxBytes"`
	Include             []string `json:"include,omitempty"`
	Exclude             []string `json:"exclude,omitempty"`
	DefaultExclude      bool     `json:"defaultExclude"`
	ResolveAfterSeconds int64    `json:"resolveAfterSeconds"`
}

// WindowCount is a snapshot count, never a sum of overlapping polling windows.
type Incident struct {
	ID          string   `json:"id"`
	IssueID     string   `json:"issueId"`
	Workload    string   `json:"workload"`
	Service     string   `json:"service"`
	Kind        string   `json:"kind"`
	Severity    string   `json:"severity"`
	Summary     string   `json:"summary"`
	Status      string   `json:"status"`
	Occurrence  int      `json:"occurrence"`
	FirstSeen   string   `json:"firstSeen"`
	LastSeen    string   `json:"lastSeen"`
	OpenedAt    string   `json:"openedAt"`
	ResolvedAt  string   `json:"resolvedAt,omitempty"`
	QuietSince  string   `json:"quietSince,omitempty"`
	WindowCount int      `json:"windowCount"`
	Pods        []string `json:"pods"`
	TraceID     string   `json:"traceId,omitempty"`
}

type MonitorEvent struct {
	SchemaVersion string    `json:"schemaVersion"`
	Cursor        string    `json:"cursor"`
	Type          string    `json:"type"`
	ObservedAt    string    `json:"observedAt"`
	Incident      *Incident `json:"incident,omitempty"`
	Coverage      *Coverage `json:"coverage,omitempty"`
	Status        string    `json:"status,omitempty"`
}

type MonitorState struct {
	SchemaVersion string              `json:"schemaVersion"`
	StoreID       string              `json:"storeId"`
	Spec          MonitorSpec         `json:"spec"`
	Sequence      uint64              `json:"sequence"`
	LastPollAt    string              `json:"lastPollAt,omitempty"`
	Coverage      Coverage            `json:"coverage"`
	Status        string              `json:"status"`
	Incidents     map[string]Incident `json:"incidents"`
	Events        []MonitorEvent      `json:"events"`
}

type ChangesPage struct {
	SchemaVersion string         `json:"schemaVersion"`
	Cursor        string         `json:"cursor"`
	NextCursor    string         `json:"nextCursor"`
	OldestCursor  string         `json:"oldestCursor"`
	HasMore       bool           `json:"hasMore"`
	ResetRequired bool           `json:"resetRequired"`
	Coverage      Coverage       `json:"coverage"`
	Status        string         `json:"status"`
	Events        []MonitorEvent `json:"events"`
}

type IncidentList struct {
	SchemaVersion string      `json:"schemaVersion"`
	Cursor        string      `json:"cursor"`
	Scope         MonitorSpec `json:"scope"`
	Coverage      Coverage    `json:"coverage"`
	Status        string      `json:"status"`
	Incidents     []Incident  `json:"incidents"`
}

// MonitorStore has one process-wide writer lock held for its lifetime. The OS
// releases it on process exit, including crashes; readers use atomic snapshots.
type MonitorStore struct {
	path  string
	lock  *os.File
	state MonitorState
}

func normalizeMonitorSpec(spec MonitorSpec) MonitorSpec {
	spec.Include = append([]string(nil), spec.Include...)
	spec.Exclude = append([]string(nil), spec.Exclude...)
	sort.Strings(spec.Include)
	sort.Strings(spec.Exclude)
	return spec
}

func OpenMonitorStore(path string, spec MonitorSpec) (*MonitorStore, error) {
	if path == "" || spec.Context == "" || spec.Namespace == "" {
		return nil, errors.New("monitor requires explicit state path, context and namespace")
	}
	window, err := time.ParseDuration(spec.Since)
	if err != nil || window <= 0 || window > 5*time.Minute {
		return nil, errors.New("monitor --since must be greater than zero and at most 5m")
	}
	if spec.ResolveAfterSeconds <= 0 {
		return nil, errors.New("resolve-after must be at least one second")
	}
	spec = normalizeMonitorSpec(spec)
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	path = absPath
	stateDir := filepath.Dir(path)
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return nil, err
	}
	if err := rejectSymlinkPath(stateDir, false); err != nil {
		return nil, err
	}
	dirInfo, err := os.Lstat(stateDir)
	if err != nil {
		return nil, err
	}
	if !dirInfo.IsDir() || dirInfo.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("monitor state parent must be a real directory")
	}
	for _, name := range []string{path, path + ".lock"} {
		if info, err := os.Lstat(name); err == nil && !info.Mode().IsRegular() {
			return nil, fmt.Errorf("state and lock must be regular files: %s", name)
		} else if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
	}
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := lock.Chmod(0o600); err != nil {
		lock.Close()
		return nil, err
	}
	if err := lockMonitorFile(lock); err != nil {
		lock.Close()
		return nil, fmt.Errorf("monitor state is already in use: %w", err)
	}
	store := &MonitorStore{path: path, lock: lock}
	state, err := ReadMonitorState(path)
	if os.IsNotExist(err) {
		id := make([]byte, 16)
		if _, err = rand.Read(id); err == nil {
			state = MonitorState{SchemaVersion: MonitorSchemaVersion, StoreID: hex.EncodeToString(id), Spec: spec,
				Status: "unknown", Coverage: Coverage{Status: "unavailable"}, Incidents: map[string]Incident{}, Events: []MonitorEvent{}}
		}
	}
	if err == nil && !reflect.DeepEqual(normalizeMonitorSpec(state.Spec), spec) {
		err = errors.New("state belongs to a different scope or policy; use a different --state file")
	}
	if err != nil {
		store.Close()
		return nil, err
	}
	store.state = state
	return store, nil
}

func (s *MonitorStore) Close() error {
	if s.lock == nil {
		return nil
	}
	err := s.lock.Close()
	s.lock = nil
	return err
}

func ReadMonitorState(path string) (MonitorState, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return MonitorState{}, err
	}
	if !info.Mode().IsRegular() {
		return MonitorState{}, errors.New("monitor state must be a regular file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return MonitorState{}, err
	}
	var state MonitorState
	if err := json.Unmarshal(data, &state); err != nil {
		return state, fmt.Errorf("invalid monitor state (not overwritten): %w", err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return state, errors.New("monitor state permissions are unsafe (expected private file)")
	}
	if state.SchemaVersion != MonitorSchemaVersion || len(state.StoreID) != 32 || state.Incidents == nil || state.Events == nil {
		return state, errors.New("unsupported or incomplete monitor state (not overwritten)")
	}
	if _, err := hex.DecodeString(state.StoreID); err != nil || len(state.Events) > monitorEventRetention || state.Sequence < uint64(len(state.Events)) {
		return state, errors.New("invalid monitor sequence or event journal (not overwritten)")
	}
	firstSequence := state.Sequence - uint64(len(state.Events)) + 1
	for index, event := range state.Events {
		sequence := firstSequence + uint64(index)
		if event.SchemaVersion != MonitorSchemaVersion || event.Cursor != state.cursor(sequence) || event.Type == "" {
			return state, errors.New("invalid monitor event journal (not overwritten)")
		}
	}
	return state, nil
}

func (s *MonitorStore) Cursor() string { return s.state.cursor(s.state.Sequence) }

func (s *MonitorStore) ListIncidents() IncidentList { return s.state.ListIncidents() }

func (s MonitorState) cursor(sequence uint64) string {
	return s.StoreID + ":" + strconv.FormatUint(sequence, 10)
}

func (s MonitorState) ListIncidents() IncidentList {
	result := IncidentList{SchemaVersion: MonitorSchemaVersion, Cursor: s.cursor(s.Sequence), Scope: s.Spec,
		Coverage: s.Coverage, Status: s.Status, Incidents: []Incident{}}
	for _, incident := range s.Incidents {
		result.Incidents = append(result.Incidents, incident)
	}
	sort.Slice(result.Incidents, func(i, j int) bool { return result.Incidents[i].ID < result.Incidents[j].ID })
	return result
}

func (s MonitorState) Changes(after string, limit int) (ChangesPage, error) {
	if limit <= 0 || limit > monitorEventRetention {
		return ChangesPage{}, errors.New("change page limit must be between 1 and 1000")
	}
	oldest := s.Sequence - uint64(len(s.Events))
	page := ChangesPage{SchemaVersion: MonitorSchemaVersion, Cursor: s.cursor(s.Sequence), NextCursor: s.cursor(oldest),
		OldestCursor: s.cursor(oldest), Coverage: s.Coverage, Status: s.Status, Events: []MonitorEvent{}}
	sequence := oldest
	if after != "" {
		id, value, ok := strings.Cut(after, ":")
		parsed, err := strconv.ParseUint(value, 10, 64)
		if !ok || err != nil || id != s.StoreID || parsed > s.Sequence {
			return page, errors.New("invalid cursor or cursor belongs to a different monitor")
		}
		sequence = parsed
		if sequence < oldest {
			page.ResetRequired = true
			return page, nil
		}
	}
	page.NextCursor = s.cursor(sequence)
	start := int(sequence - oldest)
	end := min(len(s.Events), start+limit)
	page.Events = append(page.Events, s.Events[start:end]...)
	if len(page.Events) > 0 {
		page.NextCursor = page.Events[len(page.Events)-1].Cursor
	}
	page.HasMore = end < len(s.Events)
	return page, nil
}

func (s *MonitorStore) Changes(after string, limit int) (ChangesPage, error) {
	return s.state.Changes(after, limit)
}

// Apply persists the entire state and its replay journal before handing events
// to a consumer. Delivery is at-least-once via cursors, not exactly-once actions.
func (s *MonitorStore) Apply(report Report, now time.Time) ([]MonitorEvent, error) {
	if s.lock == nil {
		return nil, errors.New("monitor store is closed")
	}
	// Work on a copy so a failed write cannot advance the in-memory cursor.
	encoded, err := json.Marshal(s.state)
	if err != nil {
		return nil, err
	}
	var next MonitorState
	if err := json.Unmarshal(encoded, &next); err != nil {
		return nil, err
	}
	if report.Scope.Context != next.Spec.Context || report.Scope.Namespace != next.Spec.Namespace {
		return nil, errors.New("collected scope differs from pinned monitor scope")
	}
	now = now.UTC()
	coverage := report.Coverage
	coverage.Reasons = append([]string(nil), coverage.Reasons...)
	window, _ := time.ParseDuration(next.Spec.Since)
	lastPoll, _ := time.Parse(time.RFC3339Nano, next.LastPollAt)
	if !lastPoll.IsZero() && (now.Before(lastPoll) || now.Sub(lastPoll) > window) {
		coverage.Status = "partial"
		coverage.Reasons = append(coverage.Reasons, "polling gap or clock change; quiet-period verification restarted")
	}
	if coverage.Status == "" || report.Truncated || len(report.CollectionErrors) > 0 {
		if coverage.Status != "unavailable" {
			coverage.Status = "partial"
		}
	}
	sort.Strings(coverage.Reasons)
	status := "unknown"
	if report.Summary.Errors > 0 || report.Summary.Warnings > 0 || report.Summary.UnhealthyPods > 0 {
		status = "degraded"
	} else if coverage.Status == "complete" && report.Summary.Status == "healthy" {
		status = "healthy"
	}
	var changes []MonitorEvent
	emit := func(kind string, incident *Incident) {
		next.Sequence++
		event := MonitorEvent{SchemaVersion: MonitorSchemaVersion, Cursor: next.cursor(next.Sequence), Type: kind, ObservedAt: timestamp(now)}
		if incident != nil {
			copy := *incident
			event.Incident = &copy
		} else {
			copy := coverage
			event.Coverage, event.Status = &copy, status
		}
		changes = append(changes, event)
		next.Events = append(next.Events, event)
	}
	if next.LastPollAt == "" || !reflect.DeepEqual(next.Coverage, coverage) || next.Status != status {
		emit("monitor.coverage", nil)
	}
	seen := map[string]bool{}
	for _, issue := range report.Issues {
		if issue.ID == "" {
			continue
		}
		keyData := next.Spec.Context + "\x00" + next.Spec.Namespace + "\x00" + issue.Workload + "\x00" + issue.ID
		hash := sha256.Sum256([]byte(keyData))
		id := hex.EncodeToString(hash[:16])
		seen[id] = true
		old, exists := next.Incidents[id]
		lastSeen, validTime := parseMonitorTime(issue.LastSeen)
		oldSeen, _ := parseMonitorTime(old.LastSeen)
		pods := append([]string(nil), issue.Pods...)
		sort.Strings(pods)
		incident := old
		if !exists {
			incident = Incident{ID: id, IssueID: issue.ID, Workload: issue.Workload, Service: issue.Service,
				Kind: issue.Kind, Summary: Redact(issue.Summary), Status: "open", Occurrence: 1,
				FirstSeen: issue.FirstSeen, OpenedAt: timestamp(now)}
		}
		kind := ""
		if !exists {
			kind = "incident.opened"
		} else if old.Status == "resolved" {
			resolved, _ := parseMonitorTime(old.ResolvedAt)
			if !validTime || !lastSeen.After(resolved) {
				continue // old retained evidence must not reopen a recovered incident
			}
			incident.Status, incident.Occurrence, incident.OpenedAt, incident.ResolvedAt = "open", old.Occurrence+1, timestamp(now), ""
			kind = "incident.reopened"
		} else if lastSeen.After(oldSeen) || issue.Severity != old.Severity || issue.Summary != old.Summary || !reflect.DeepEqual(pods, old.Pods) {
			kind = "incident.updated"
			if (issue.Count > old.WindowCount && lastSeen.After(oldSeen)) || (old.Severity != "error" && issue.Severity == "error") {
				kind = "incident.worsening"
			}
		}
		incident.Service, incident.Kind, incident.Summary = issue.Service, issue.Kind, Redact(issue.Summary)
		incident.Severity, incident.WindowCount, incident.Pods, incident.TraceID = issue.Severity, issue.Count, pods, Redact(issue.TraceID)
		incident.QuietSince = ""
		if !exists || lastSeen.After(oldSeen) {
			incident.LastSeen = issue.LastSeen
		}
		next.Incidents[id] = incident
		if kind != "" {
			emit(kind, &incident)
		}
	}
	workloads := map[string]bool{}
	for _, workload := range report.Scope.Workloads {
		workloads[workload] = true
	}
	ids := make([]string, 0, len(next.Incidents))
	for id := range next.Incidents {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		incident := next.Incidents[id]
		if incident.Status != "open" || seen[id] {
			continue
		}
		// Absence under a partial scan, unhealthy pods or missing workload is
		// not recovery. Require a continuously observed quiet period.
		workloadPresent := workloads[incident.Workload]
		if coverage.Status != "complete" || report.Kind != "DiagnosticReport" || report.Summary.UnhealthyPods > 0 || (!workloadPresent && report.Coverage.ExpectedStreams != 0) {
			incident.QuietSince = ""
		} else if incident.QuietSince == "" {
			incident.QuietSince = timestamp(now)
		} else if quiet, ok := parseMonitorTime(incident.QuietSince); ok && now.Sub(quiet) >= time.Duration(next.Spec.ResolveAfterSeconds)*time.Second {
			incident.Status, incident.ResolvedAt = "resolved", timestamp(now)
			emit("incident.resolved", &incident)
		}
		next.Incidents[id] = incident
	}
	next.LastPollAt, next.Coverage, next.Status = timestamp(now), coverage, status
	if len(next.Events) > monitorEventRetention {
		next.Events = append([]MonitorEvent(nil), next.Events[len(next.Events)-monitorEventRetention:]...)
	}
	if err := saveMonitorState(s.path, next); err != nil {
		return nil, err
	}
	s.state = next
	return changes, nil
}

func parseMonitorTime(value string) (time.Time, bool) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	return parsed, err == nil
}

func saveMonitorState(path string, state MonitorState) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".tailg-monitor-*")
	if err != nil {
		return err
	}
	temporary := file.Name()
	defer os.Remove(temporary)
	if err := json.NewEncoder(file).Encode(state); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}
