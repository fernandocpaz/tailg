package core

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// LogQuery is a case-insensitive query made up of optional field predicates
// and a literal text search. The literal is intentionally contiguous, as it
// was in the original line filter.
type LogQuery struct {
	predicates []logPredicate
	literal    string
}

type logPredicate struct {
	field string
	op    string
	text  string
	num   int64
	dur   time.Duration
}

// CompileLogQuery parses field predicates while retaining unknown colon words
// as literal text. Recognized malformed predicates return an error.
func CompileLogQuery(text string) (*LogQuery, error) {
	tokens, err := scanLogQuery(text)
	if err != nil {
		return nil, err
	}
	query := &LogQuery{}
	var literals []string
	recognizedAny := false
	for _, token := range tokens {
		predicate, recognized, err := parseLogPredicate(token)
		if err != nil {
			return nil, err
		}
		if recognized {
			recognizedAny = true
			query.predicates = append(query.predicates, predicate)
		} else {
			literals = append(literals, token.value)
		}
	}
	if !recognizedAny && !queryUsesPhraseQuotes(text) {
		// Keep the original line-filter behavior for a plain search, including
		// repeated whitespace and JSON punctuation.
		query.literal = strings.TrimSpace(text)
	} else {
		query.literal = strings.Join(literals, " ")
	}
	return query, nil
}

func queryUsesPhraseQuotes(text string) bool {
	jsonDepth := 0
	jsonString := false
	for index := 0; index < len(text); index++ {
		char := text[index]
		if jsonDepth > 0 {
			if jsonString {
				if char == '\\' {
					index++
				} else if char == '"' {
					jsonString = false
				}
				continue
			}
			switch char {
			case '"':
				jsonString = true
			case '{', '[':
				jsonDepth++
			case '}', ']':
				jsonDepth--
			}
			continue
		}
		switch char {
		case '"':
			return true
		case '{', '[':
			jsonDepth = 1
		}
	}
	return false
}

// Matches reports whether every predicate and the literal portion match.
func (q *LogQuery) Matches(record LogRecord) bool {
	if q == nil {
		return true
	}
	if q.literal != "" && !strings.Contains(strings.ToLower(StripANSI(record.Text)), strings.ToLower(q.literal)) {
		return false
	}
	for _, predicate := range q.predicates {
		var matched bool
		switch predicate.field {
		case "level":
			matched = strings.EqualFold(record.Fields.Level, predicate.text)
		case "service":
			matched = strings.Contains(strings.ToLower(record.Fields.Service), strings.ToLower(predicate.text))
		case "pod":
			matched = strings.Contains(strings.ToLower(record.Event.Pod), strings.ToLower(predicate.text))
		case "trace":
			matched = strings.EqualFold(record.Fields.TraceID, predicate.text)
		case "method":
			matched = strings.EqualFold(record.Fields.Method, predicate.text)
		case "path":
			matched = strings.Contains(strings.ToLower(record.Fields.Path), strings.ToLower(predicate.text))
		case "status":
			matched = record.Fields.StatusCode != 0 && compareInt(int64(record.Fields.StatusCode), predicate.op, predicate.num)
		case "duration":
			matched = record.Fields.HasDuration && compareInt(int64(record.Fields.Duration), predicate.op, int64(predicate.dur))
		}
		if !matched {
			return false
		}
	}
	return true
}

// Highlight returns the literal substring that should be highlighted in the
// rendered line. Structured-only queries have no highlight text.
func (q *LogQuery) Highlight() string {
	if q == nil {
		return ""
	}
	return q.literal
}

type logQueryToken struct {
	value string
}

func scanLogQuery(text string) ([]logQueryToken, error) {
	var tokens []logQueryToken
	jsonDepth := 0
	jsonString := false
	for index := 0; index < len(text); {
		for index < len(text) && (text[index] == ' ' || text[index] == '\t' || text[index] == '\n' || text[index] == '\r') {
			index++
		}
		if index == len(text) {
			break
		}
		var builder strings.Builder
		quoted := false
		for index < len(text) {
			char := text[index]
			if jsonDepth > 0 {
				// Keep JSON string state separate from query phrase quoting so
				// braces and escaped quotes inside a value do not end the raw
				// JSON literal early.
				if jsonString {
					builder.WriteByte(char)
					if char == '\\' && index+1 < len(text) {
						builder.WriteByte(text[index+1])
						index += 2
						continue
					}
					if char == '"' {
						jsonString = false
					}
					index++
					continue
				}
				if char == '"' {
					builder.WriteByte(char)
					jsonString = true
					index++
					continue
				}
			}
			if char == '"' {
				quoted = !quoted
				index++
				continue
			}
			if char == '\\' && quoted {
				index++
				if index == len(text) {
					return nil, fmt.Errorf("query has an incomplete escape")
				}
				switch text[index] {
				case 'n':
					builder.WriteByte('\n')
				case 'r':
					builder.WriteByte('\r')
				case 't':
					builder.WriteByte('\t')
				default:
					builder.WriteByte(text[index])
				}
				index++
				continue
			}
			if !quoted && (char == ' ' || char == '\t' || char == '\n' || char == '\r') {
				break
			}
			builder.WriteByte(char)
			if char == '{' || char == '[' {
				jsonDepth++
			} else if (char == '}' || char == ']') && jsonDepth > 0 {
				jsonDepth--
			}
			index++
		}
		if quoted {
			return nil, fmt.Errorf("query has an unterminated quote")
		}
		tokens = append(tokens, logQueryToken{value: builder.String()})
	}
	return tokens, nil
}

