package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"fmt"
	"strings"
	"time"

	"github.com/fernandocpaz/tailg/internal/core"
)

// renderAnalytics draws a bounded in-terminal chart dashboard. Missing HTTP
// status and latency samples are never represented as known zero values.
func (m model) renderAnalytics() string {
	period:=m.analyticsWindowMinutes
	if period!=5 {period=15}
	snapshot:=m.analytics.SnapshotWindow(time.Now(),period)
	counts:=snapshot.Total
	chartMode:="focused"
	if m.analyticsGrid && m.width>=100 && m.height>=28 && !m.analyticsEndpointExpanded {
		chartMode="grid"
	}
	lines:=[]string{
		fmt.Sprintf("Observed HTTP completion logs · %d calendar-minute buckets · %s view",period,chartMode),
		fmt.Sprintf("Requests: %-8d  4xx: %-8d  5xx: %-8d  Slow (>250ms): %d",
			counts.Requests,counts.ClientErrors,counts.ServerErrors,counts.Slow),
		fmt.Sprintf("Observed 5xx rate: %-8s  4xx rate: %-8s  Approx p50/p95/p99: %s / %s / %s",
			analyticsPercent(counts.ServerErrors,counts.StatusKnown),
			analyticsPercent(counts.ClientErrors,counts.StatusKnown),
			analyticsDuration(counts,.50),analyticsDuration(counts,.95),analyticsDuration(counts,.99)),
		fmt.Sprintf("Last 5m: %.1f req/min (%d)  |  Previous 5m: %.1f req/min (%d)  |  Timed: %d/%d",
			float64(snapshot.LastFive.Requests)/5,snapshot.LastFive.Requests,
			float64(snapshot.PreviousFive.Requests)/5,snapshot.PreviousFive.Requests,
			counts.Timed,counts.Requests),
		fmt.Sprintf("HTTP status coverage: %d/%d · observed log completions, NOT all incoming traffic",
			counts.StatusKnown,counts.Requests),
		analyticsErrorTrend(snapshot.LastFive,snapshot.PreviousFive),
	}
	if m.width<80 {
		lines=[]string{
			fmt.Sprintf("Observed HTTP completion logs | %dm",period),
			fmt.Sprintf("Requests: %d  4xx: %d  5xx: %d  Slow: %d",
				counts.Requests,counts.ClientErrors,counts.ServerErrors,counts.Slow),
			fmt.Sprintf("4xx rate: %s | 5xx rate: %s",
				analyticsPercent(counts.ClientErrors,counts.StatusKnown),
				analyticsPercent(counts.ServerErrors,counts.StatusKnown)),
			fmt.Sprintf("Approx p50/p95/p99: %s / %s / %s",
				analyticsDuration(counts,.50),analyticsDuration(counts,.95),analyticsDuration(counts,.99)),
			fmt.Sprintf("HTTP status coverage: %d/%d | timed: %d/%d",
				counts.StatusKnown,counts.Requests,counts.Timed,counts.Requests),
			analyticsErrorTrend(snapshot.LastFive,snapshot.PreviousFive),
		}
	}
	if len(m.reconnecting)>0 {
		lines=append(lines,"INCOMPLETE: log streams reconnecting; request coverage may undercount")
	}
	if !m.analyticsEndpointExpanded {
		if chartMode=="grid" {
			lines=append(lines,renderAnalyticsChartGrid(snapshot,period,m.width,m.height,m.config.Formatter.Color)...)
		} else {
			metric:=analyticsChartMetric(m.analyticsMetric)
			if metric<analyticsThroughput||metric>=analyticsChartCount {
				metric=analyticsThroughput
			}
			plotHeight:=min(10,max(5,m.height-17))
			lines=append(lines,renderAnalyticsTimeChart(snapshot,period,metric,max(24,m.width),plotHeight,m.config.Formatter.Color)...)
		}
	}
	lines=append(lines,"")
	lines=append(lines,"Top observed endpoints | method  path  completions  5xx  ~p95")
	if len(snapshot.Endpoints)==0 {
		lines=append(lines,"  No parsed HTTP completions in this window.")
	} else {
		// Expanded endpoint mode makes the full bounded list scrollable.
		available:=max(0,(m.height-3)-len(lines))
		maxShown:=3
		start:=0
		if m.analyticsEndpointExpanded {
			start=min(max(0,m.analyticsEndpointOffset),len(snapshot.Endpoints)-1)
			maxShown=max(1,available-1)
		} else {
			maxShown=min(3,available)
		}
		end:=min(len(snapshot.Endpoints),start+maxShown)
		for _,endpoint:=range snapshot.Endpoints[start:end] {
			lines=append(lines,fmt.Sprintf("  %-5s %-43s %6d %5d %8s",
				endpoint.Method,truncatePlain(endpoint.Path,43),
				endpoint.Counts.Requests,endpoint.Counts.ServerErrors,
				analyticsDuration(endpoint.Counts,.95)))
		}
		if m.analyticsEndpointExpanded {
			lines=append(lines,fmt.Sprintf("Endpoints %d–%d of %d · ↑↓ PgUp/PgDn scroll",
				start+1,end,len(snapshot.Endpoints)))
		} else if end<len(snapshot.Endpoints) && end==0 {
			// On small terminals the endpoint list remains available via E.
			lines=append(lines,"Press E for the endpoint table.")
		}
	}
	for i:=range lines {
		lines[i]=truncate(lines[i],m.width)
	}
	return m.panel("HTTP analytics (F9)",strings.Join(lines,"\n"),
		"F9/Esc closes | 1 Requests · 2 4xx/5xx · 3 ~Latency · T 5/15m · G Grid · E Endpoints")
}

func (m model) updateAnalyticsKey(key string) (tea.Model,tea.Cmd) {
	switch key {
	case "f9":
		m.analyticsOpen=false
	case "1","2","3":
		m.analyticsMetric=int(key[0]-'1')
		m.analyticsGrid=false
		m.analyticsEndpointExpanded=false
	case "t","T":
		if m.analyticsWindowMinutes==5 {m.analyticsWindowMinutes=15} else {m.analyticsWindowMinutes=5}
		m.analyticsEndpointOffset=0
	case "g","G":
		m.analyticsGrid=!m.analyticsGrid
		m.analyticsEndpointExpanded=false
	case "e","E":
		m.analyticsEndpointExpanded=!m.analyticsEndpointExpanded
		m.analyticsEndpointOffset=0
	case "up","k":
		if m.analyticsEndpointExpanded {m.analyticsEndpointOffset=max(0,m.analyticsEndpointOffset-1)}
	case "down","j":
		if m.analyticsEndpointExpanded {m.analyticsEndpointOffset++}
	case "pgup":
		if m.analyticsEndpointExpanded {m.analyticsEndpointOffset=max(0,m.analyticsEndpointOffset-max(1,m.height/2))}
	case "pgdown":
		if m.analyticsEndpointExpanded {m.analyticsEndpointOffset+=max(1,m.height/2)}
	case "home":
		if m.analyticsEndpointExpanded {m.analyticsEndpointOffset=0}
	case "end":
		if m.analyticsEndpointExpanded {
			snapshot:=m.analytics.SnapshotWindow(time.Now(),m.analyticsWindowMinutes)
			m.analyticsEndpointOffset=max(0,len(snapshot.Endpoints)-1)
		}
	}
	return m,nil
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
