package core

import (
	"encoding/json"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ParseLogFields extracts the fields used by the log query language. It always
// reads Event.Message; the formatted display line may have hidden properties.
func ParseLogFields(event LogEvent) LogFields {
	f := LogFields{Service: event.Container}
	objects := logJSONObjects(event.Message)
	entries := make([]logJSONEntry, 0)
	for objectIndex, object := range objects {
		before := len(entries)
		flattenLogJSON(object, "", &entries)
		for index := before; index < len(entries); index++ {
			entries[index].object = objectIndex
		}
	}

	if value := logEntryString(entries, "@l", "level", "lvl", "loglevel", "severity"); value != "" {
		f.Level = normalizeLogLevel(value)
	} else if value := leadingTextLogLevel(event.Message); value != "" {
		f.Level = normalizeLogLevel(value)
	}
	if value := logEntryString(entries, "service.name", "servicename", "service_name", "service"); value != "" {
		f.Service = value
	}
	for _, value := range []string{
		logEntryString(entries, "traceparent"),
		logEntryString(entries, "@tr", "traceid", "trace_id", "trace-id", "trace"),
		bracketTraceID(event.Message),
	} {
		if trace := validTraceID(value); trace != "" {
			f.TraceID = trace
			break
		}
	}
	f.SpanID = validSpanID(logEntryString(entries, "@sp", "spanid", "span_id", "span-id", "span"))
	f.RequestID = logEntryString(entries, "requestid", "request_id", "request-id", "requestId", "x-request-id", "correlationid", "correlation_id")

	// Serilog's conventional HTTP text format supplies these values even when
	// the trailing properties object is unavailable.
	httpMessage := logEntryString(entries, "@m", "message", "msg", "renderedmessage")
	if httpMessage == "" {
		httpMessage = event.Message
	}
	method, path, status, duration, hasDuration := parseHTTPText(httpMessage)
	if method == "" && path == "" {
		method, path, status, duration, hasDuration = parseHTTPText(event.Message)
	}
	f.Method, f.Path, f.StatusCode, f.Duration, f.HasDuration = method, path, status, duration, hasDuration
	if value := logEntryString(entries, "requestmethod", "request_method", "http.method", "method"); value != "" {
		f.Method = value
	}
	if value := logEntryString(entries, "requestpath", "request_path", "http.path", "path"); value != "" {
		f.Path = value
	}
	if value, ok := logEntryInt(entries, "statuscode", "status_code", "http.status_code", "status"); ok {
		f.StatusCode = value
	}
	if value, ok := logEntryMilliseconds(entries, "elapsed", "elapsedmilliseconds", "elapsed_ms"); ok {
		f.Duration, f.HasDuration = value, true
	}
	return f
}

var leadingPlainLogLevel = regexp.MustCompile(`(?i)^\s*(WRN|ERR|INF|DBG|VRB|WARN|ERROR|INFO|DEBUG|VERBOSE|TRACE|TRC)(?:\s+|:\s*|$)`)

func leadingTextLogLevel(message string) string {
	if match := bracketedLevel.FindStringSubmatch(message); len(match) > 1 {
		return match[1]
	}
	if match := leadingPlainLogLevel.FindStringSubmatch(message); len(match) > 1 {
		return match[1]
	}
	return ""
}

type logJSONEntry struct {
	path   string
	leaf   string
	value  any
	depth  int
	root   string
	object int
}

func logJSONObjects(message string) []map[string]any {
	var objects []map[string]any
	for index := 0; index < len(message); index++ {
		if message[index] != '{' {
			continue
		}
		decoder := json.NewDecoder(strings.NewReader(message[index:]))
		decoder.UseNumber()
		var value any
		if decoder.Decode(&value) == nil {
			if object, ok := value.(map[string]any); ok {
				objects = append(objects, object)
				// A valid object consumes the remainder of a trailing structured
				// log, so continuing is only useful for another independent object.
				index += int(decoder.InputOffset()) - 1
			}
		}
	}
	return objects
}

func flattenLogJSON(value map[string]any, prefix string, entries *[]logJSONEntry) {
	flattenLogJSONAt(value, prefix, "", 0, entries)
}

// flattenLogJSONAt sorts object keys before walking them. Besides making the
// result reproducible, the depth and root fields let field lookup prefer
// explicit root fields over conventional Properties and arbitrary payloads.
func flattenLogJSONAt(value map[string]any, prefix, root string, depth int, entries *[]logJSONEntry) {
	keys := make([]string, 0, len(value))
	for key := range value {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		item := value[key]
		path := key
		if prefix != "" {
			path = prefix + "." + key
		}
		entryRoot := root
		if entryRoot == "" {
			entryRoot = key
		}
		entryDepth := depth + 1
		if child, ok := item.(map[string]any); ok {
			*entries = append(*entries, logJSONEntry{path: path, leaf: key, value: item, depth: entryDepth, root: entryRoot})
			flattenLogJSONAt(child, path, entryRoot, entryDepth, entries)
			continue
		}
		*entries = append(*entries, logJSONEntry{path: path, leaf: key, value: item, depth: entryDepth, root: entryRoot})
	}
}

func logKey(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func logEntryMatch(entry logJSONEntry, names []string) bool {
	_, _, ok := logEntryRank(entry, names)
	return ok
}

func logEntryString(entries []logJSONEntry, names ...string) string {
	for _, entry := range orderedLogEntries(entries, names...) {
		if text, ok := logValueString(entry.value); ok && strings.TrimSpace(text) != "" {
			return strings.TrimSpace(text)
		}
	}
	return ""
}

func logValueString(value any) (string, bool) {
	switch item := value.(type) {
	case string:
		return item, true
	case json.Number:
		return item.String(), true
	default:
		return "", false
	}
}

func logEntryInt(entries []logJSONEntry, names ...string) (int, bool) {
	for _, entry := range orderedLogEntries(entries, names...) {
		text, ok := logValueString(entry.value)
		if !ok {
			continue
		}
		value, err := strconv.Atoi(strings.TrimSpace(text))
		if err == nil {
			return value, true
		}
	}
	return 0, false
}

func logEntryMilliseconds(entries []logJSONEntry, names ...string) (time.Duration, bool) {
	for _, entry := range orderedLogEntries(entries, names...) {
		text, ok := logValueString(entry.value)
		if !ok {
			continue
		}
		milliseconds, err := strconv.ParseFloat(strings.TrimSpace(text), 64)
		if err != nil || math.IsNaN(milliseconds) || math.IsInf(milliseconds, 0) || milliseconds < 0 || milliseconds > float64(math.MaxInt64)/float64(time.Millisecond) {
			continue
		}
		return time.Duration(math.Round(milliseconds * float64(time.Millisecond))), true
	}
	return 0, false
}

type rankedLogEntry struct {
	entry    logJSONEntry
	priority int
	alias    int
}

// orderedLogEntries gives direct root fields precedence over nested fields.
// Properties is a conventional Serilog container and remains a useful
// fallback, while arbitrary nested payloads cannot shadow explicit fields.
func orderedLogEntries(entries []logJSONEntry, names ...string) []logJSONEntry {
	ranked := make([]rankedLogEntry, 0, len(entries))
	for _, entry := range entries {
		priority, alias, ok := logEntryRank(entry, names)
		if ok {
			ranked = append(ranked, rankedLogEntry{entry: entry, priority: priority, alias: alias})
		}
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].priority != ranked[j].priority {
			return ranked[i].priority < ranked[j].priority
		}
		if ranked[i].alias != ranked[j].alias {
			return ranked[i].alias < ranked[j].alias
		}
		if ranked[i].entry.object != ranked[j].entry.object {
			return ranked[i].entry.object < ranked[j].entry.object
		}
		if logKey(ranked[i].entry.path) != logKey(ranked[j].entry.path) {
			return logKey(ranked[i].entry.path) < logKey(ranked[j].entry.path)
		}
		return logKey(ranked[i].entry.leaf) < logKey(ranked[j].entry.leaf)
	})
	ordered := make([]logJSONEntry, len(ranked))
	for index, candidate := range ranked {
		ordered[index] = candidate.entry
	}
	return ordered
}

