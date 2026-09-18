package core

import "time"

// LogFields are parsed from the original log before display formatting hides
// structured properties. A missing duration is different from a zero duration.
type LogFields struct {
	Level       string
	Service     string
	TraceID     string
	SpanID      string
	RequestID   string
	Duration    time.Duration
	HasDuration bool
	StatusCode  int
	Method      string
	Path        string
}

// LogRecord preserves source identity, transport timestamp and raw content while
// keeping a separate compact display line. ID is a local, monotonic row identity.
type LogRecord struct {
	ID     uint64
	Event  LogEvent
	Text   string
	Fields LogFields
}

func RecordsForEvent(formatter Formatter, event LogEvent, hideHeartbeat bool) []LogRecord {
	fields := ParseLogFields(event)
	lines := formatter.Format(event.Pod, event.Container, event.Message, hideHeartbeat)
	records := make([]LogRecord, 0, len(lines))
	for _, line := range lines {
		records = append(records, LogRecord{Event: event, Text: line, Fields: fields})
	}
	return records
}