var recognizedLogQueryFields = map[string]bool{
	"level": true, "service": true, "pod": true, "trace": true, "traceid": true,
	"status": true, "duration": true, "method": true, "path": true,
}

func parseLogPredicate(token logQueryToken) (logPredicate, bool, error) {
	text := token.value
	lower := strings.ToLower(text)
	field, op, value, found := splitLogPredicate(lower, text)
	if !found {
		return logPredicate{}, false, nil
	}
	if !recognizedLogQueryFields[field] {
		return logPredicate{}, false, nil
	}
	if value == "" {
		return logPredicate{}, true, fmt.Errorf("%s predicate has an empty value", field)
	}
	if field != "status" && field != "duration" && op != "=" && op != "==" {
		return logPredicate{}, true, fmt.Errorf("%s does not support operator %q", field, op)
	}
	predicate := logPredicate{field: field, op: op}
	switch field {
	case "status":
		num, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return logPredicate{}, true, fmt.Errorf("invalid status %q: use an integer", value)
		}
		predicate.num = num
	case "duration":
		duration, err := parseQueryDuration(value)
		if err != nil {
			return logPredicate{}, true, fmt.Errorf("invalid duration %q: %w", value, err)
		}
		predicate.dur = duration
	case "trace", "traceid":
		trace := validTraceID(value)
		if trace == "" {
			return logPredicate{}, true, fmt.Errorf("invalid trace ID %q: expected 32 hexadecimal characters", value)
		}
		predicate.field = "trace"
		predicate.text = trace
	case "level":
		predicate.text = normalizeLogLevel(value)
	default:
		predicate.text = value
	}
	return predicate, true, nil
}

func splitLogPredicate(lower, original string) (field, op, value string, found bool) {
	if index := strings.IndexByte(lower, ':'); index >= 0 {
		field = lower[:index]
		if recognizedLogQueryFields[field] {
			value = original[index+1:]
			for _, candidateOp := range []string{">=", "<=", "!=", "==", ">", "<", "="} {
				if strings.HasPrefix(value, candidateOp) {
					return field, candidateOp, value[len(candidateOp):], true
				}
			}
			return field, "=", value, true
		}
	}
	for _, candidate := range []string{"duration", "status", "level", "service", "pod", "traceid", "trace", "method", "path"} {
		if !strings.HasPrefix(lower, candidate) {
			continue
		}
		remainder := original[len(candidate):]
		for _, candidateOp := range []string{">=", "<=", "!=", "==", ">", "<", "="} {
			if strings.HasPrefix(remainder, candidateOp) {
				return candidate, candidateOp, remainder[len(candidateOp):], true
			}
		}
	}
	return "", "", "", false
}

func parseQueryDuration(value string) (time.Duration, error) {
	if value == "" {
		return 0, fmt.Errorf("duration is empty")
	}
	if allDigits(value) {
		milliseconds, err := strconv.ParseInt(value, 10, 64)
		if err != nil || milliseconds < 0 || milliseconds > math.MaxInt64/int64(time.Millisecond) {
			return 0, fmt.Errorf("use a duration such as 250ms or 2s")
		}
		return time.Duration(milliseconds) * time.Millisecond, nil
	}
	duration, err := time.ParseDuration(value)
	if err != nil || duration < 0 {
		return 0, fmt.Errorf("use a duration such as 250ms or 2s")
	}
	return duration, nil
}

func allDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, char := range value {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}

func compareInt(actual int64, op string, expected int64) bool {
	switch op {
	case ">":
		return actual > expected
	case ">=":
		return actual >= expected
	case "<":
		return actual < expected
	case "<=":
		return actual <= expected
	case "!=":
		return actual != expected
	case "=", "==":
		return actual == expected
	default:
		return false
	}
}
