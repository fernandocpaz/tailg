package core

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func radarRecord(at time.Time, workload, ip, path string, status int) LogRecord {
	msg := fmt.Sprintf("{\"RemoteIpAddress\":%q,\"RequestMethod\":\"GET\",\"RequestPath\":%q,\"StatusCode\":%d,\"Elapsed\":12}", ip, path, status)
	event := LogEvent{Workload: workload, Container: workload, Pod: workload + "-pod", Message: msg, ObservedAt: at}
	return LogRecord{Event: event, Fields: ParseLogFields(event)}
}

func radarFinding(snapshot SecurityRadarSnapshot, id, service, source string) (SecurityRadarFinding, bool) {
	for _, f := range snapshot.Findings {
		if f.ID == id && f.Service == service && f.Source == source {
			return f, true
		}
	}
	return SecurityRadarFinding{}, false
}

func TestSecurityRadarAuthBurstRequiresKnownSource(t *testing.T) {
	now := time.Date(2026, 11, 23, 17, 3, 20, 0, time.UTC)
	radar := NewSecurityRadar()
	for i := 0; i < 35; i++ {
		status := 401
		if i >= 27 {
			status = 200
		}
		if !radar.ObserveRecord(radarRecord(now.Add(-time.Duration(i%3)*time.Minute), "api", "192.0.2.5", "/login", status), now) {
			t.Fatal("HTTP completion rejected")
		}
	}
	for i := 0; i < 100; i++ {
		radar.ObserveRecord(radarRecord(now, "api", "", "/login", 401), now)
	}
	s := radar.Snapshot(now)
	f, found := radarFinding(s, "AUTH-BURST", "api", "192.0.2.5")
	if !found || f.Count != 27 || f.Requests != 35 || f.Severity != "MEDIUM" {
		t.Fatalf("auth burst: %+v; snapshot: %+v", f, s)
	}
	if _, found := radarFinding(s, "AUTH-BURST", "api", securityRadarUnknownSource); found {
		t.Fatal("unattributed auth failures assigned to fictitious actor")
	}
	if s.Unattributed != 100 || s.ObservedRequests != 135 {
		t.Fatalf("attribution coverage: %+v", s)
	}
}

func TestSecurityRadar404ScanGroupsByWorkload(t *testing.T) {
	now := time.Date(2026, 11, 23, 17, 3, 0, 0, time.UTC)
	radar := NewSecurityRadar()
	for i := 0; i < 30; i++ {
		radar.ObserveRecord(radarRecord(now, "orders", "2001:db8::5", fmt.Sprintf("/missing-%d", i), 404), now)
		radar.ObserveRecord(radarRecord(now, "checkout", "2001:db8::5", "/ok", 200), now)
	}
	s := radar.Snapshot(now)
	f, found := radarFinding(s, "PATH-SCAN", "orders", "2001:db8::5")
	if !found || f.DistinctPaths != 30 || f.Severity != "HIGH" {
		t.Fatalf("scan missing or wrong source: %+v; %+v", f, s.Findings)
	}
	if _, found := radarFinding(s, "PATH-SCAN", "checkout", "2001:db8::5"); found {
		t.Fatal("unrelated workload inherited scan")
	}
}

func TestSecurityRadarScansURLOnlyAndOmitsSecrets(t *testing.T) {
	now := time.Date(2026, 11, 23, 17, 3, 0, 0, time.UTC)
	radar := NewSecurityRadar()
	token := "do-not-retain-this-token"
	for i := 0; i < 5; i++ {
		radar.ObserveRecord(radarRecord(now, "api", "", "/search?x=%27%20OR%201%3D1--&token="+token, 400), now)
	}
	s := radar.Snapshot(now)
	f, found := radarFinding(s, "WEB-SQLI", "api", securityRadarUnknownSource)
	if !found || f.Count != 5 || f.Severity != "MEDIUM" {
		t.Fatalf("repeated unattributed path indicators missing: %+v", s)
	}
	if strings.Contains(fmt.Sprintf("%+v", s), token) {
		t.Fatal("raw URL query secret was retained in snapshot")
	}
	for i := 0; i < 5; i++ {
		if radar.ObserveRecord(LogRecord{Event: LogEvent{ObservedAt: now, Message: "SQL error union select from table"}}, now) {
			t.Fatal("app exception classified as incoming attack request")
		}
	}
	if radar.Snapshot(now).ObservedRequests != 5 {
		t.Fatal("non-request changed counts")
	}
}

