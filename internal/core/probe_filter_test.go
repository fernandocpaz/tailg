package core

import (
	"strings"
	"testing"
)

func TestDefaultProbeFilterPreservesHealthCheckFailures(t *testing.T) {
	defaults, err := CompilePatterns(DefaultExcludePatterns)
	if err != nil {
		t.Fatal(err)
	}
	formatter := Formatter{DefaultExclude: defaults, Color: true}

	tests := []struct {
		name    string
		message string
		level   string
		visible bool
	}{
		{"healthy check", "INFO [09:21:34.000] [health check] AuthService is healthy", "INF", false},
		{"normal ready probe", "[09:21:34 INF] HTTP GET /ready responded 200", "INF", false},
		{"failed ready probe", "[09:21:35 ERR] HTTP GET /ready responded 503", "ERR", true},
		{"failed authorization", `ERROR [09:21:35.286] [health check] GetTokenWithApplicationKey => POST https://d1qwynap01.vitas.com:19030/oauth/token responded 401 . Body: {"timestamp":"2026-10-23T13:21:35.260+00:00","status":401,"error":"Unauthorized","path":"/oauth/token"}`, "ERR", true},
		{"failed service", `ERROR [09:21:35.291] [health check] RR AuthService token request failed. Url: https://d1qwynap01.vitas.com:19030/ Mode: ApplicationKey Reason: GetTokenWithApplicationKey => POST https://d1qwynap01.vitas.com:19030/oauth/token responded 401`, "ERR", true},
		{"fatal service", "FATAL [09:21:35.291] [health check] RentalResultsAuthService is not healthy. Service halted.", "ERR", true},
		{"warning check", "WARNING [09:21:35.300] [health check] RR AuthService degraded", "WRN", true},
		{"healthy JSON check", `{"level":"Information","message":"health check passed"}`, "INF", false},
		{"failed JSON check", `{"level":"Error","message":"health check failed"}`, "ERR", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			event := LogEvent{Pod: "rr-pod", Container: "rr", Message: tt.message}
			records := RecordsForEvent(formatter, event, false)
			if (len(records) > 0) != tt.visible {
				t.Fatalf("visible=%t, want %t; records=%+v", len(records) > 0, tt.visible, records)
			}
			if level := ParseLogFields(event).Level; level != tt.level {
				t.Fatalf("level=%q, want %q", level, tt.level)
			}
			if tt.visible && tt.level == "ERR" {
				if !strings.Contains(records[0].Text, colors["red"]) {
					t.Fatalf("error not red: %q", records[0].Text)
				}
				if IsLogContinuation(tt.message) {
					t.Fatal("error treated as a continuation")
				}
			}
		})
	}

	// Suppressing a custom class of messages must still be possible.
	explicit, err := CompilePatterns([]string{"AuthService"})
	if err != nil {
		t.Fatal(err)
	}
	formatter.Exclude = explicit
	if rows := formatter.Format("rr-pod", "rr", "FATAL [09:21:35.291] [health check] RentalResultsAuthService halted", false); len(rows) != 0 {
		t.Fatalf("explicit --exclude did not win: %v", rows)
	}
	if rows := (Formatter{}).Format("rr-pod", "rr", "INFO [09:21:34.000] [health check] healthy", false); len(rows) == 0 {
		t.Fatal("disabling default exclusions still hides probe logs")
	}
}

func TestHealthCheckErrorBodyIsPreserved(t *testing.T) {
	message := `ERROR [09:21:35.286] [health check] POST https://d1qwynap01.vitas.com:19030/oauth/token responded 401 . Body: {"timestamp":"2026-10-23T13:21:35.260+00:00","status":401,"error":"Unauthorized","path":"/oauth/token"}`
	records := RecordsForEvent(Formatter{}, LogEvent{Message: message}, false)
	if len(records) != 1 || !strings.Contains(records[0].Text, `"error":"Unauthorized"`) || !strings.Contains(records[0].Text, `"status":401`) {
		t.Fatalf("JSON response body was lost: %+v", records)
	}
}

func TestHealthCheckFailuresRemainSearchableAndInErrorHistory(t *testing.T) {
	messages := []string{
		"INFO [09:21:34.000] [health check] AuthService healthy",
		"ERROR [09:21:35.286] [health check] AuthService responded 401",
		"ERROR [09:21:35.291] [health check] Token request failed",
		"FATAL [09:21:35.291] [health check] Service halted",
	}
	events := make([]LogEvent, len(messages))
	for i, msg := range messages {
		events[i] = LogEvent{Pod: "rr-pod", Container: "rr", Message: msg}
	}
	blocks := ErrorLogBlocks(events)
	if len(blocks) != 3 {
		t.Fatalf("error history entries=%d, want 3", len(blocks))
	}
	query, err := CompileLogQuery("level:error")
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i < len(events); i++ {
		records := RecordsForEvent(Formatter{}, events[i], false)
		if len(records) != 1 || !query.Matches(records[0]) {
			t.Fatalf("failed level:error query for %q", messages[i])
		}
	}
}