func logEntryRank(entry logJSONEntry, names []string) (priority, alias int, ok bool) {
	path, leaf := logKey(entry.path), logKey(entry.leaf)
	for index, name := range names {
		name = logKey(name)
		if path == name {
			// A path explicitly named by the query's alias list is preferable
			// to a leaf-only match in an arbitrary nested payload.
			if entry.depth <= 1 {
				return 0, index, true
			}
			if strings.EqualFold(entry.root, "properties") && entry.depth == 2 {
				return 2, index, true
			}
			return 1, index, true
		}
		if leaf == name {
			if entry.depth <= 1 {
				return 0, index, true
			}
			if strings.EqualFold(entry.root, "properties") && entry.depth == 2 {
				return 2, index, true
			}
			return 3, index, true
		}
	}
	return 0, 0, false
}

var httpTextPattern = regexp.MustCompile(`(?i)\bHTTP\s+([A-Z]+)\s+(\S+)\s+responded\s+(\d{3})\s+in\s+([-+]?(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+))\s*(ns|us|µs|ms|s)\b`)

func parseHTTPText(message string) (string, string, int, time.Duration, bool) {
	match := httpTextPattern.FindStringSubmatch(message)
	if len(match) == 0 {
		return "", "", 0, 0, false
	}
	status, _ := strconv.Atoi(match[3])
	number, err := strconv.ParseFloat(match[4], 64)
	if err != nil || math.IsNaN(number) || math.IsInf(number, 0) || number < 0 {
		return match[1], match[2], status, 0, false
	}
	unit := strings.ToLower(match[5])
	multiplier := map[string]float64{"ns": 1, "us": 1e3, "µs": 1e3, "ms": 1e6, "s": 1e9}[unit]
	if multiplier == 0 || number > float64(math.MaxInt64)/multiplier {
		return match[1], match[2], status, 0, false
	}
	return match[1], match[2], status, time.Duration(math.Round(number * multiplier)), true
}