func TestSecurityRadarReplaysAndWindowExpiry(t *testing.T) {
	now := time.Date(2026, 11, 23, 17, 3, 10, 0, time.UTC)
	radar := NewSecurityRadar()
	base := radarRecord(now, "api", "192.0.2.1", "/.env", 404)
	if !radar.ObserveRecord(base, now) {
		t.Fatal("base completion rejected")
	}
	for _, change := range []func(*LogRecord){
		func(r *LogRecord) { r.Event.Replayed = true },
		func(r *LogRecord) { r.Event.Closed = true },
		func(r *LogRecord) { r.Event.Started = true },
		func(r *LogRecord) { r.Event.ObservedAt = time.Time{} },
		func(r *LogRecord) { r.Event.ObservedAt = now.Add(-6*time.Minute) },
		func(r *LogRecord) { r.Event.ObservedAt = now.Add(time.Minute) },
	} {
		record := base
		change(&record)
		if radar.ObserveRecord(record, now) {
			t.Fatalf("ineligible record counted: %+v", record.Event)
		}
	}
	if got := radar.Snapshot(now).ObservedRequests; got != 1 {
		t.Fatalf("replay count %d", got)
	}
	if got := radar.Snapshot(now.Add(5*time.Minute)).ObservedRequests; got != 0 {
		t.Fatalf("expired count %d", got)
	}
}

func TestSecurityRadarCapsSourcesAndPaths(t *testing.T) {
	now := time.Date(2026, 11, 23, 17, 3, 0, 0, time.UTC)
	radar := NewSecurityRadar()
	for i := 1; i <= 120; i++ {
		radar.ObserveRecord(radarRecord(now, "api", fmt.Sprintf("198.51.100.%d", i), "/missing", 404), now)
	}
	s := radar.Snapshot(now)
	if s.ObservedRequests != 120 || s.Dropped != 120-securityRadarMaxSources {
		t.Fatalf("unreported source overflow: %+v", s)
	}
	for i := 0; i < 300; i++ {
		radar.ObserveRecord(radarRecord(now.Add(time.Minute), "api", "203.0.113.44", fmt.Sprintf("/path-%d", i), 404), now.Add(time.Minute))
	}
	for _, bucket := range radar.buckets {
		if len(bucket.sources) > securityRadarMaxSources {
			t.Fatal("source cardinality exceeded")
		}
		for _, source := range bucket.sources {
			if len(source.paths404) > securityRadarMax404Paths {
				t.Fatal("path cardinality exceeded")
			}
		}
	}
}

func TestClientIPParsingNeverTrustsForwardedHeader(t *testing.T) {
	for _, raw := range []string{
		"{\"RequestMethod\":\"GET\",\"RequestPath\":\"/login\",\"StatusCode\":401,\"X-Forwarded-For\":\"192.0.2.77\"}",
		"{\"RemoteIpAddress\":\"192.0.2.1, 198.51.100.4\"}",
	} {
		if got := ParseLogFields(LogEvent{Message: raw}).ClientIP; got != "" {
			t.Fatalf("invalid/spoofable source accepted: %q", got)
		}
	}
	for _, tc := range []struct{ raw, want string }{
		{"{\"RemoteIpAddress\":\"192.0.2.8:4512\"}", "192.0.2.8"},
		{"{\"remote_addr\":\"[2001:db8::1]:4444\"}", "2001:db8::1"},
		{"{\"client\":{\"ip\":\"::ffff:192.0.2.9\"}}", "192.0.2.9"},
	} {
		if got := ParseLogFields(LogEvent{Message: tc.raw}).ClientIP; got != tc.want {
			t.Fatalf("source = %q, want %q", got, tc.want)
		}
	}
}
