package core

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// SecurityFinding describes a potential threat indicator, not a verified attack.
type SecurityFinding struct {
	ID          string
	Title       string
	Description string
	Score       int
}

// SecurityAssessment is a bounded, local evaluation of one log entry.
// It does not infer client request rates or query data sources outside that entry.
type SecurityAssessment struct {
	Score       int
	Risk        string
	HTTPContext bool
	Truncated   bool
	Findings    []SecurityFinding
}

const securityMaxInputBytes = 64 * 1024

var (
	securitySQLiPattern = regexp.MustCompile("(?i)(\\bunion\\s+(all\\s+)?select\\b|\\b(or|and)\\s+['\"]?[0-9]+['\"]?\\s*=\\s*['\"]?[0-9]+['\"]?|\\b(sleep|benchmark)\\s*\\(|\\binformation_schema\\b)")
	securityXSSPattern = regexp.MustCompile("(?i)(<\\s*script(\\s|/|>)|\\bonerror\\s*=|\\bonload\\s*=|javascript:)")
	securitySensitivePathPattern = regexp.MustCompile("(?i)(^|[/?])((\\.env|\\.git/(config|head)|wp-admin|wp-login\\.php|actuator/(env|beans)|server-status|vendor/phpunit|cgi-bin)([/?&=]|$))")
	securityCommandPattern = regexp.MustCompile("(?i)(;|\\|\\||&&)\\s*(curl|wget|bash|sh|cat|powershell|cmd\\.exe)\\b")
)

// AssessLogSecurity uses deterministic OWASP-inspired indicators. It is intentionally
// passive and never sends logs to an external service or changes Kubernetes objects.
func AssessLogSecurity(record LogRecord) SecurityAssessment {
	fields := record.Fields
	if fields.Method == "" && fields.Path == "" && fields.StatusCode == 0 {
		fields = ParseLogFields(record.Event)
	}
	result := SecurityAssessment{Risk: "NO INDICATORS", HTTPContext: fields.Method != "" || fields.Path != "" || fields.StatusCode != 0}
	raw := StripANSI(record.Event.Message)
	if len(raw) > securityMaxInputBytes {
		raw = raw[:securityMaxInputBytes]
		result.Truncated = true
	}
	path := fields.Path
	if len(path) > securityMaxInputBytes {
		path = path[:securityMaxInputBytes]
		result.Truncated = true
	}

	// Loggers differ: some preserve the URL in RequestPath, others place it
	// only in the rendered HTTP message. Decode at most twice to catch common
	// percent-encoding evasions without expanding unbounded attacker input.
	sample := normalizeSecuritySample(path + " " + raw)
	pathSample := normalizeSecuritySample(path)
	add := func(id, title, description string, score int) {
		result.Findings = append(result.Findings, SecurityFinding{
			ID: id, Title: title, Description: description, Score: score,
		})
		result.Score += score
	}

	if securitySQLiPattern.MatchString(sample) {
		add("WEB-SQLI", "SQL injection-like payload", "SQL operator or database probing syntax appears in the request/log data.", 80)
	}
	if strings.Contains(sample, "../") || strings.Contains(sample, "..\\") {
		add("WEB-TRAVERSAL", "Path traversal pattern", "Relative parent-directory segments may indicate an attempt to access files outside an intended route.", 80)
	}
	if securityXSSPattern.MatchString(sample) {
		add("WEB-XSS", "Cross-site scripting-like payload", "Executable HTML or script syntax appears in the request/log data.", 70)
	}
	if securityCommandPattern.MatchString(sample) {
		add("WEB-COMMAND", "Shell command chaining", "Command separators followed by a shell or process invocation may indicate injection.", 75)
	}
	if securitySensitivePathPattern.MatchString(pathSample) || (path == "" && securitySensitivePathPattern.MatchString(sample)) {
		add("WEB-PROBE", "Sensitive endpoint reconnaissance", "A frequently probed configuration, administrative, or development endpoint was requested.", 45)
	}
	switch strings.ToUpper(strings.TrimSpace(fields.Method)) {
	case "TRACE", "TRACK":
		add("WEB-METHOD", "Unusual HTTP method", "TRACE/TRACK requests can be reconnaissance; verify whether this method is expected.", 30)
	}
	if result.Score > 100 {
		result.Score = 100
	}
	switch {
	case result.Score >= 70:
		result.Risk = "HIGH"
	case result.Score >= 35:
		result.Risk = "MEDIUM"
	case result.Score > 0:
		result.Risk = "LOW"
	case !result.HTTPContext:
		result.Risk = "INSUFFICIENT HTTP CONTEXT"
	}
	return result
}

func normalizeSecuritySample(input string) string {
	if len(input) > securityMaxInputBytes*2 {
		input = input[:securityMaxInputBytes*2]
	}
	for i := 0; i < 2; i++ {
		decoded, err := url.QueryUnescape(input)
		if err != nil || decoded == input {
			break
		}
		input = decoded
	}
	return strings.ToLower(input)
}

// Text returns a copyable, safe-to-display summary. It omits raw URLs and
// request bodies, which may contain credentials or patient information.
func (a SecurityAssessment) Text() string {
	var b strings.Builder
	b.WriteString("SECURITY ASSESSMENT — LOCAL / SINGLE LOG ENTRY\n")
	fmt.Fprintf(&b, "\nRisk: %s | Heuristic score: %d/100\n", a.Risk, a.Score)
	if !a.HTTPContext {
		b.WriteString("HTTP metadata: Not identified in this entry\n")
	}
	if len(a.Findings) == 0 {
		b.WriteString("\nNo known request attack indicators were matched.\n")
	} else {
		fmt.Fprintf(&b, "\nIndicators (%d):\n", len(a.Findings))
		for _, finding := range a.Findings {
			fmt.Fprintf(&b, "\n[%s] %s (+%d)\n%s\n", finding.ID, finding.Title, finding.Score, finding.Description)
		}
	}
	b.WriteString("\nAssessment scope:\n")
	b.WriteString("- Only this selected log event; no IP-based correlation, rate analysis, or request history.\n")
	b.WriteString("- Missing request headers, bodies, or gateway logs can hide threats.\n")
	if a.Truncated {
		b.WriteString("- Oversized log content was truncated for bounded analysis.\n")
	}
	b.WriteString("- Signatures can produce false positives. A score is not proof of an attack.\n")
	b.WriteString("- No matching indicator does not establish that a request is safe.\n")
	b.WriteString("\nNext step: Inspect related requests/trace and ingress access logs before taking action.")
	return b.String()
}
