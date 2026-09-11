package core

import (
	"testing"
	"time"
)

func TestFilterStateStructuredRecordsAndLiveBounds(t *testing.T) {
	state := NewFilterState(2)
	first := LogRecord{Event: LogEvent{Pod: "api", Message: "one"}, Text: "one"}
	second := LogRecord{Event: LogEvent{Pod: "api", Message: "two"}, Text: "two"}
	third := LogRecord{Event: LogEvent{Pod: "api", Message: "three"}, Text: "three"}
	state.AppendRecords(first, second, third)

	got := state.AllRecords()
	if len(got) != 2 || got[0].Text != "two" || got[1].Text != "three" {
		t.Fatalf("bounded records = %#v", got)
	}
	if got[0].ID == 0 || got[1].ID == 0 || got[1].ID <= got[0].ID {
		t.Fatalf("live IDs = %d, %d", got[0].ID, got[1].ID)
	}
	got[0].Text = "mutated"
	if state.AllRecords()[0].Text != "two" {
		t.Fatal("AllRecords did not return an immutable snapshot")
	}
	if selected, ok := state.SelectedRecord(1); !ok || selected.Text != "three" {
		t.Fatalf("selected record = %#v, %t", selected, ok)
	}
}

func TestFilterStateStructuredQueryMatchesHiddenMetadata(t *testing.T) {
	state := NewFilterState(10)
	state.AppendRecords(
		LogRecord{
			Event:  LogEvent{Pod: "checkout-1", Message: "display text"},
			Text:   "display text",
			Fields: LogFields{Service: "payments", TraceID: "0123456789abcdef0123456789abcdef", StatusCode: 503},
		},
		LogRecord{Event: LogEvent{Pod: "checkout-2", Message: "other"}, Text: "other", Fields: LogFields{Service: "catalog"}},
	)
	state.SetFilter("service:payments status:>=500")
	if got := state.Lines(); len(got) != 1 || got[0] != "display text" {
		t.Fatalf("hidden metadata filter = %#v", got)
	}
	if state.Highlight() != "" || state.MatchCount() != 1 {
		t.Fatalf("structured filter state: highlight=%q matches=%d", state.Highlight(), state.MatchCount())
	}
}

func TestFilterStateSearchReconcilesOccurrencesAndClear(t *testing.T) {
	state := NewFilterState(10)
	state.AppendRecords(LogRecord{Event: LogEvent{Pod: "old", Message: "needle"}, Text: "needle"})
	state.SetFilter("needle")
	at := time.Unix(100, 0)
	state.AppendRecords(
		LogRecord{Event: LogEvent{Pod: "same", Message: "needle", ObservedAt: at}, Text: "needle"},
		LogRecord{Event: LogEvent{Pod: "same", Message: "needle", ObservedAt: at}, Text: "needle"},
		LogRecord{Event: LogEvent{Pod: "other", Message: "needle", ObservedAt: at}, Text: "needle"},
	)
	live := state.AllRecords()
	history := []LogRecord{{Event: LogEvent{Pod: "same", Message: "needle", ObservedAt: at}, Text: "needle"}}
	if !state.SetSearchRecords("needle", history) {
		t.Fatal("search result rejected")
	}
	// One of the two exact same occurrences is consumed by history; the second
	// one and the occurrence from the other pod must remain.
	if got := state.MatchCount(); got != 3 {
		t.Fatalf("match count = %d, want 3", got)
	}
	if got := len(state.Lines()); got != 3 {
		t.Fatalf("reconciled rows = %#v", got)
	}
	rows := state.Records()
	if rows[0].ID != live[1].ID || rows[0].ID == 0 {
		t.Fatalf("matching history did not preserve live ID: history=%d live=%d", rows[0].ID, live[1].ID)
	}
	if !state.SetSearchRecords("needle", history) || len(state.Records()) != 3 {
		t.Fatalf("repeated history completion duplicated a live row: %#v", state.Records())
	}
	if state.SetSearchRecords("stale", history) {
		t.Fatal("stale search result accepted")
	}

	state.SetFilter("")
	if got := state.AllLines(); len(got) != 4 || got[0] != "needle" {
		t.Fatalf("cleared filter = %#v", got)
	}
}

