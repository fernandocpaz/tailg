package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/fernandocpaz/tailg/internal/core"
)

// renderAnalytics intentionally presents log-derived estimates rather than
// claiming authoritative traffic statistics or service-level indicators.
func (m model) renderAnalytics() string {
	snapshot := m.analytics.Snapshot(time.Now())
	counts := snapshot.Total
	var lines []string
	lines = append(lines, "Observed HTTP completion logs | last 15 calendar-minute buckets")
	lines = append(lines, "Not all requests are necessarily logged. Health/probe and source filters can affect coverage.")
	lines = append(lines, "")
	lines = append(lines, fmt.Sprintf("Requests: %-8d  4xx: %-8d  5xx: %-8d  Slow (>250ms): %d",
		counts.Requests, counts.ClientErrors, counts.ServerErrors, counts.Slow))
	lines = append(lines, fmt.Sprintf("Observed 5xx rate: %-8s  4xx rate: %-8s  Approx p50/p95/p99: %s / %s / %s",
		analyticsPercent(counts.ServerErrors, counts.StatusKnown),
		analyticsPercent(counts.ClientErrors, counts.StatusKnown),
		analyticsDuration(counts, .50), analyticsDuration(counts, .95), analyticsDuration(counts, .99)))
	lines = append(lines, fmt.Sprintf("Last 5m: %.1f req/min (%d)  |  Previous 5m: %.1f req/min (%d)  |  Timed: %d/%d",
		float64(snapshot.LastFive.Requests)/5, snapshot.LastFive.Requests,
		float64(snapshot.PreviousFive.Requests)/5, snapshot.PreviousFive.Requests,
		counts.Timed, counts.Requests))
	lines = append(lines, fmt.Sprintf("HTTP status coverage: %d/%d observed completions", counts.StatusKnown, counts.Requests))
	lines = append(lines, analyticsErrorTrend(snapshot.LastFive, snapshot.PreviousFive))
	lines = append(lines, "Requests/min trend: "+analyticsSparkline(snapshot.Minutes, false))
	lines = append(lines, "HTTP 5xx/min trend: "+analyticsSparkline(snapshot.Minutes, true))
	lines = append(lines, "")
	lines = append(lines, "Top observed endpoints | method  path  completions  5xx  ~p95")
	if len(snapshot.Endpoints) == 0 {
		lines = append(lines, "  No parsed HTTP completions in this window.")
	} else {
		for i, endpoint := range snapshot.Endpoints {
			if i >= 8 {
				lines = append(lines, fmt.Sprintf("  +%d further endpoint groups", len(snapshot.Endpoints)-i))
				break
			}
			lines = append(lines, fmt.Sprintf("  %-5s %-43s %6d %5d %8s",
				endpoint.Method, truncatePlain(endpoint.Path, 43),
				endpoint.Counts.Requests, endpoint.Counts.ServerErrors,
				analyticsDuration(endpoint.Counts, .95)))
		}
	}
	if len(m.reconnecting) > 0 {
		lines = append(lines, "WARNING: some log streams are reconnecting; traffic coverage may be incomplete.")
	}
	for i := range lines {
		lines[i] = truncatePlain(lines[i], m.width)
	}
	return m.panel("HTTP analytics (F9)", strings.Join(lines, "\n"), "F9/Esc closes | Estimates from timestamped log completions; percentile bounds are approximate.")
}

// analyticsErrorTrend deliberately needs enough status samples in both
// windows. Sparse or incomplete log streams cannot establish an anomaly.
func analyticsErrorTrend(recent, previous core.AnalyticsCounts) string {
	if recent.StatusKnown < 30 || previous.StatusKnown < 30 {
		return "5xx trend: insufficient HTTP status samples for comparison"
	}
	currentRate := 100 * float64(recent.ServerErrors) / float64(recent.StatusKnown)
	priorRate := 100 * float64(previous.ServerErrors) / float64(previous.StatusKnown)
	if recent.ServerErrors >= 5 && currentRate >= 5 && currentRate-priorRate >= 5 && (priorRate == 0 || currentRate >= 2*priorRate) {
		return fmt.Sprintf("Possible 5xx spike: %.1f%% in recent 5m vs %.1f%% before (log-derived)", currentRate, priorRate)
	}
	return fmt.Sprintf("5xx rate trend: %.1f%% recent vs %.1f%% previous (no strong spike)", currentRate, priorRate)
}

func analyticsPercent(numerator, denominator int) string {
	if denominator <= 0 {
		return "n/a"
	}
	return fmt.Sprintf("%.1f%%", 100*float64(numerator)/float64(denominator))
}

func analyticsDuration(counts core.AnalyticsCounts, percentile float64) string {
	if counts.Timed == 0 {
		return "n/a"
	}
	return "~" + counts.ApproxPercentile(percentile).String()
}

func analyticsSparkline(minutes []core.AnalyticsMinute, errorsOnly bool) string {
	const blocks = "▁▂▃▄▅▆▇█"
	values := make([]int, len(minutes))
	var maximum int
	for i, minute := range minutes {
		value := minute.Counts.Requests
		if errorsOnly {
			value = minute.Counts.ServerErrors
		}
		values[i] = value
		if value > maximum {
			maximum = value
		}
	}
	if maximum == 0 {
		return strings.Repeat("·", len(minutes)) + " (none observed)"
	}
	var result strings.Builder
	levels := []rune(blocks)
	for _, value := range values {
		if value == 0 {
			result.WriteRune('·')
		} else {
			level := (value * (len(levels) - 1)) / maximum
			result.WriteRune(levels[level])
		}
	}
	return result.String() + fmt.Sprintf(" (peak %d/min, oldest → newest)", maximum)
}
