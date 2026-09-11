package core

import (
	"testing"
	"time"
)

func TestParseLogFieldsSerilogHTTP(t *testing.T) {
	event := LogEvent{Container: "orders", Message: `[09:47:32 INF] [11d7729cbf34122f3af8d3c73a47213d] HTTP GET /orders responded 200 in 300.3415 ms {"SourceContext":"Orders.Api","Elapsed":300.3415,"RequestId":"req-7"}`}
	fields := ParseLogFields(event)
	if fields.Level != "INF" || fields.Service != "orders" || fields.TraceID != "11d7729cbf34122f3af8d3c73a47213d" || fields.RequestID != "req-7" {
		t.Fatalf("fields identity = %#v", fields)
	}
	if fields.Method != "GET" || fields.Path != "/orders" || fields.StatusCode != 200 {
		t.Fatalf("fields http = %#v", fields)
	}
	if !fields.HasDuration || fields.Duration != 300341500*time.Nanosecond {
		t.Fatalf("fields duration = %v (%v)", fields.Duration, fields.HasDuration)
	}
}

func TestParseLogFieldsJSONPropertiesAndTraceparent(t *testing.T) {
	event := LogEvent{Container: "worker", Message: `{"@l":"Error","@m":"HTTP POST /orders responded 503 in 3 ms","traceparent":"00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01","Properties":{"service.name":"checkout","RequestMethod":"POST","RequestPath":"/orders","StatusCode":503,"Elapsed":1.25,"SpanId":"1111111111111111"}}`}
	fields := ParseLogFields(event)
	if fields.Level != "ERR" || fields.Service != "checkout" || fields.TraceID != "4bf92f3577b34da6a3ce929d0e0e4736" || fields.SpanID != "1111111111111111" {
		t.Fatalf("fields identity = %#v", fields)
	}
	if fields.Method != "POST" || fields.Path != "/orders" || fields.StatusCode != 503 || fields.Duration != 1250*time.Microsecond {
		t.Fatalf("fields http = %#v", fields)
	}
}

func TestParseLogFieldsRejectsBadTraceAndElapsed(t *testing.T) {
	for _, message := range []string{
		`{"TraceId":"00000000000000000000000000000000","Elapsed":-1}`,
		`{"traceparent":"00-00000000000000000000000000000000-00f067aa0ba902b7-01","Elapsed":"NaN"}`,
	} {
		fields := ParseLogFields(LogEvent{Container: "api", Message: message})
		if fields.TraceID != "" || fields.HasDuration {
			t.Fatalf("bad values parsed from %s: %#v", message, fields)
		}
	}
}

func TestValidTraceIDValidatesW3CTraceparentFields(t *testing.T) {
	valid := "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	if got := validTraceID(valid); got != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Fatalf("valid traceparent = %q", got)
	}
	for _, value := range []string{
		"0g-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
		"00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-0g",
		"00-4bf92f3577b34da6a3ce929d0e0e4736-0000000000000000-01",
		"ff-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
	} {
		if got := validTraceID(value); got != "" {
			t.Errorf("validTraceID(%q) = %q, want empty", value, got)
		}
	}
}

func TestParseLogFieldsPrefersExplicitFieldsToNestedPayload(t *testing.T) {
	event := LogEvent{Container: "api", Message: `{"level":"Info","status":201,"path":"/explicit","Properties":{"Level":"Error","StatusCode":503,"RequestPath":"/properties"},"payload":{"level":"Error","status":500,"path":"/payload"}}`}
	fields := ParseLogFields(event)
	if fields.Level != "INF" || fields.StatusCode != 201 || fields.Path != "/explicit" {
		t.Fatalf("explicit fields were shadowed: %#v", fields)
	}
}

func TestParseLogFieldsOnlyUsesLeadingPlainLevel(t *testing.T) {
	if fields := ParseLogFields(LogEvent{Message: "error=0"}); fields.Level != "" {
		t.Fatalf("counter was parsed as a level: %#v", fields)
	}
	if fields := ParseLogFields(LogEvent{Message: "Error: database unavailable"}); fields.Level != "ERR" {
		t.Fatalf("leading level was not parsed: %#v", fields)
	}
}
