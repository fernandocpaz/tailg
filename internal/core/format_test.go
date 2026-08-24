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

func TestFormatterParsesStructuredJSON(t *testing.T) {
	formatter := Formatter{}
	got := formatter.Format("pod", "web", `{"ts":"2026-08-20T12:00:00Z","level":"ERR","logger":"Component","message":"failed","exception":"boom"}`, false)
	if len(got) != 2 || !strings.Contains(got[0], "[ERR]") || !strings.Contains(got[0], "[Component]") || got[1] != "boom" {
		t.Fatalf("formatted = %#v", got)
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