func TestFilterStateSearchRepeatKeepsPinnedHistoryAndLateDuplicate(t *testing.T) {
	state := NewFilterState(10)
	state.SetFilter("needle")
	history := LogRecord{Event: LogEvent{Pod: "api", Message: "needle"}, Text: "needle"}
	if !state.SetSearchRecords("needle", []LogRecord{history}) {
		t.Fatal("search result rejected")
	}
	historyID := state.Records()[0].ID

	state.AppendRecords(history)
	lateID := state.AllRecords()[0].ID
	if !state.SetSearchRecords("needle", []LogRecord{history}) {
		t.Fatal("repeated search result rejected")
	}
	rows := state.Records()
	if len(rows) != 2 {
		t.Fatalf("history and late duplicate rows = %#v, want 2 rows", rows)
	}
	if rows[0].ID != historyID || rows[1].ID != lateID {
		t.Fatalf("history IDs changed or late row was deduplicated: got [%d %d], want [%d %d]", rows[0].ID, rows[1].ID, historyID, lateID)
	}
}

func TestFilterStateHistoryIDsAreUniqueWhenRowsReuseLiveOccurrences(t *testing.T) {
	state := NewFilterState(10)
	firstAt := time.Unix(100, 0)
	secondAt := time.Unix(200, 0)
	state.AppendRecords(
		LogRecord{Event: LogEvent{Pod: "api", Message: "needle first", ObservedAt: firstAt}, Text: "needle first"},
		LogRecord{Event: LogEvent{Pod: "api", Message: "needle second", ObservedAt: secondAt}, Text: "needle second"},
	)
	live := state.AllRecords()
	state.SetFilter("needle")
	if !state.SetSearchRecords("needle", []LogRecord{
		{Event: LogEvent{Pod: "api", Message: "needle second", ObservedAt: secondAt}, Text: "needle second"},
		{Event: LogEvent{Pod: "api", Message: "needle first", ObservedAt: firstAt}, Text: "needle first"},
		{Event: LogEvent{Pod: "api", Message: "needle historical", ObservedAt: time.Unix(300, 0)}, Text: "needle historical"},
	}) {
		t.Fatal("search result rejected")
	}
	rows := state.Records()
	if len(rows) != 3 {
		t.Fatalf("history rows = %#v, want 3 rows", rows)
	}
	seen := make(map[uint64]struct{}, len(rows))
	for _, row := range rows {
		if row.ID == 0 {
			t.Fatalf("history row received zero ID: %#v", row)
		}
		if _, ok := seen[row.ID]; ok {
			t.Fatalf("duplicate history row ID %d: %#v", row.ID, rows)
		}
		seen[row.ID] = struct{}{}
	}
	if rows[0].ID != live[0].ID || rows[1].ID != live[1].ID {
		t.Fatalf("matching live IDs were not reused: got [%d %d], want [%d %d]", rows[0].ID, rows[1].ID, live[0].ID, live[1].ID)
	}
	if rows[2].ID <= live[1].ID {
		t.Fatalf("new history ID %d did not advance past live IDs [%d %d]", rows[2].ID, live[0].ID, live[1].ID)
	}
}

