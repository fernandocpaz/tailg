package core

import (
	"crypto/sha256"
	"time"
)

// LogCursor belongs to one pod/container stream. Attempts must use it serially.
// Only a small timestamp overlap is retained; log messages are stored as hashes.
type LogCursor struct {
	latest time.Time
	seen   map[logIdentity]int
}

type logIdentity struct {
	nanos  int64
	digest [32]byte
}

const maxReplayIdentities = 4096

// LogReplay is a snapshot of already delivered occurrences at reconnect time.
// Counts preserve legitimate identical messages sharing the same timestamp.
type LogReplay struct {
	Since time.Time
	seen  map[logIdentity]int
}

func (c *LogCursor) Resume() LogReplay {
	if c == nil || c.latest.IsZero() {
		return LogReplay{}
	}
	seen := make(map[logIdentity]int, len(c.seen))
	for key, count := range c.seen {
		seen[key] = count
	}
	// Kubernetes may serialize since-time at second precision. Include a full
	// second of overlap and suppress known occurrences locally.
	return LogReplay{Since: c.latest.Truncate(time.Second).Add(-time.Second), seen: seen}
}

func logID(event LogEvent) logIdentity {
	return logIdentity{nanos: event.ObservedAt.UnixNano(), digest: sha256.Sum256([]byte(event.Message))}
}

func (r *LogReplay) Duplicate(event LogEvent) bool {
	if event.ObservedAt.IsZero() || len(r.seen) == 0 {
		return false
	}
	key := logID(event)
	if r.seen[key] == 0 {
		return false
	}
	r.seen[key]--
	if r.seen[key] == 0 {
		delete(r.seen, key)
	}
	return true
}

// Observe is called only after an event has been delivered successfully.
func (c *LogCursor) Observe(event LogEvent) {
	if c == nil || event.ObservedAt.IsZero() {
		return
	}
	if c.seen == nil {
		c.seen = make(map[logIdentity]int)
	}
	if event.ObservedAt.After(c.latest) {
		previousSecond := c.latest.Truncate(time.Second)
		c.latest = event.ObservedAt
		if !previousSecond.Equal(c.latest.Truncate(time.Second)) {
			cutoff := c.latest.Truncate(time.Second).Add(-time.Second).UnixNano()
			for key := range c.seen {
				if key.nanos < cutoff {
					delete(c.seen, key)
				}
			}
		}
	}
	if event.ObservedAt.Before(c.latest.Truncate(time.Second).Add(-time.Second)) {
		return
	}
	key := logID(event)
	if c.seen[key] > 0 || len(c.seen) < maxReplayIdentities {
		c.seen[key]++
	}
	// If the bound is exceeded, prefer a possible replay to dropping a new log.
}
