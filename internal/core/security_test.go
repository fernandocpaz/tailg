package core

import (
	"strings"
	"testing"
)

func TestAssessLogSecurityRequestPatterns(t *testing.T) {
	tests := []struct {
		name string
		path string
		id   string
		min  int
	}{
		{"SQL injection", "/api?filter=%27%20OR%201%3D1--", "WEB-SQLI", 70},
		{"double encoded traversal", "/files/%252e%252e%252fetc/passwd", "WEB-TRAVERSAL", 70},
		{"cross-site scripting", "/search?q=%3Cscript%3Ealert(1)%3C/script%3E", "WEB-XSS", 70},
		{"sensitive endpoint probe", "/.git/config", "WEB-PROBE", 35},
		{"command injection", "/shell?value=hello%3Bbash%20-c%20id", "WEB-COMMAND", 70},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			record := LogRecord{
				Event: LogEvent{Message: "HTTP request", Pod: "api-1"},
				Fields: LogFields{Method: "GET", Path: test.path, StatusCode: 404},
			}
			result := AssessLogSecurity(record)
			if result.Score < test.min {
				t.Fatalf("score=%d, want at least %d", result.Score, test.min)
			}
			found := false
			for _, finding := range result.Findings {
				found = found || finding.ID == test.id
			}
			if !found {
				t.Fatalf("missing %s: %+v", test.id, result)
			}
		})
	}
}

func TestAssessLogSecurityNormalRequestsAndMissingMetadata(t *testing.T) {
	for _, status := range []int{200, 401, 403, 404, 500} {
		record := LogRecord{Event: LogEvent{Message: "HTTP request"},
			Fields: LogFields{Method: "GET", Path: "/patients/123", StatusCode: status}}
		result := AssessLogSecurity(record)
		if result.Score != 0 || result.Risk != "NO INDICATORS" {
			t.Fatalf("status %d produced false alert: %+v", status, result)
		}
	}
	result := AssessLogSecurity(LogRecord{Event: LogEvent{Message: "database connection temporarily unavailable"}})
	if result.Risk != "INSUFFICIENT HTTP CONTEXT" || result.HTTPContext {
		t.Fatalf("missing HTTP metadata misclassified: %+v", result)
	}
}

func TestAssessLogSecurityRedactsEvidenceFromReport(t *testing.T) {
	token := "supersecret-unique-token"
	record := LogRecord{Event: LogEvent{Message: "GET /.env?token=" + token},
		Fields: LogFields{Method: "GET", Path: "/.env?token=" + token}}
	result := AssessLogSecurity(record)
	report := result.Text()
	if result.Score == 0 || strings.Contains(report, token) || strings.Contains(report, record.Fields.Path) {
		t.Fatalf("report missed finding or leaked raw URL: %s", report)
	}
	if !strings.Contains(report, "single") && !strings.Contains(report, "SINGLE") {
		t.Fatal("report must declare the single-event scope")
	}
}

func TestAssessLogSecurityLimitsLargeLog(t *testing.T) {
	record := LogRecord{Event: LogEvent{Message: strings.Repeat("a", securityMaxInputBytes+100)}}
	result := AssessLogSecurity(record)
	if !result.Truncated || !strings.Contains(result.Text(), "truncated") {
		t.Fatalf("oversized content was not reported as limited: %+v", result)
	}
}