func TestFilterStateOutOfOrderLateRowsDoNotEvictPinnedHistory(t *testing.T) {
	state := NewFilterState(1)
	state.SetFilter("needle")
	history := []LogRecord{
		{Event: LogEvent{Pod: "api", Message: "needle old", ObservedAt: time.Unix(10, 0)}, Text: "needle old"},
		{Event: LogEvent{Pod: "api", Message: "needle current", ObservedAt: time.Unix(20, 0)}, Text: "needle current"},
	}
	if !state.SetSearchRecords("needle", history) {
		t.Fatal("search result rejected")
	}
	historyIDs := []uint64{state.Records()[0].ID, state.Records()[1].ID}

	// The rows arrive out of timestamp order while the history request is in
	// flight. Sorting the merged result moves the first late row ahead of the
	// pinned history prefix.
	state.AppendRecords(
		LogRecord{Event: LogEvent{Pod: "api", Message: "needle late", ObservedAt: time.Unix(30, 0)}, Text: "needle late"},
		LogRecord{Event: LogEvent{Pod: "api", Message: "needle replay", ObservedAt: time.Unix(0, 0)}, Text: "needle replay"},
	)
	if !state.SetSearchRecords("needle", history) {
		t.Fatal("repeated search result rejected")
	}

	// A subsequent append trims the one-row live tail. Both history rows remain
	// pinned even though the merged slice is timestamp sorted.
	state.AppendRecords(LogRecord{Event: LogEvent{Pod: "api", Message: "needle newest", ObservedAt: time.Unix(40, 0)}, Text: "needle newest"})
	rows := state.Records()
	seen := make(map[uint64]bool, len(rows))
	for _, row := range rows {
		seen[row.ID] = true
	}
	for _, id := range historyIDs {
		if !seen[id] {
			t.Fatalf("pinned history ID %d was evicted: %#v", id, rows)
		}
	}
	if len(rows) != 3 || rows[len(rows)-1].Text != "needle newest" {
		t.Fatalf("history plus bounded live tail = %#v", rows)
	}
}

func TestFilterStateHistoryRowsReceiveUniqueIDs(t *testing.T) {
	state := NewFilterState(10)
	state.SetFilter("needle")
	history := []LogRecord{
		{Event: LogEvent{Pod: "old", Message: "first"}, Text: "needle first"},
		{Event: LogEvent{Pod: "old", Message: "second"}, Text: "needle second"},
	}
	if !state.SetSearchRecords("needle", history) {
		t.Fatal("search result rejected")
	}
	rows := state.Records()
	if len(rows) != 2 || rows[0].ID == 0 || rows[1].ID == 0 || rows[0].ID == rows[1].ID {
		t.Fatalf("history IDs = %#v", rows)
	}
}

func TestFilterStateAppendCountWhenSearchTailTrims(t *testing.T) {
	state := NewFilterState(2)
	state.SetFilter("needle")
	if !state.SetSearchRecords("needle", []LogRecord{{Event: LogEvent{Message: "history needle"}, Text: "history needle"}}) {
		t.Fatal("search result rejected")
	}
	added := state.AppendRecords(
		LogRecord{Event: LogEvent{Message: "one needle"}, Text: "one needle"},
		LogRecord{Event: LogEvent{Message: "two needle"}, Text: "two needle"},
		LogRecord{Event: LogEvent{Message: "three needle"}, Text: "three needle"},
	)
	if added != 3 {
		t.Fatalf("added visible rows = %d, want 3", added)
	}
	if got := state.Lines(); len(got) != 3 || got[1] != "two needle" || got[2] != "three needle" {
		t.Fatalf("history plus bounded live tail = %#v", got)
	}
}

func TestFilterStateInvalidQueryAndStructuredSearchBounds(t *testing.T) {
	state := NewFilterState(10)
	state.Append("alpha", "needle", "omega")
	state.SetFilter("status:wat")
	if state.FilterError() == nil {
		t.Fatal("invalid query did not expose an error")
	}
	if len(state.Lines()) != 0 || state.MatchCount() != 0 {
		t.Fatalf("invalid query matched rows: %#v (%d)", state.Lines(), state.MatchCount())
	}

	records := []LogRecord{
		{Event: LogEvent{Message: "zero"}, Text: "zero"},
		{Event: LogEvent{Message: "one"}, Text: "one"},
		{Event: LogEvent{Message: "needle"}, Text: "needle"},
		{Event: LogEvent{Message: "after"}, Text: "after"},
	}
	got, err := SearchRecordsFromFirstMatch(records, "needle", 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Text != "one" || got[1].Text != "needle" {
		t.Fatalf("structured search bounds = %#v", got)
	}
}
