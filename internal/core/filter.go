package core

import (
	"regexp"
	"sort"
	"strings"
	"sync"
)

var ansiPattern = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func StripANSI(value string) string { return ansiPattern.ReplaceAllString(value, "") }

// FilterState owns the bounded live log buffer and, while a complete-history
// search is active, a pinned history prefix followed by the bounded live tail.
// Records are kept here instead of just their rendered text so filters can
// inspect properties that are not displayed in the TUI.
type FilterState struct {
	mu       sync.RWMutex
	maxLines int

	allRecords     []LogRecord
	visibleRecords []LogRecord

	filterText  string
	filterQuery *LogQuery
	filterErr   error

	// externalRecords is non-nil while a complete-history result is active.
	externalFilter  string
	externalRecords []LogRecord
	externalHistory map[uint64]struct{}

	matchesOnly bool
	matchIndex  int
	matchCount  int

	// nextLiveID is the ID assigned to the next row appended to the live
	// stream. searchWatermark lets a history result include rows which arrived
	// while that result was being fetched.
	nextLiveID      uint64
	searchWatermark uint64
}

func NewFilterState(maxLines int) *FilterState {
	return &FilterState{maxLines: maxLines, matchIndex: -1, nextLiveID: 1}
}

// Append is the compatibility API for callers which already have rendered
// lines. The original line is retained as the raw event message as well.
func (s *FilterState) Append(lines ...string) int {
	if len(lines) == 0 {
		return 0
	}
	records := make([]LogRecord, len(lines))
	for i, line := range lines {
		event := LogEvent{Message: line}
		records[i] = LogRecord{Event: event, Text: line, Fields: ParseLogFields(event)}
	}
	return s.AppendRecords(records...)
}

