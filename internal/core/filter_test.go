package core

import "testing"

func TestFilterStateCompleteHistoryContextAndMatchesOnly(t *testing.T) {
	state := NewFilterState(2)
	state.Append("recent one", "recent needle")
	state.SetFilter("needle")
	if !state.SetSearchResults("needle", []string{"old needle", "context row", "recent needle"}) {
		t.Fatal("search result was rejected")
	}
	state.Append("future context", "future needle")
	if got := state.Lines(); len(got) != 5 || got[1] != "context row" || got[3] != "future context" {
		t.Fatalf("context view = %#v", got)
	}
	state.SetMatchesOnly(true)
	got := state.Lines()
	want := []string{"old needle", "recent needle", "future needle"}
	if len(got) != len(want) {
		t.Fatalf("matches = %#v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("matches[%d]=%q", i, got[i])
		}
	}
	if state.MatchIndex() != 0 {
		t.Fatalf("match index = %d", state.MatchIndex())
	}
	state.SetFilter("")
	got = state.Lines()
	if len(got) != 2 || got[0] != "future context" {
		t.Fatalf("cleared = %#v", got)
	}
}

func TestSearchLinesFromFirstMatch(t *testing.T) {
	lines := []string{"zero", "one", "two", "three", "four", "context", "needle", "after", "last"}
	got := SearchLinesFromFirstMatch(lines, "needle", 2, 4)
	want := []string{"four", "context", "needle", "after"}
	if len(got) != len(want) {
		t.Fatalf("got %#v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got[%d]=%q", i, got[i])
		}
	}
	all := SearchLinesFromFirstMatch(lines, "needle", 1, -1)
	if len(all) != 4 {
		t.Fatalf("unlimited length = %d", len(all))
	}
}

func TestFilterStateBoundsLiveAndSearchLines(t *testing.T) {
	state := NewFilterState(3)
	state.Append("one", "two", "three", "four")
	if got := state.AllLines(); len(got) != 3 || got[0] != "two" || got[2] != "four" {
		t.Fatalf("bounded live lines = %#v", got)
	}

	state.SetFilter("needle")
	state.SetSearchResults("needle", []string{"needle", "context one", "context two", "discarded"})
	if got := state.Lines(); len(got) != 3 || got[0] != "needle" || got[2] != "context two" {
		t.Fatalf("bounded search context = %#v", got)
	}

	state.Append("new context")
	if got := state.Lines(); len(got) != 3 || got[0] != "context one" || got[2] != "new context" {
		t.Fatalf("bounded active search = %#v", got)
	}
}
