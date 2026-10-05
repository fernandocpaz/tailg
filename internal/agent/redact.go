package agent

import (
	"regexp"
	"strings"
	"unicode"
)

var (
	// Match whole keys, including common environment/header spellings.
	credentialPattern = regexp.MustCompile(`(?i)(?:^|[^a-z0-9_-])["']?(?:authorization|proxy-authorization|(?:x[-_])?api[-_]?key|(?:access|refresh|id|auth)[-_]?token|token|password|passwd|(?:client[-_])?secret)["']?[ \t]*[=:][ \t]*`)
	bearerPattern     = regexp.MustCompile(`(?i)\bbearer[ \t]+(?:\[REDACTED\]|[A-Za-z0-9._~+/=-]+)`)
	jwtPattern        = regexp.MustCompile(`\beyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\b`)
)

// Redact removes common credentials from diagnostic text. Unterminated quoted
// values are redacted to the end; this is not a general-purpose secret detector.
func Redact(value string) string {
	var output strings.Builder
	for {
		match := credentialPattern.FindStringIndex(value)
		if match == nil {
			output.WriteString(value)
			break
		}
		output.WriteString(value[:match[1]])
		value = value[match[1]:]
		if value == "" {
			break
		}
		if value[0] == '"' || value[0] == '\'' {
			quote := value[0]
			end := 1
			for end < len(value) {
				if value[end] == '\\' && end+1 < len(value) {
					end += 2
					continue
				}
				if value[end] == quote {
					break
				}
				end++
			}
			output.WriteByte(quote)
			output.WriteString("[REDACTED]")
			if end < len(value) {
				output.WriteByte(quote)
				end++
			}
			value = value[end:]
			continue
		}
		// Redact both the authorization scheme and payload, not just "Bearer".
		end := bareCredentialEnd(value)
		scheme := value[:end]
		if strings.EqualFold(scheme, "bearer") || strings.EqualFold(scheme, "basic") {
			start := end
			for start < len(value) && (value[start] == ' ' || value[start] == '\t') {
				start++
			}
			end = start + bareCredentialEnd(value[start:])
		}
		if end == 0 {
			output.WriteByte(value[0])
			value = value[1:]
			continue
		}
		output.WriteString("[REDACTED]")
		value = value[end:]
	}
	value = bearerPattern.ReplaceAllString(output.String(), "Bearer [REDACTED]")
	return jwtPattern.ReplaceAllString(value, "[REDACTED_JWT]")
}

func bareCredentialEnd(value string) int {
	// Preserve markers so applying Redact twice is stable.
	if strings.HasPrefix(value, "[REDACTED]") {
		return len("[REDACTED]")
	}
	for index, r := range value {
		if unicode.IsSpace(r) || strings.ContainsRune(",;&\"'{}[]<>", r) {
			return index
		}
	}
	return len(value)
}