// AppendRecords appends live rows and assigns each one a monotonic local ID.
// The caller's records are copied before IDs are assigned.
func (s *FilterState) AppendRecords(records ...LogRecord) int {
	if len(records) == 0 {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	appended := make([]LogRecord, len(records))
	for i, record := range records {
		record.ID = s.allocateIDLocked()
		appended[i] = record
	}
	s.allRecords = append(s.allRecords, appended...)
	trimmedLive := s.trimLiveLocked()

	addedVisible := 0
	if s.externalRecords != nil {
		s.externalRecords = append(s.externalRecords, appended...)
		trimmedExternal := s.trimExternalLocked()
		if trimmedExternal || trimmedLive {
			for _, record := range appended {
				if s.filterErr == nil && ((s.externalRecords != nil && !s.matchesOnly) || s.recordVisibleLocked(record)) {
					addedVisible++
				}
			}
			s.refreshLocked()
		} else {
			for _, record := range appended {
				if s.appendVisibleLocked(record) {
					addedVisible++
				}
			}
		}
	} else if trimmedLive {
		s.refreshLocked()
		for _, record := range appended {
			if s.recordVisibleLocked(record) {
				addedVisible++
			}
		}
	} else {
		for _, record := range appended {
			if s.appendVisibleLocked(record) {
				addedVisible++
			}
		}
	}
	return addedVisible
}

func (s *FilterState) SetFilter(text string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.filterText = text
	s.filterQuery, s.filterErr = CompileLogQuery(text)
	s.externalFilter = ""
	s.externalRecords = nil
	s.externalHistory = nil
	s.searchWatermark = s.nextLiveID
	s.refreshLocked()
}

// SetSearchResults preserves the original text-only search API. History rows
// have no local identity; live rows are represented by AppendRecords.
func (s *FilterState) SetSearchResults(text string, lines []string) bool {
	records := make([]LogRecord, len(lines))
	for i, line := range lines {
		event := LogEvent{Message: line}
		records[i] = LogRecord{Event: event, Text: line, Fields: ParseLogFields(event)}
	}
	return s.SetSearchRecords(text, records)
}

// SetSearchRecords installs a complete-history result. Rows which arrived
// after SetFilter are reconciled against history by occurrence counts, rather
// than by text alone, so identical visible messages from different traces are
// retained.
func (s *FilterState) SetSearchRecords(text string, records []LogRecord) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	normalized := normalizeFilter(text)
	if normalized == "" || normalized != normalizeFilter(s.filterText) {
		return false
	}

	// Keep identities stable when a history request is repeated or overlaps the
	// live tail. Prefer an existing pre-watermark live row, then an existing
	// history row, then a late live row. A genuinely historical row receives a
	// fresh local ID from the same monotonic sequence as live rows.
	type candidate struct {
		id   uint64
		late bool
	}
	candidates := make(map[recordOccurrence][]candidate, len(s.allRecords))
	candidateIDs := make(map[recordOccurrence]map[uint64]struct{}, len(s.allRecords))
	usedCandidateIDs := make(map[uint64]struct{}, len(s.allRecords))
	addCandidate := func(record LogRecord, late bool) {
		if record.ID == 0 {
			return
		}
		key := occurrenceOf(record)
		if candidateIDs[key] == nil {
			candidateIDs[key] = make(map[uint64]struct{})
		}
		if _, exists := candidateIDs[key][record.ID]; exists {
			// A row that was pinned by an earlier search can also be present in
			// the late live tail. Keep the history candidate's position, but
			// remember that its live counterpart must be consumed below.
			if late {
				for i := range candidates[key] {
					if candidates[key][i].id == record.ID {
						candidates[key][i].late = true
						break
					}
				}
			}
			return
		}
		if _, exists := usedCandidateIDs[record.ID]; exists {
			return
		}
		candidateIDs[key][record.ID] = struct{}{}
		usedCandidateIDs[record.ID] = struct{}{}
		candidates[key] = append(candidates[key], candidate{record.ID, late})
	}
	for _, record := range s.allRecords {
		if record.ID < s.searchWatermark {
			addCandidate(record, false)
		}
	}
	if s.externalRecords != nil {
		for _, record := range s.externalRecords {
			if _, ok := s.externalHistory[record.ID]; ok {
				addCandidate(record, false)
			}
		}
	}
	for _, record := range s.allRecords {
		if record.ID >= s.searchWatermark {
			addCandidate(record, true)
		}
	}

	history := make([]LogRecord, len(records))
	newHistory := make(map[uint64]struct{}, len(records))
	lateMatched := make(map[uint64]struct{})
	historyCounts := make(map[recordOccurrence]int, len(records))
	for i, record := range records {
		key := occurrenceOf(record)
		historyCounts[key]++
		queue := candidates[key]
		if len(queue) > 0 {
			chosen := queue[0]
			candidates[key] = queue[1:]
			record.ID = chosen.id
			if chosen.late {
				lateMatched[chosen.id] = struct{}{}
			}
			historyCounts[key]--
		} else {
			record.ID = s.allocateIDLocked()
		}
		newHistory[record.ID] = struct{}{}
		history[i] = record
	}
	merged := make([]LogRecord, len(history))
	copy(merged, history)

	// The history command commonly overlaps the live tail. Consume one history
	// occurrence for each matching late live row; an excess late occurrence is
	// appended and remains visible. This is deliberately a full occurrence key,
	// not a rendered-text key.
	for _, record := range s.allRecords {
		if record.ID < s.searchWatermark {
			continue
		}
		key := occurrenceOf(record)
		if _, matched := lateMatched[record.ID]; matched {
			continue
		}
		if count := historyCounts[key]; count > 0 {
			historyCounts[key] = count - 1
			continue
		}
		merged = append(merged, record)
	}

	// Kube history rows normally have timestamps. Preserve source order for the
	// legacy text API, whose history rows have no event timestamps, while making
	// structured history deterministic and chronological. Unknown timestamps
	// sort after timestamped rows.
	if recordsHaveAnyTimestamp(history) {
		sort.SliceStable(merged, func(i, j int) bool {
			left, right := merged[i].Event.ObservedAt, merged[j].Event.ObservedAt
			if left.IsZero() != right.IsZero() {
				return !left.IsZero()
			}
			if left.Equal(right) {
				return false
			}
			return left.Before(right)
		})
	}

	s.externalFilter = normalized
	s.externalRecords = merged
	s.externalHistory = newHistory
	s.refreshLocked()
	return true
}

