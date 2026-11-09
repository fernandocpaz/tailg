package core

import (
	"regexp"
	"sort"
	"strings"
	"sync"
)

var ansiPattern = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func StripANSI(value string) string { return ansiPattern.ReplaceAllString(value, "") }

// FilterState owns the bounded live log buffer and any pinned history from a
// complete-history search.
// Records are kept here instead of just their rendered text so filters can
// inspect properties that are not displayed in the TUI.
type FilterState struct {
	mu       sync.RWMutex
	maxLines int

	allRecords     recordQueue
	visibleRecords recordList

	filterText  string
	filterQuery *LogQuery
	filterErr   error

	// externalHistory identifies pinned rows and is non-nil during a search result.
	externalHistory   map[uint64]struct{}
	externalRecords   recordList
	externalLiveCount int

	matchesOnly bool
	matchCount  int

	// nextLiveID is the ID assigned to the next row appended to the live
	// stream. searchWatermark lets a history result include rows which arrived
	// while that result was being fetched.
	nextLiveID      uint64
	searchWatermark uint64
}

func NewFilterState(maxLines int) *FilterState {
	return &FilterState{maxLines: maxLines, nextLiveID: 1}
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
// The return count includes visible arrivals even if later rows in the same
// batch evict them. The caller's records are left intact.
func (s *FilterState) AppendRecords(records ...LogRecord) int {
	if len(records) == 0 {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	addedVisible := 0
	for _, record := range records {
		record.ID = s.allocateIDLocked()
		if s.maxLines > 0 && s.allRecords.count == s.maxLines {
			oldest := s.allRecords.pop()
			if s.externalHistory == nil {
				s.removeVisibleLocked(oldest, false)
			}
		}
		s.allRecords.push(record)

		if s.externalHistory != nil {
			if s.maxLines > 0 && s.externalLiveCount == s.maxLines {
				s.evictExternalLocked()
			}
			s.externalRecords.tail.push(record)
			s.externalLiveCount++
		}
		if s.appendVisibleLocked(record, false) {
			addedVisible++
		}
	}
	return addedVisible
}

func (s *FilterState) SetFilter(text string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.filterText = text
	s.filterQuery, s.filterErr = nil, nil
	if strings.TrimSpace(text) != "" {
		s.filterQuery, s.filterErr = CompileLogQuery(text)
	}
	s.externalRecords = recordList{}
	s.externalHistory = nil
	s.externalLiveCount = 0
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
	candidates := make(map[recordOccurrence][]candidate, s.allRecords.count)
	candidateIDs := make(map[recordOccurrence]map[uint64]struct{}, s.allRecords.count)
	usedCandidateIDs := make(map[uint64]struct{}, s.allRecords.count)
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
	for _, record := range s.allRecords.view(nil).All {
		if record.ID < s.searchWatermark {
			addCandidate(record, false)
		}
	}
	if s.externalHistory != nil {
		for _, record := range s.externalRecords.view().All {
			if _, ok := s.externalHistory[record.ID]; ok {
				addCandidate(record, false)
			}
		}
	}
	for _, record := range s.allRecords.view(nil).All {
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
	for _, record := range s.allRecords.view(nil).All {
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

	// Install the already contiguous history prefix separately. Any pinned rows
	// interleaved with live arrivals remain in the queue until eviction reaches them.
	prefixEnd := 0
	for _, record := range merged {
		if _, pinned := newHistory[record.ID]; !pinned {
			break
		}
		prefixEnd++
	}
	s.externalRecords = recordList{
		prefix: merged[:prefixEnd:prefixEnd],
		tail:   recordQueue{rows: merged[prefixEnd:cap(merged)], count: len(merged) - prefixEnd},
	}
	s.externalHistory = newHistory
	s.externalLiveCount = len(merged) - len(newHistory)
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
	if s.matchCount > 0 {
		for index, record := range s.visibleRecords.view().All {
			if s.queryMatchesLocked(record) {
				return index
			}
		}
	}
	return -1
}

func (s *FilterState) MatchCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.matchCount
}

// RenderRecords holds a read lock while a synchronous TUI frame is constructed,
// avoiding a copy of the ring. The callback must not mutate or retain the view,
// or invoke methods that lock this FilterState.
func (s *FilterState) RenderRecords(render func(LogRecordView) []string) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return render(s.visibleRecords.view())
}

// VisibleCount returns the displayed row count without allocating a []string.
func (s *FilterState) VisibleCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.visibleRecords.prefix) + s.visibleRecords.tail.count
}