var bracketTracePattern = regexp.MustCompile(`\[([0-9a-fA-F]{32})\]`)

func bracketTraceID(message string) string {
	match := bracketTracePattern.FindStringSubmatch(message)
	if len(match) == 2 {
		return match[1]
	}
	return ""
}

func validTraceID(value string) string {
	text := strings.TrimSpace(value)
	if strings.HasPrefix(text, "[") && strings.HasSuffix(text, "]") {
		text = strings.TrimSpace(text[1 : len(text)-1])
	}
	parts := strings.Split(text, "-")
	if len(parts) == 4 && len(parts[0]) == 2 && len(parts[1]) == 32 && len(parts[2]) == 16 && len(parts[3]) == 2 {
		// W3C trace-context requires all four fields to be hexadecimal. The
		// version ff is reserved, and a parent ID of all zeroes is invalid.
		if !isHex(parts[0]) || !isHex(parts[1]) || !isHex(parts[2]) || !isHex(parts[3]) || strings.EqualFold(parts[0], "ff") || strings.Trim(parts[2], "0") == "" {
			return ""
		}
		text = parts[1]
	}
	if len(text) != 32 || !isHex(text) || strings.Trim(text, "0") == "" {
		return ""
	}
	return strings.ToLower(text)
}

func validSpanID(value string) string {
	text := strings.TrimSpace(value)
	if len(text) != 16 || !isHex(text) || strings.Trim(text, "0") == "" {
		return ""
	}
	return strings.ToLower(text)
}

func isHex(value string) bool {
	for _, char := range value {
		if !(char >= '0' && char <= '9') && !(char >= 'a' && char <= 'f') && !(char >= 'A' && char <= 'F') {
			return false
		}
	}
	return true
}

func normalizeLogLevel(value string) string {
	text := strings.ToUpper(strings.TrimSpace(value))
	switch text {
	case "ERR", "ERROR", "FATAL", "CRIT", "CRITICAL":
		return "ERR"
	case "WRN", "WARN", "WARNING":
		return "WRN"
	case "INF", "INFO", "INFORMATION":
		return "INF"
	case "DBG", "DEBUG":
		return "DBG"
	case "VRB", "VERBOSE":
		return "VRB"
	case "TRC", "TRA", "TRACE":
		return "TRC"
	default:
		return text
	}
}
