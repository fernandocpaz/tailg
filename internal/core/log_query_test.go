package core

import (
	"testing"
	"time"
)

func TestCompileLogQueryPredicatesAndLiteral(t *testing.T) {
	query, err := CompileLogQuery(`level:error service:api pod:foo trace:4bf92f3577b34da6a3ce929d0e0e4736 status:>=500 duration:>250ms method:GET path:/orders "upstream failed"`)
	if err != nil {
		t.Fatal(err)
	}
	record := LogRecord{Text: "09:47:32 upstream failed", Event: LogEvent{Pod: "orders-foo-1"}, Fields: LogFields{
		Level: "ERR", Service: "api-v2", TraceID: "4bf92f3577b34da6a3ce929d0e0e4736", StatusCode: 503,
		Duration: 300 * time.Millisecond, HasDuration: true, Method: "GET", Path: "/orders/123",
	}}
	if !query.Matches(record) || query.Highlight() != "upstream failed" {
		t.Fatalf("query did not match; highlight=%q", query.Highlight())
	}
	record.Fields.Duration = 250 * time.Millisecond
	if query.Matches(record) {
		t.Fatal("strict duration predicate matched equal value")
	}
}

func TestCompileLogQueryLegacyLiteralAndQuotes(t *testing.T) {
	query, err := CompileLogQuery(`HTTP GET /orders?state=paid`)
	if err != nil || query.Highlight() != `HTTP GET /orders?state=paid` {
		t.Fatalf("legacy query = %#v, %v", query, err)
	}
	if !query.Matches(LogRecord{Text: "prefix HTTP GET /orders?state=paid suffix"}) {
		t.Fatal("legacy literal did not match")
	}
	quoted, err := CompileLogQuery(`service:api "HTTP 500, retrying"`)
	if err != nil || quoted.Highlight() != "HTTP 500, retrying" {
		t.Fatalf("quoted query = %#v, %v", quoted, err)
	}
}

func TestCompileLogQueryInvalidRecognizedExpressions(t *testing.T) {
	for _, text := range []string{"status:wat", "duration:NaN", "duration:-1ms", `service:"unterminated`} {
		if _, err := CompileLogQuery(text); err == nil {
			t.Errorf("CompileLogQuery(%q) returned nil error", text)
		}
	}
	query, err := CompileLogQuery("foo:bar")
	if err != nil || query.Highlight() != "foo:bar" {
		t.Fatalf("unknown field should remain literal: %#v, %v", query, err)
	}
}

func TestCompileLogQueryPreservesLegacyJSONLiteralQuotes(t *testing.T) {
	query, err := CompileLogQuery(`{"level":"Error","message":"upstream failed"}`)
	if err != nil || query.Highlight() != `{"level":"Error","message":"upstream failed"}` {
		t.Fatalf("JSON literal query = %#v, %v", query, err)
	}
	if !query.Matches(LogRecord{Text: `prefix {"level":"Error","message":"upstream failed"} suffix`}) {
		t.Fatal("JSON literal did not match")
	}

	quoted, err := CompileLogQuery(`"upstream failed"`)
	if err != nil || quoted.Highlight() != "upstream failed" || !quoted.Matches(LogRecord{Text: "prefix upstream failed suffix"}) {
		t.Fatalf("quoted phrase behavior changed: %#v, %v", quoted, err)
	}
}

func TestCompileLogQueryPreservesPlainWhitespace(t *testing.T) {
	query, err := CompileLogQuery("request  completed\twith  spaces")
	if err != nil || query.Highlight() != "request  completed\twith  spaces" {
		t.Fatalf("plain literal = %#v, %v", query, err)
	}
	if !query.Matches(LogRecord{Text: "request  completed\twith  spaces"}) {
		t.Fatal("plain literal with repeated whitespace did not match")
	}
}

func TestCompileLogQueryRejectsDurationMillisecondOverflow(t *testing.T) {
	if _, err := CompileLogQuery("duration:9223372036855"); err == nil {
		t.Fatal("duration millisecond overflow was accepted")
	}
}
