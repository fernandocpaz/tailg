package core

import (
	"encoding/json"
	"regexp"
	"strings"
)

var logEntryPrefix = regexp.MustCompile(`(?i)^(?:\[?\d{2}:\d{2}:\d{2}|\[?\d{4}-\d{2}-\d{2}[T ]|\[?(?:ERR|ERROR|FATAL|CRIT|CRITICAL|WRN|WARN|WARNING|INF|INFO|INFORMATION|DBG|DEBUG|VRB|VERBOSE|TRACE|TRC)\b)`)
var exceptionPrefix = regexp.MustCompile(`^(?:[[:alnum:]_$]+\.)*[[:alnum:]_$]*(?:Exception|Error)(?:\b|\()`)

// IsLogContinuation recognizes common exception/stack-trace lines. A new
// timestamp, severity prefix or JSON event always starts a separate entry,
// including when that entry is indented. Unrecognized unindented text is kept
// separate rather than attributed to the wrong request.
func IsLogContinuation(message string) bool {
	plain := StripANSI(message)
	text := strings.TrimSpace(plain)
	if text == "" {
		return true
	}
	for _, prefix := range []string{
		"at ", "---", "--->", "... ", "Caused by:", "Suppressed:",
		"Traceback (most recent call last):", "File \"", "goroutine ",
		"ClientConnectionId:", "Error Number:", "HResult:", "HRESULT:",
	} {
		if strings.HasPrefix(text, prefix) {
			return true
		}
	}
	if logEntryPrefix.MatchString(text) || (strings.HasPrefix(text, "{") && json.Valid([]byte(text))) {
		return false
	}
	if strings.HasPrefix(plain, " ") || strings.HasPrefix(plain, "\t") || exceptionPrefix.MatchString(text) {
		return true
	}
	return false
}

// LogBlock returns the selected physical line and its adjacent exception
// continuations, with no line-count cap. Other pods/containers may be interleaved
// in events, but never become part of this block. Selecting a stack frame also
// includes the preceding entry. The bool distinguishes a missing anchor from a
// single-line event, which matters when Kubernetes has rotated the logs.
func LogBlock(events []LogEvent, selected LogEvent) ([]LogEvent, bool) {
	var stream []LogEvent
	anchor := -1
	for _, event := range events {
		if event.Pod != selected.Pod || event.Container != selected.Container || event.Started || event.Closed {
			continue
		}
		stream = append(stream, event)
		if event.ObservedAt.Equal(selected.ObservedAt) && event.Message == selected.Message {
			anchor = len(stream) - 1
		}
	}
	if anchor < 0 {
		return nil, false
	}
	start, end := anchor, anchor+1
	for start > 0 && IsLogContinuation(stream[start].Message) {
		start--
	}
	for end < len(stream) && IsLogContinuation(stream[end].Message) {
		end++
	}
	return append([]LogEvent(nil), stream[start:end]...), true
}
