package tui

import (
	"fmt"
	"math"
	"strings"
	"time"

	tslc "github.com/NimbleMarkets/ntcharts/linechart/timeserieslinechart"
	"github.com/charmbracelet/lipgloss"
	"github.com/fernandocpaz/tailg/internal/core"
)

// Metric charts are rendered on demand from the existing bounded 15-minute
// snapshot, not updated for every streamed request. There are no additional
// Kubernetes queries or unbounded samples.
type analyticsChartMetric int

const (
	analyticsThroughput analyticsChartMetric = iota
	analyticsHTTPRates
	analyticsLatency
	analyticsChartCount
)

type analyticsSeries struct {
	name   string
	color  string
	values []tslc.TimePoint
}

type analyticsChartData struct {
	title  string
	legend string
	hint   string
	series []analyticsSeries
	maxY   float64
	units  string
}

// The chart shares the per-minute status and histogram denominators used by
// the summary. Minutes without status or timing data are skipped, never
// reported as a known 0% error rate or zero latency.
func makeAnalyticsChartData(snapshot core.AnalyticsSnapshot, period int, metric analyticsChartMetric) (analyticsChartData, []core.AnalyticsMinute) {
	minutes := snapshot.Minutes
	if period == 5 && len(minutes) > 5 {
		minutes = minutes[len(minutes)-5:]
	}
	out := analyticsChartData{maxY: 1}
	switch metric {
	case analyticsHTTPRates:
		out.title = "HTTP 4xx / 5xx rate by minute"
		out.legend = "4xx (amber) · 5xx (red) | denominator: status-known completions"
		out.units = "%"
		out.series = []analyticsSeries{{name:"4xx",color:"214"},{name:"5xx",color:"196"}}
	case analyticsLatency:
		out.title = "Approximate latency percentile by minute"
		out.legend = "~p50 (green) · ~p95 (amber) · ~p99 (red) | histogram bounds"
		out.units = "ms"
		out.series = []analyticsSeries{{name:"p50",color:"42"},{name:"p95",color:"214"},{name:"p99",color:"196"}}
	default:
		out.title = "HTTP completions per minute"
		out.legend = "Requests (cyan) | one observed completion = one parsed log event"
		out.units = "req"
		out.series = []analyticsSeries{{name:"requests",color:"45"}}
	}
	missing := 0
	for _,minute := range minutes {
		counts := minute.Counts
		when := minute.Minute
		var values []float64
		switch metric {
		case analyticsHTTPRates:
			if counts.StatusKnown == 0 {
				missing++
				continue
			}
			values = []float64{
				100*float64(counts.ClientErrors)/float64(counts.StatusKnown),
				100*float64(counts.ServerErrors)/float64(counts.StatusKnown),
			}
		case analyticsLatency:
			if counts.Timed == 0 {
				missing++
				continue
			}
			values = []float64{
				float64(counts.ApproxPercentile(.5))/float64(time.Millisecond),
				float64(counts.ApproxPercentile(.95))/float64(time.Millisecond),
				float64(counts.ApproxPercentile(.99))/float64(time.Millisecond),
			}
			for i, value := range values {
				// An open-ended histogram bin must not overflow the Y axis or
				// masquerade as a precise multi-year response duration.
				if value > 300000 {values[i] = 300000}
			}
		default:
			values = []float64{float64(counts.Requests)}
		}
		for i,value := range values {
			out.series[i].values = append(out.series[i].values,tslc.TimePoint{Time:when,Value:value})
			out.maxY = math.Max(out.maxY,value)
		}
	}
	if metric == analyticsHTTPRates {
		out.maxY = math.Min(100, math.Max(1,out.maxY*1.1))
	} else {
		out.maxY = math.Max(1,math.Ceil(out.maxY*1.15))
	}
	if missing > 0 {
		out.hint = fmt.Sprintf("%d/%d min without %s samples; lines may bridge gaps",
			missing,len(minutes),map[analyticsChartMetric]string{analyticsHTTPRates:"status",analyticsLatency:"latency"}[metric])
	}
	if metric == analyticsLatency {
		if out.hint != "" {out.hint += " · "}
		out.hint += "open-ended latency bin clipped to 5m"
	}
	return out,minutes
}

func renderAnalyticsTimeChart(snapshot core.AnalyticsSnapshot, period int, metric analyticsChartMetric, width, height int, color bool) []string {
	data,minutes := makeAnalyticsChartData(snapshot,period,metric)
	if width < 24 || height < 5 {
		return []string{data.title+": resize terminal to view chart"}
	}
	lines := []string{data.title}
	haveData := false
	for _,series:=range data.series {
		haveData=haveData||len(series.values)>0
	}
	if !haveData {
		return append(lines,"No parsed samples for this metric and time range.",data.legend)
	}
	start,end := minutes[0].Minute,minutes[len(minutes)-1].Minute
	chart := tslc.New(width,height,
		tslc.WithTimeRange(start,end),
		tslc.WithYRange(0,data.maxY),
		tslc.WithXYSteps(2,2),
		tslc.WithXLabelFormatter(func(_ int,v float64)string {
			return time.Unix(int64(v),0).Local().Format("15:04")
		}),
		tslc.WithYLabelFormatter(func(_ int,v float64)string {
			if data.units=="%" {return fmt.Sprintf("%.0f%%",v)}
			if v>=1000 && data.units=="ms" {return fmt.Sprintf("%.0fs",v/1000)}
			return fmt.Sprintf("%.0f",v)
		}),
	)
	for _,series := range data.series {
		style:=lipgloss.NewStyle()
		if color {style=style.Foreground(lipgloss.Color(series.color))}
		chart.SetDataSetStyle(series.name,style)
		for _,point:=range series.values {
			chart.PushDataSet(series.name,point)
		}
	}
	chart.DrawBrailleAll()
	lines=append(lines,strings.Split(chart.View(),"\n")...)
	lines=append(lines,data.legend)
	if data.hint!="" {lines=append(lines,data.hint)}
	return lines
}

// The actual chart area is generated by ntcharts. This layout only joins
// fixed-size graphs, keeps them inside the available terminal width and never
// infers missing metric values.
func renderAnalyticsChartGrid(snapshot core.AnalyticsSnapshot,period,width,height int,color bool) []string {
	if width<100 || height<28 {return nil}
	gap:=2
	half:=(width-gap)/2
	// Leave room for summary and endpoint labels in a typical 30-line terminal.
	plotHeight:=min(7,max(5,(height-18)/2))
	left:=renderAnalyticsTimeChart(snapshot,period,analyticsThroughput,half,plotHeight,color)
	right:=renderAnalyticsTimeChart(snapshot,period,analyticsHTTPRates,half,plotHeight,color)
	bottom:=renderAnalyticsTimeChart(snapshot,period,analyticsLatency,half,plotHeight,color)
	leftPanel:=analyticsChartBlock(left,half)
	rightPanel:=analyticsChartBlock(right,half)
	bottomPanel:=analyticsChartBlock(bottom,half)
	grid:=lipgloss.JoinHorizontal(lipgloss.Top,leftPanel,strings.Repeat(" ",gap),rightPanel)
	out:=strings.Split(grid,"\n")
	out=append(out,strings.Split(bottomPanel,"\n")...)
	return out
}

func analyticsChartBlock(lines []string,width int) string {
	for i:=range lines {
		lines[i]=truncate(lines[i],width)
	}
	return strings.Join(lines,"\n")
}
