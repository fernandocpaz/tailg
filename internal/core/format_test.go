package core

import (
	"strings"
	"testing"
)

func TestFormatterHidesTrailingStructuredProperties(t *testing.T) {
	formatter := Formatter{}
	message := `[13:19:49 INF] [trace] HTTP POST /v1/items responded 200 in 62.0327 ms {"SourceContext":"Middleware","RequestId":"1"}`
	got := formatter.Format("pod", "web", message, false)
	if len(got) != 1 || strings.Contains(got[0], "SourceContext") {
		t.Fatalf("formatted = %#v", got)
	}
	formatter.Detail = true
	got = formatter.Format("pod", "web", message, false)
	if !strings.Contains(got[0], "SourceContext") {
		t.Fatalf("detail = %#v", got)
	}
}

func TestFormatterHidesStructuredPropertiesBeforeTrailingAssignments(t *testing.T) {
	formatter := Formatter{}
	message := `[13:47:22 ERR] SSRS render failed: Query execution failed for dataset 'GetEndEncounter'. {"Step":"Render","Fault":{"Reason":"processing failed"}} ResponseBodyExcerpt=null`
	got := formatter.Format("pod", "web", message, false)
	if len(got) != 1 || strings.Contains(got[0], `"Step"`) || strings.Contains(got[0], "ResponseBodyExcerpt") || !strings.Contains(got[0], "GetEndEncounter") {
		t.Fatalf("formatted = %#v", got)
	}

	formatter.Detail = true
	got = formatter.Format("pod", "web", message, false)
	if len(got) != 1 || !strings.Contains(got[0], `"Step"`) || !strings.Contains(got[0], "ResponseBodyExcerpt=null") {
		t.Fatalf("detail = %#v", got)
	}
}

func TestFormatterParsesStructuredJSON(t *testing.T) {
	formatter := Formatter{}
	got := formatter.Format("pod", "web", `{"ts":"2026-08-20T12:00:00Z","level":"ERR","logger":"Component","message":"failed","exception":"boom"}`, false)
	if len(got) != 2 || !strings.Contains(got[0], "[ERR]") || !strings.Contains(got[0], "[Component]") || got[1] != "boom" {
		t.Fatalf("formatted = %#v", got)
	}
}

func TestFormatterDoesNotTreatNarrativeErrorAsLogLevel(t *testing.T) {
	formatter := Formatter{Color: true}
	message := "SoapFaultException: An error has occurred during report processing"
	got := formatter.Format("pod", "web", message, false)
	if len(got) != 1 || strings.Contains(got[0], "\x1b[31m") {
		t.Fatalf("narrative error was colored as an error-level record: %#v", got)
	}

	got = formatter.Format("pod", "web", "ERROR: report processing failed", false)
	if len(got) != 1 || !strings.Contains(got[0], "\x1b[31m") {
		t.Fatalf("leading error level was not colored: %#v", got)
	}
}

func TestNormalizeSinceDays(t *testing.T) {
	if got := NormalizeSince("4d"); got != "96h" {
		t.Fatalf("got %q", got)
	}
	duration, ok := ParseLogDuration("1.02:03:04")
	if !ok || FormatDuration(duration, true) != "1.02:03:04" {
		t.Fatalf("duration=%v ok=%t", duration, ok)
	}
}


func TestFormatterUsesSeverityHierarchyColors(t *testing.T) {
	formatter := Formatter{Color: true}
	tests := []struct {
		level string
		code  string
	}{
		{"ERR", "\x1b[31m"},
		{"WRN", "\x1b[33m"},
		{"INF", "\x1b[90m"},
	}
	for _, tt := range tests {
		line := formatter.Format("pod", "api", "[12:00:00 "+tt.level+"] message", false)
		if len(line) != 1 || !strings.Contains(line[0], tt.code) {
			t.Fatalf("%s line did not use expected severity color %q: %#v", tt.level, tt.code, line)
		}
	}
}
