package core

import (
	"regexp"
	"strings"
	"sync"
)

var ansiPattern = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func StripANSI(value string) string { return ansiPattern.ReplaceAllString(value, "") }

// FilterState owns the in-memory log history and the complete-history context
// loaded after a filter reaches beyond the initial tail buffer.
type FilterState struct {
	mu             sync.RWMutex
	maxLines       int
	allLines       []string
	visibleLines   []string
	filterText     string
	externalFilter string
	externalLines  []string
	matchesOnly    bool
	matchIndex     int
}

func NewFilterState(maxLines int) *FilterState {
	return &FilterState{maxLines: maxLines, matchIndex: -1}
}

func (s *FilterState) Append(lines ...string) int {
	if len(lines) == 0 {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	addedVisible := 0
	for _, line := range lines {
		if s.lineVisibleLocked(line) {
			s.visibleLines = append(s.visibleLines, line)
			addedVisible++
		}
	}
	s.allLines = append(s.allLines, lines...)
	trimmedLive := false
	if s.maxLines > 0 && len(s.allLines) > s.maxLines {
		s.allLines = append([]string(nil), s.allLines[len(s.allLines)-s.maxLines:]...)
		trimmedLive = true
	}
	trimmedExternal := false
	if s.externalLines != nil {
		s.externalLines = append(s.externalLines, lines...)
		if s.maxLines > 0 && len(s.externalLines) > s.maxLines {
			s.externalLines = append([]string(nil), s.externalLines[len(s.externalLines)-s.maxLines:]...)
			trimmedExternal = true
		}
	}
	if trimmedExternal || (trimmedLive && s.externalLines == nil) {
		s.refreshLocked()
	}
	return addedVisible
}

func (s *FilterState) SetFilter(text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.filterText = text
	s.externalFilter = ""
	s.externalLines = nil
	s.matchIndex = -1
	s.refreshLocked()
}

func (s *FilterState) SetSearchResults(text string, lines []string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	normalized := strings.ToLower(strings.TrimSpace(text))
	if normalized == "" || normalized != strings.ToLower(strings.TrimSpace(s.filterText)) {
		return false
	}
	s.externalFilter = normalized
	s.externalLines = append([]string(nil), lines...)
	if s.maxLines > 0 && len(s.externalLines) > s.maxLines {
		// Complete-history results begin at the earliest match. Preserve that
		// context when applying the steady-state memory limit.
		s.externalLines = append([]string(nil), s.externalLines[:s.maxLines]...)
	}
	s.refreshLocked()
	return true
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

func (s *FilterState) MatchIndex() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.matchIndex
}

func (s *FilterState) Lines() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]string(nil), s.visibleLines...)
}

func (s *FilterState) AllLines() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]string(nil), s.allLines...)
}

func (s *FilterState) Selected(index int) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if index < 0 || index >= len(s.visibleLines) {
		return ""
	}
	return StripANSI(s.visibleLines[index])
}

func (s *FilterState) refreshLocked() {
	needle := strings.ToLower(strings.TrimSpace(s.filterText))
	source := s.allLines
	if s.externalLines != nil {
		source = s.externalLines
	}
	s.visibleLines = s.visibleLines[:0]
	for _, line := range source {
		if needle == "" || (s.externalFilter != "" && !s.matchesOnly) || strings.Contains(strings.ToLower(StripANSI(line)), needle) {
			s.visibleLines = append(s.visibleLines, line)
		}
	}
	s.matchIndex = -1
	if needle != "" {
		for index, line := range s.visibleLines {
			if strings.Contains(strings.ToLower(StripANSI(line)), needle) {
				s.matchIndex = index
				break
			}
		}
	}
}

func (s *FilterState) lineVisibleLocked(line string) bool {
	if s.externalFilter != "" && !s.matchesOnly {
		return true
	}
	needle := strings.ToLower(strings.TrimSpace(s.filterText))
	return needle == "" || strings.Contains(strings.ToLower(StripANSI(line)), needle)
}

func SearchLinesFromFirstMatch(lines []string, searchText string, beforeLines, maxLines int) []string {
	needle := strings.ToLower(strings.TrimSpace(searchText))
	if needle == "" {
		return nil
	}
	first := -1
	for index, line := range lines {
		if strings.Contains(strings.ToLower(StripANSI(line)), needle) {
			first = index
			break
		}
	}
	if first < 0 {
		return nil
	}
	start := first - max(0, beforeLines)
	if start < 0 {
		start = 0
	}
	end := len(lines)
	if maxLines >= 0 && start+max(1, maxLines) < end {
		end = start + max(1, maxLines)
	}
	return append([]string(nil), lines[start:end]...)
}
