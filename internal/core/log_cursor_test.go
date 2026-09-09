package core

import (
	"fmt"
	"testing"
	"time"
)

func TestLogCursorPreservesRepeatedMessagesAndTimestampBoundaries(t *testing.T) {
	base := time.Date(2026, 9, 9, 20, 0, 0, 100, time.UTC)
	cursor := &LogCursor{}
	first := LogEvent{ObservedAt: base, Message: "retry"}
	second := LogEvent{ObservedAt: base.Add(800 * time.Millisecond), Message: "retry"}
	cursor.Observe(first)
	cursor.Observe(first)
	cursor.Observe(second)
	replay := cursor.Resume()
	if !replay.Since.Equal(base.Truncate(time.Second).Add(-time.Second)) {
		t.Fatalf("resume time = %v", replay.Since)
	}
	for _, event := range []LogEvent{first, first, second} {
		if !replay.Duplicate(event) {
			t.Fatalf("known occurrence not recognized: %+v", event)
		}
	}
	for _, event := range []LogEvent{first, {ObservedAt: base, Message: "different"}, {Message: "retry"}} {
		if replay.Duplicate(event) {
			t.Fatalf("new occurrence suppressed: %+v", event)
		}
	}
	// A new attempt has an independent replay budget, even when the prior
	// reconnect ended before receiving anything beyond the overlap.
	if next := cursor.Resume(); !next.Duplicate(first) || !next.Duplicate(first) {
		t.Fatal("reconnect consumed the persistent cursor")
	}
}

func TestLogCursorBoundedMemoryAndUnknownTimestamps(t *testing.T) {
	cursor := &LogCursor{}
	cursor.Observe(LogEvent{Message: "without timestamp"})
	if !cursor.Resume().Since.IsZero() {
		t.Fatal("invented a position for an undated log")
	}
	base := time.Now()
	for i := 0; i < maxReplayIdentities+10; i++ {
		cursor.Observe(LogEvent{ObservedAt: base, Message: fmt.Sprint(i)})
	}
	if len(cursor.seen) != maxReplayIdentities {
		t.Fatalf("replay memory exceeded bound: %d", len(cursor.seen))
	}
	replay := cursor.Resume()
	if replay.Duplicate(LogEvent{ObservedAt: base, Message: "brand new"}) {
		t.Fatal("overflow suppressed a new log")
	}
	cursor.Observe(LogEvent{ObservedAt: base.Add(3 * time.Second), Message: "later"})
	if len(cursor.seen) != 1 {
		t.Fatalf("old overlap not evicted: %d", len(cursor.seen))
	}
	latest := cursor.Resume().Since
	cursor.Observe(LogEvent{ObservedAt: base.Add(-time.Hour), Message: "out of order"})
	if !cursor.Resume().Since.Equal(latest) {
		t.Fatal("out-of-order log moved the cursor backwards")
	}
}
