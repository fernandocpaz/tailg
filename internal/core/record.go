package core

import (
    "time"
    "strings"
)

// LogFields are parsed from the original log before display formatting hides
// structured properties. A missing duration is different from a zero duration.
type LogFields struct {
	Level       string
	Service     string
	TraceID     string
	SpanID      string
	RequestID   string
	ClientIP    string // validated server-emitted remote address; never trust forwarded headers implicitly
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
    // A remote log provider may supply severity independently of content.
    // Decorate only otherwise unclassified plain-text entries.
    if event.Level!="" && textLogStyleAbsent(event.Message) && len(lines)>0 {
        label,colorName:=levelStyle(event.Level)
        if label!="" {
            prefix:=formatter.colorize("["+label+"] ",colorName)
            for i:=range lines{lines[i]=prefix+formatter.colorize(lines[i],colorName)}
        }
    }
	records := make([]LogRecord, 0, len(lines))
	for _, line := range lines {
		records = append(records, LogRecord{Event: event, Text: line, Fields: fields})
	}
	return records
}

func textLogStyleAbsent(message string)bool{
    if _,name:=textLogStyle(message);name!=""{return false}
    trimmed:=strings.TrimSpace(message)
    return !strings.HasPrefix(trimmed,"{")
}