// allocateIDLocked returns the next nonzero local row ID. Keeping this in one
// place also makes a zero-value FilterState safe to use.
func (s *FilterState) allocateIDLocked() uint64 {
	if s.nextLiveID == 0 {
		s.nextLiveID = 1
	}
	id := s.nextLiveID
	s.nextLiveID++
	if s.nextLiveID == 0 { // ID zero is reserved, including on uint64 wraparound.
		s.nextLiveID = 1
	}
	return id
}

func (s *FilterState) SetMatchesOnly(enabled bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.matchesOnly == enabled {
		return false
	}
	s.matchesOnly = enabled
	s.refreshLocked()
	return true
}

func (s *FilterState) ToggleMatchesOnly() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.matchesOnly = !s.matchesOnly
	s.refreshLocked()
	return s.matchesOnly
}

func (s *FilterState) MatchesOnly() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.matchesOnly
}

func (s *FilterState) Filter() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.filterText
}

func (s *FilterState) FilterError() error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.filterErr
}

func (s *FilterState) Highlight() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.filterQuery == nil || s.filterErr != nil {
		return ""
	}
	return s.filterQuery.Highlight()
}

func (s *FilterState) MatchIndex() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.matchIndex
}

func (s *FilterState) MatchCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.matchCount
}

// Records returns the currently visible immutable record snapshots.
func (s *FilterState) Records() []LogRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneRecords(s.visibleRecords)
}

// AllRecords returns the latest bounded live records, independent of filter
// and complete-history context.
func (s *FilterState) AllRecords() []LogRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneRecords(s.allRecords)
}

func (s *FilterState) Lines() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return recordTexts(s.visibleRecords)
}

func (s *FilterState) AllLines() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return recordTexts(s.allRecords)
}

func (s *FilterState) BufferUsage() (int, int) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.allRecords), s.maxLines
}

func (s *FilterState) Selected(index int) string {
	record, ok := s.SelectedRecord(index)
	if !ok {
		return ""
	}
	return StripANSI(record.Text)
}

func (s *FilterState) SelectedRecord(index int) (LogRecord, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if index < 0 || index >= len(s.visibleRecords) {
		return LogRecord{}, false
	}
	return s.visibleRecords[index], true
}

func (s *FilterState) trimLiveLocked() bool {
	if s.maxLines <= 0 || len(s.allRecords) <= s.maxLines {
		return false
	}
	s.allRecords = append([]LogRecord(nil), s.allRecords[len(s.allRecords)-s.maxLines:]...)
	return true
}

func (s *FilterState) trimExternalLocked() bool {
	if s.maxLines <= 0 {
		return false
	}
	liveCount := 0
	for _, record := range s.externalRecords {
		if _, pinned := s.externalHistory[record.ID]; !pinned {
			liveCount++
		}
	}
	if liveCount <= s.maxLines {
		return false
	}
	keepFrom := liveCount - s.maxLines
	bounded := make([]LogRecord, 0, len(s.externalRecords)-keepFrom)
	for _, record := range s.externalRecords {
		if _, pinned := s.externalHistory[record.ID]; pinned {
			bounded = append(bounded, record)
			continue
		}
		if keepFrom > 0 {
			keepFrom--
			continue
		}
		bounded = append(bounded, record)
	}
	s.externalRecords = bounded
	return true
}

