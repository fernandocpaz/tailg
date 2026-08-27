package agent

import "regexp"

var (
	credentialPattern = regexp.MustCompile(`(?i)(authorization|api[-_]?key|access[-_]?token|token|password|passwd|secret)(\s*[=:]\s*|"\s*:\s*")([^\s,;"'}]+)`)
	bearerPattern     = regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._~+/=-]+`)
	jwtPattern        = regexp.MustCompile(`\beyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\b`)
)

func Redact(value string) string {
	value = credentialPattern.ReplaceAllString(value, `$1$2[REDACTED]`)
	value = bearerPattern.ReplaceAllString(value, "Bearer [REDACTED]")
	return jwtPattern.ReplaceAllString(value, "[REDACTED_JWT]")
}
