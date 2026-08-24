package core

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

const ansiReset = "\x1b[0m"

var colors = map[string]string{
	"red": "\x1b[31m", "yellow": "\x1b[33m", "cyan": "\x1b[36m", "dim": "\x1b[90m",
}

type Formatter struct {
	Include []*regexp.Regexp
	Exclude []*regexp.Regexp
	ShowPod bool
	Color   bool
	Detail  bool
}

func CompilePatterns(values []string) ([]*regexp.Regexp, error) {
	patterns := make([]*regexp.Regexp, 0, len(values))
	for _, value := range values {
		pattern, err := regexp.Compile(value)
		if err != nil {
			return nil, fmt.Errorf("invalid regex %q: %w", value, err)
		}
		patterns = append(patterns, pattern)
	}
	return patterns, nil
}

func (f Formatter) Format(pod, _ string, message string, hideHeartbeat bool) []string {
	if message == "" || (hideHeartbeat && IsHeartbeat(message)) || !f.shouldShow(message) {
		return nil
	}
	prefix := ""
	if f.ShowPod {
		prefix = f.colorize("["+PodReplicaSuffix(pod)+"] ", "dim")
	}

	var object map[string]any
	if json.Unmarshal([]byte(message), &object) == nil && object != nil {
		timestamp := firstString(object, "ts", "timestamp", "time", "@t")
		level := firstString(object, "level", "lvl", "@l")
		logger := firstString(object, "logger", "SourceContext", "sourceContext")
		rendered := firstString(object, "msg", "message", "RenderedMessage", "@m")
		if rendered == "" {
			rendered = message
		}
		label, colorName := levelStyle(level)
		var builder strings.Builder
		builder.WriteString(prefix)
		if timestamp != "" {
			builder.WriteString(f.colorize(formatTimestamp(timestamp)+" ", colorName))
		}
		if label != "" {
			builder.WriteString(f.colorize("["+label+"] ", colorName))
		}
		if logger != "" {
			builder.WriteString(f.colorize("["+logger+"] ", colorName))
		}
		builder.WriteString(f.colorize(rendered, colorName))
		result := []string{builder.String()}
		if exception := firstString(object, "exception", "Exception", "@x"); exception != "" {
			result = append(result, f.colorize(exception, "red"))
		}
		return result
	}

	rendered := message
	if !f.Detail {
		rendered = StripTrailingStructuredProperties(message)
	}
	_, colorName := textLogStyle(rendered)
	return []string{prefix + f.colorize(rendered, colorName)}
}

func (f Formatter) shouldShow(message string) bool {
	if len(f.Include) > 0 {
		matched := false
		for _, pattern := range f.Include {
			if pattern.MatchString(message) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	for _, pattern := range f.Exclude {
		if pattern.MatchString(message) {
			return false
		}
	}
	return true
}

func (f Formatter) colorize(text, name string) string {
	if !f.Color || name == "" {
		return text
	}
	if code := colors[name]; code != "" {
		return code + text + ansiReset
	}
	return text
}

func firstString(value map[string]any, names ...string) string {
	for _, name := range names {
		if item, ok := value[name]; ok && item != nil {
			text := strings.TrimSpace(fmt.Sprint(item))
			if text != "" {
				return text
			}
		}
	}
	return ""
}

func levelStyle(level string) (string, string) {
	switch strings.ToUpper(strings.TrimSpace(level)) {
	case "ERR", "ERROR", "FATAL", "CRIT", "CRITICAL":
		return "ERR", "red"
	case "WRN", "WARN", "WARNING":
		return "WRN", "yellow"
	case "INF", "INFO", "INFORMATION":
		return "INF", "cyan"
	case "DBG", "DEBUG", "VRB", "VERBOSE", "TRACE", "TRC":
		return strings.ToUpper(strings.TrimSpace(level))[:3], "dim"
	default:
		return strings.ToUpper(strings.TrimSpace(level)), ""
	}
}

var bracketedLevel = regexp.MustCompile(`(?i)\[(?:[^\]]+\s)?(WRN|ERR|INF|DBG|VRB|WARN|ERROR|INFO|DEBUG|VERBOSE|TRACE|TRC)\]`)
var plainLevel = regexp.MustCompile(`(?i)\b(WRN|ERR|INF|DBG|VRB|WARN|ERROR|INFO|DEBUG|VERBOSE|TRACE|TRC)\b`)

func textLogStyle(message string) (string, string) {
	if match := bracketedLevel.FindStringSubmatch(message); len(match) > 1 {
		return levelStyle(match[1])
	}
	if match := plainLevel.FindStringSubmatch(message); len(match) > 1 {
		return levelStyle(match[1])
	}
	return "", ""
}

func formatTimestamp(raw string) string {
	parsed, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return raw
	}
	return parsed.Format("15:04:05.000")
}

func StripTrailingStructuredProperties(message string) string {
	for index := 0; index < len(message); index++ {
		if message[index] != '{' || index == 0 || !strings.ContainsAny(message[index-1:index], " \t") {
			continue
		}
		var object map[string]any
		if json.Unmarshal([]byte(message[index:]), &object) == nil && object != nil {
			return strings.TrimSpace(message[:index])
		}
	}
	return message
}

func PodReplicaSuffix(pod string) string {
	parts := strings.Split(pod, "-")
	if len(parts) >= 3 {
		return strings.Join(parts[len(parts)-2:], "-")
	}
	return pod
}

func UniquePods(items []InventoryItem) []string {
	seen := map[string]bool{}
	var pods []string
	for _, item := range items {
		if !seen[item.Pod] {
			seen[item.Pod] = true
			pods = append(pods, item.Pod)
		}
	}
	return pods
}

func ServiceNames(items []InventoryItem) []string {
	seen := map[string]bool{}
	var services []string
	for _, item := range items {
		if !seen[item.Container] {
			seen[item.Container] = true
			services = append(services, item.Container)
		}
	}
	sort.Strings(services)
	return services
}
