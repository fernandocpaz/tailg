package core

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	SecurityRadarWindow        = 5 * time.Minute
	securityRadarMinutes       = 5
	securityRadarMaxSources    = 96 // per minute, including each unattributed service
	securityRadarMax404Paths   = 48 // per source and minute
	securityRadarMaxFindings   = 20
	securityRadarUnknownSource = "(unattributed)"
)

type securityRadarSource struct {
	service, source string
	requests        int
	authFailures    int
	notFound        int
	paths404        map[string]struct{}
	indicators      map[string]int
}

type securityRadarBucket struct {
	minute    int64
	seen      int
	dropped   int
	unknown   int
	sources   map[string]*securityRadarSource
}

// SecurityRadar retains only bounded aggregate counters for recent HTTP
// completion logs. It does not store message text, URL query values, request
// bodies, credentials, or raw response payloads.
type SecurityRadar struct {
	buckets [securityRadarMinutes]securityRadarBucket
}

type SecurityRadarFinding struct {
	ID            string
	Severity      string
	Title         string
	Service       string
	Source        string // validated address from a server-emitted field, or "(unattributed)"
	Count         int
	Requests      int
	DistinctPaths int
	Description   string
}

type SecurityRadarSnapshot struct {
	Window          time.Duration
	ObservedRequests int // accepted HTTP completions (including capacity-overflow groups)
	Unattributed    int
	Dropped         int // completions whose source cardinality exceeded the cap
	TrackedSources  int
	Findings        []SecurityRadarFinding
}

func NewSecurityRadar() *SecurityRadar { return &SecurityRadar{} }

// ObserveRecord uses the fields parsed once for display; it intentionally
// analyzes only the HTTP request path, not application exception messages.
func (r *SecurityRadar) ObserveRecord(record LogRecord, now time.Time) bool {
	if r == nil {
		return false
	}
	event := record.Event
	if event.Started || event.Closed || event.Replayed || event.Err != nil || event.ObservedAt.IsZero() {
		return false
	}
	minute := event.ObservedAt.UTC().Truncate(time.Minute)
	current := now.UTC().Truncate(time.Minute)
	if minute.After(current) || minute.Before(current.Add(-(securityRadarMinutes-1)*time.Minute)) {
		return false
	}
	fields := record.Fields
	method := normalizeHTTPMethod(fields.Method)
	path := normalizeEndpoint(fields.Path)
	if method == "" || path == "" || (fields.StatusCode < 100 && !fields.HasDuration) {
		return false
	}
	service := strings.TrimSpace(event.Workload)
	if service == "" {
		service = strings.TrimSpace(event.Container)
	}
	if service == "" {
		service = "(unknown workload)"
	}
	source := validLogClientIP(fields.ClientIP)
	if source == "" {
		source = securityRadarUnknownSource
	}
	index := int((minute.Unix() / 60) % securityRadarMinutes)
	if index < 0 {
		index += securityRadarMinutes
	}
	bucket := &r.buckets[index]
	if bucket.sources == nil || bucket.minute != minute.Unix() {
		*bucket = securityRadarBucket{
			minute: minute.Unix(), sources: make(map[string]*securityRadarSource),
		}
	}
	bucket.seen++
	if source == securityRadarUnknownSource {
		bucket.unknown++
	}
	key := service + "\x00" + source
	counter := bucket.sources[key]
	if counter == nil {
		if len(bucket.sources) == securityRadarMaxSources {
			bucket.dropped++
			return true
		}
		counter = &securityRadarSource{
			service: service, source: source,
			paths404: make(map[string]struct{}),
			indicators: make(map[string]int),
		}
		bucket.sources[key] = counter
	}
	counter.requests++
	switch fields.StatusCode {
	case 401, 403:
		counter.authFailures++
	case 404:
		counter.notFound++
		// Limit cardinality even when URLs contain attacker-controlled IDs.
		// Query strings are dropped before the key is retained.
		endpoint := endpointFingerprint(path)
		if len(endpoint) > 160 {
			endpoint = endpoint[:160]
		}
		if len(counter.paths404) < securityRadarMax404Paths {
			counter.paths404[endpoint] = struct{}{}
		}
	}
	// Reuse the single-event pattern definitions but supply path-only input.
	// Scanning exception text here would incorrectly classify SQL statements
	// written by the application as incoming attacks.
	assessment := AssessLogSecurity(LogRecord{
		Event: LogEvent{},
		Fields: LogFields{Method: method, Path: fields.Path, StatusCode: fields.StatusCode},
	})
	for _, finding := range assessment.Findings {
		counter.indicators[finding.ID]++
	}
	return true
}

