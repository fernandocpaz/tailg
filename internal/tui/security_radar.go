package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/fernandocpaz/tailg/internal/core"
)

func (m model) renderSecurityRadar() string {
	snapshot := m.securityRadar.Snapshot(time.Now())
	lines := []string{
		"PASSIVE / LOCAL · rolling 5 calendar-minute buckets · log-derived HTTP completions",
		"No cluster writes, remote API calls, or retained request URLs/bodies.",
		"",
		fmt.Sprintf("HTTP completions: %d  |  attributed sources: %d  |  missing source: %d  |  cardinality overflow: %d",
			snapshot.ObservedRequests, snapshot.TrackedSources, snapshot.Unattributed, snapshot.Dropped),
		"Source IPs come only from server-emitted remote-address fields; forwarded headers are not used.",
		"Traffic may be sampled, duplicated by upstream logging, or missing after restart/rotation.",
		"",
	}
	if snapshot.Dropped > 0 || len(m.reconnecting) > 0 {
		lines = append(lines, "INCOMPLETE: source group cap reached or log streams reconnecting. Findings may undercount.")
	}
	if len(snapshot.Findings) == 0 {
		lines = append(lines, "No multi-request indicators exceeded the configured thresholds in this window.")
		lines = append(lines, "This does not establish the absence of attacks. Single-log inspection: select a log and press S.")
	} else {
		lines = append(lines, fmt.Sprintf("Potential patterns (%d shown) — heuristic indicators, not confirmed attacks:", len(snapshot.Findings)))
		for i, finding := range snapshot.Findings {
			if i >= max(1, m.height-12) {
				lines = append(lines, fmt.Sprintf("... %d more finding(s)", len(snapshot.Findings)-i))
				break
			}
			service := truncatePlain(core.StripANSI(finding.Service), 20)
			source := truncatePlain(core.StripANSI(finding.Source), 36)
			line := fmt.Sprintf("[%s] %s  %s  %d hits | %s | %s",
				finding.Severity, finding.ID, service, finding.Count, source, finding.Description)
			if finding.DistinctPaths > 0 {
				line += fmt.Sprintf(" (%d paths)", finding.DistinctPaths)
			}
			if m.config.Formatter.Color && finding.Severity == "HIGH" {
				line = renderWithColor(alertStyle, line, true)
			} else if m.config.Formatter.Color && finding.Severity == "MEDIUM" {
				line = renderWithColor(warnStyle, line, true)
			}
			lines = append(lines, line)
		}
	}
	for i := range lines {
		lines[i] = truncate(lines[i], m.width)
	}
	return m.panel("Stream security radar (F10)", strings.Join(lines, "\n"),
		"F10/Esc closes | S inspects one selected entry | Source attribution requires validated server remote-IP metadata")
}