// Records returns a snapshot of the currently visible records.
func (s *FilterState) Records() []LogRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.visibleRecords.view().clone()
}

// AllRecords returns the latest bounded live records, independent of filter
// and complete-history context.
func (s *FilterState) AllRecords() []LogRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.allRecords.view(nil).clone()
}

// SelectedBlock uses unfiltered history/live records so matches-only mode does
// not hide the stack frames attached to the selected error.
func (s *FilterState) SelectedBlock(record LogRecord) []LogEvent {
	s.mu.RLock()
	defer s.mu.RUnlock()
	source := s.sourceLocked()
	events := make([]LogEvent, 0, source.Len())
	for _, candidate := range source.All {
		events = append(events, candidate.Event)
	}
	if block, ok := LogBlock(events, record.Event); ok {
		return block
	}
	return []LogEvent{record.Event}
}

func (s *FilterState) Lines() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.visibleRecords.view().texts()
}

func (s *FilterState) AllLines() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.allRecords.view(nil).texts()
}

func (s *FilterState) BufferUsage() (int, int) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.allRecords.count, s.maxLines
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
	visible := s.visibleRecords.view()
	if index < 0 || index >= visible.Len() {
		return LogRecord{}, false
	}
	return visible.at(index), true
}

func (s *FilterState) sourceLocked() LogRecordView {
	if s.externalHistory != nil {
		return s.externalRecords.view()
	}
	return s.allRecords.view(nil)
}

// History and in-flight live arrivals can be interleaved by timestamp. Move
// pinned rows at the front to a permanent prefix until the oldest live row
// can be evicted. Each pinned row is moved at most once per search result.
func (s *FilterState) evictExternalLocked() {
	for {
		oldest := s.externalRecords.tail.pop()
		_, pinned := s.externalHistory[oldest.ID]
		s.removeVisibleLocked(oldest, pinned)
		if !pinned {
			s.externalLiveCount--
			return
		}
		s.externalRecords.prefix = append(s.externalRecords.prefix, oldest)
	}
}

func (s *FilterState) removeVisibleLocked(record LogRecord, pinned bool) {
	tail := &s.visibleRecords.tail
	if tail.count == 0 || tail.rows[tail.head].ID != record.ID {
		return
	}
	tail.pop()
	if pinned {
		s.visibleRecords.prefix = append(s.visibleRecords.prefix, record)
	} else if s.filterQuery != nil && s.queryMatchesLocked(record) {
		s.matchCount--
	}
}

func (s *FilterState) refreshLocked() {
	source := s.sourceLocked()
	s.visibleRecords.reset()
	s.matchCount = 0
	for index, record := range source.All {
		s.appendVisibleLocked(record, index < len(source[0]))
	}
}

func (s *FilterState) appendVisibleLocked(record LogRecord, pinned bool) bool {
	matched := s.queryMatchesLocked(record)
	if !matched && (s.filterErr != nil || s.externalHistory == nil || s.matchesOnly) {
		return false
	}
	if matched && s.filterQuery != nil {
		s.matchCount++
	}
	if pinned {
		s.visibleRecords.prefix = append(s.visibleRecords.prefix, record)
	} else {
		s.visibleRecords.tail.push(record)
	}
	return true
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
