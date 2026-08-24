package core

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var compactDuration = regexp.MustCompile(`(?i)(\d+)(ms|[smhd])`)
var dayClockDuration = regexp.MustCompile(`^(?:(\d+)\.)?(\d{1,2}):(\d{2}):(\d{2})(?:\.\d+)?$`)

func ParseDuration(value string, defaultUnit time.Duration) (time.Duration, error) {
	text := strings.ToLower(strings.TrimSpace(value))
	if text == "" {
		return 0, fmt.Errorf("duration is empty")
	}
	if onlyDigits, err := strconv.Atoi(text); err == nil {
		if onlyDigits <= 0 {
			return 0, fmt.Errorf("duration must be greater than zero")
		}
		return time.Duration(onlyDigits) * defaultUnit, nil
	}
	parsed, ok := ParseLogDuration(text)
	if !ok || parsed <= 0 {
		return 0, fmt.Errorf("use a duration such as 5s, 15m, 1h, or 4d")
	}
	return parsed, nil
}

func ParseLogDuration(value string) (time.Duration, bool) {
	text := strings.ToLower(strings.TrimSpace(value))
	if text == "" || text == "never" || text == "none" || text == "n/a" || text == "-" {
		return 0, false
	}
	if match := dayClockDuration.FindStringSubmatch(text); match != nil {
		days, _ := strconv.Atoi(zeroIfEmpty(match[1]))
		hours, _ := strconv.Atoi(match[2])
		minutes, _ := strconv.Atoi(match[3])
		seconds, _ := strconv.Atoi(match[4])
		return time.Duration(days*24+hours)*time.Hour + time.Duration(minutes)*time.Minute + time.Duration(seconds)*time.Second, true
	}
	matches := compactDuration.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return 0, false
	}
	var rebuilt strings.Builder
	var result time.Duration
	for _, match := range matches {
		rebuilt.WriteString(match[0])
		amount, _ := strconv.Atoi(match[1])
		switch strings.ToLower(match[2]) {
		case "ms":
			result += time.Duration(amount) * time.Millisecond
		case "s":
			result += time.Duration(amount) * time.Second
		case "m":
			result += time.Duration(amount) * time.Minute
		case "h":
			result += time.Duration(amount) * time.Hour
		case "d":
			result += time.Duration(amount) * 24 * time.Hour
		}
	}
	return result, rebuilt.String() == text
}

func NormalizeSince(value string) string {
	if !strings.Contains(strings.ToLower(value), "d") {
		return strings.TrimSpace(value)
	}
	duration, ok := ParseLogDuration(value)
	if !ok {
		return strings.TrimSpace(value)
	}
	if duration%time.Hour == 0 {
		return fmt.Sprintf("%dh", int(duration/time.Hour))
	}
	return fmt.Sprintf("%ds", int(duration/time.Second))
}

func FormatDuration(value time.Duration, available bool) string {
	if !available {
		return "never"
	}
	if value < 0 {
		value = 0
	}
	seconds := int64(value / time.Second)
	days := seconds / 86400
	seconds %= 86400
	hours := seconds / 3600
	seconds %= 3600
	minutes := seconds / 60
	seconds %= 60
	if days > 0 {
		return fmt.Sprintf("%d.%02d:%02d:%02d", days, hours, minutes, seconds)
	}
	return fmt.Sprintf("%02d:%02d:%02d", hours, minutes, seconds)
}

func zeroIfEmpty(value string) string {
	if value == "" {
		return "0"
	}
	return value
}