func (s *FilterState) refreshLocked() {
	source := s.allRecords
	if s.externalRecords != nil {
		source = s.externalRecords
	}
	needle := normalizeFilter(s.filterText)
	s.visibleRecords = s.visibleRecords[:0]
	s.matchIndex = -1
	s.matchCount = 0
	for _, record := range source {
		matched := s.queryMatchesLocked(record)
		if matched && needle != "" {
			s.matchCount++
			if s.matchIndex < 0 && needle != "" {
				s.matchIndex = len(s.visibleRecords)
			}
		}
		if s.filterErr != nil {
			continue
		}
		if s.externalRecords != nil && !s.matchesOnly {
			s.visibleRecords = append(s.visibleRecords, record)
		} else if matched {
			s.visibleRecords = append(s.visibleRecords, record)
		}
	}
	if needle == "" || s.filterErr != nil {
		s.matchIndex = -1
	}
}

func (s *FilterState) appendVisibleLocked(record LogRecord) bool {
	needle := normalizeFilter(s.filterText)
	matched := s.queryMatchesLocked(record)
	if matched && needle != "" {
		s.matchCount++
	}
	if s.filterErr != nil {
		return false
	}
	visible := matched
	if s.externalRecords != nil && !s.matchesOnly {
		visible = true
	}
	if !visible {
		return false
	}
	if s.matchIndex < 0 && matched && needle != "" {
		s.matchIndex = len(s.visibleRecords)
	}
	s.visibleRecords = append(s.visibleRecords, record)
	return true
}

func (s *FilterState) recordVisibleLocked(record LogRecord) bool {
	return s.queryMatchesLocked(record)
}

func (s *FilterState) queryMatchesLocked(record LogRecord) bool {
	if s.filterErr != nil {
		return false
	}
	if s.filterQuery == nil {
		return true
	}
	return s.filterQuery.Matches(record)
}

type recordOccurrence struct {
	pod, container, message, text string
	observed                      int64
}

func occurrenceOf(record LogRecord) recordOccurrence {
	observed := int64(0)
	if !record.Event.ObservedAt.IsZero() {
		observed = record.Event.ObservedAt.UnixNano()
	}
	return recordOccurrence{record.Event.Pod, record.Event.Container, record.Event.Message, record.Text, observed}
}

func normalizeFilter(text string) string { return strings.ToLower(strings.TrimSpace(text)) }

func cloneRecords(records []LogRecord) []LogRecord {
	return append([]LogRecord(nil), records...)
}

func recordTexts(records []LogRecord) []string {
	lines := make([]string, len(records))
	for i, record := range records {
		lines[i] = record.Text
	}
	return lines
}

func recordsHaveAnyTimestamp(records []LogRecord) bool {
	for _, record := range records {
		if !record.Event.ObservedAt.IsZero() {
			return true
		}
	}
	return false
}

// SearchRecordsFromFirstMatch is the structured counterpart to
// SearchLinesFromFirstMatch. It compiles the query once and preserves all
// record metadata in the returned snapshots.
func SearchRecordsFromFirstMatch(records []LogRecord, searchText string, beforeRecords, maxRecords int) ([]LogRecord, error) {
	query, err := CompileLogQuery(searchText)
	if err != nil {
		return nil, err
	}
	if normalizeFilter(searchText) == "" {
		return nil, nil
	}
	first := -1
	for index, record := range records {
		if query.Matches(record) {
			first = index
			break
		}
	}
	if first < 0 {
		return nil, nil
	}
	start := first - max(0, beforeRecords)
	if start < 0 {
		start = 0
	}
	end := len(records)
	if maxRecords >= 0 && start+max(1, maxRecords) < end {
		end = start + max(1, maxRecords)
	}
	return cloneRecords(records[start:end]), nil
}

func SearchLinesFromFirstMatch(lines []string, searchText string, beforeLines, maxLines int) []string {
	records := make([]LogRecord, len(lines))
	for i, line := range lines {
		event := LogEvent{Message: line}
		records[i] = LogRecord{Event: event, Text: line, Fields: ParseLogFields(event)}
	}
	result, err := SearchRecordsFromFirstMatch(records, searchText, beforeLines, maxLines)
	if err != nil {
		return nil
	}
	return recordTexts(result)
}