// Snapshot is read-only. Counters are exact for accepted completion logs;
// absence of source attribution prevents actor-level auth and scan claims.
func (r *SecurityRadar) Snapshot(now time.Time) SecurityRadarSnapshot {
	out := SecurityRadarSnapshot{Window: SecurityRadarWindow}
	if r == nil {
		return out
	}
	merged := make(map[string]*securityRadarSource)
	current := now.UTC().Truncate(time.Minute)
	for i := 0; i < securityRadarMinutes; i++ {
		minute := current.Add(-time.Duration(i) * time.Minute)
		index := int((minute.Unix() / 60) % securityRadarMinutes)
		if index < 0 {
			index += securityRadarMinutes
		}
		bucket := &r.buckets[index]
		if bucket.sources == nil || bucket.minute != minute.Unix() {
			continue
		}
		out.ObservedRequests += bucket.seen
		out.Unattributed += bucket.unknown
		out.Dropped += bucket.dropped
		for key, s := range bucket.sources {
			total := merged[key]
			if total == nil {
				total = &securityRadarSource{service: s.service, source: s.source,
					paths404: make(map[string]struct{}), indicators: make(map[string]int)}
				merged[key] = total
			}
			total.requests += s.requests
			total.authFailures += s.authFailures
			total.notFound += s.notFound
			for path := range s.paths404 {
				if len(total.paths404) >= securityRadarMax404Paths {
					break
				}
				total.paths404[path] = struct{}{}
			}
			for id, count := range s.indicators {
				total.indicators[id] += count
			}
		}
	}
	for _, s := range merged {
		if s.source != securityRadarUnknownSource {
			out.TrackedSources++
			if s.requests >= 30 && s.authFailures >= 20 && 100*s.authFailures >= 60*s.requests {
				risk := "MEDIUM"
				if s.authFailures >= 50 {
					risk = "HIGH"
				}
				out.Findings = append(out.Findings, SecurityRadarFinding{
					ID: "AUTH-BURST", Severity: risk, Title: "Repeated authorization failures",
					Service: s.service, Source: s.source, Count: s.authFailures, Requests: s.requests,
					Description: "20+ HTTP 401/403 responses from the logged source and at least 60% of its observed completions. Verify client context and authentication flows.",
				})
			}
			if s.notFound >= 20 && len(s.paths404) >= 12 && 2*s.notFound >= s.requests {
				risk := "MEDIUM"
				if len(s.paths404) >= 25 {
					risk = "HIGH"
				}
				out.Findings = append(out.Findings, SecurityRadarFinding{
					ID: "PATH-SCAN", Severity: risk, Title: "Possible endpoint enumeration",
					Service: s.service, Source: s.source, Count: s.notFound, Requests: s.requests,
					DistinctPaths: len(s.paths404),
					Description: "20+ HTTP 404 responses across 12+ distinct normalized paths from the logged source. Check against expected crawlers and API discovery.",
				})
			}
		}
		for id, count := range s.indicators {
			threshold := 3
			risk := "HIGH"
			if id == "WEB-PROBE" || id == "WEB-METHOD" {
				threshold, risk = 5, "MEDIUM"
			}
			if s.source == securityRadarUnknownSource {
				threshold = max(threshold, 5)
				risk = "MEDIUM"
			}
			if count < threshold {
				continue
			}
			out.Findings = append(out.Findings, SecurityRadarFinding{
				ID: id, Severity: risk, Title: securityRadarTitle(id),
				Service: s.service, Source: s.source, Count: count, Requests: s.requests,
				Description: "Repeated URL-path indicators. This is an aggregated heuristic, not verification of an attack or a single actor.",
			})
		}
	}
	sort.Slice(out.Findings, func(i, j int) bool {
		a, b := out.Findings[i], out.Findings[j]
		if a.Severity != b.Severity {
			return a.Severity == "HIGH"
		}
		if a.Count != b.Count {
			return a.Count > b.Count
		}
		return a.Service+"\x00"+a.Source+"\x00"+a.ID < b.Service+"\x00"+b.Source+"\x00"+b.ID
	})
	if len(out.Findings) > securityRadarMaxFindings {
		out.Findings = out.Findings[:securityRadarMaxFindings]
	}
	return out
}

func securityRadarTitle(id string) string {
	switch id {
	case "WEB-SQLI":
		return "Repeated SQL injection-like URLs"
	case "WEB-TRAVERSAL":
		return "Repeated path traversal URLs"
	case "WEB-XSS":
		return "Repeated script-like URLs"
	case "WEB-COMMAND":
		return "Repeated command injection-like URLs"
	case "WEB-PROBE":
		return "Repeated sensitive endpoint probes"
	case "WEB-METHOD":
		return "Repeated unusual HTTP methods"
	default:
		return fmt.Sprintf("Repeated %s indicators", id)
	}
}
