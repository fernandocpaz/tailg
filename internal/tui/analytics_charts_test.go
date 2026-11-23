package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/fernandocpaz/tailg/internal/core"
)

func testChartAnalytics(now time.Time) *core.RollingAnalytics {
	a:=core.NewRollingAnalytics()
	for minute:=0;minute<15;minute++ {
		at:=now.UTC().Truncate(time.Minute).Add(-time.Duration(14-minute)*time.Minute)
		for i:=0;i<2+minute;i++ {
			status:=200
			if minute>7 && i%4==0 {status=503}
			if minute>9 && i%5==0 {status=404}
			a.ObserveAt(core.LogEvent{
				Workload:"api",Pod:"api-0",Container:"api",ObservedAt:at,
				Message:fmt.Sprintf("HTTP GET /orders/%d responded %d in %d ms",
					i,status,25+minute*20),
			},now)
		}
	}
	return a
}

func analyticsUIKey(m model,key string) model {
	var msg tea.KeyMsg
	if key=="f9" {msg=tea.KeyMsg{Type:tea.KeyF9}
	} else if key=="esc" {msg=tea.KeyMsg{Type:tea.KeyEsc}
	} else {msg=tea.KeyMsg{Type:tea.KeyRunes,Runes:[]rune(key)}}
	updated,_:=m.Update(msg)
	return updated.(model)
}

func TestAnalyticsChartDataDoesNotTreatUnknownRatesAsZero(t *testing.T) {
	now:=time.Date(2026,11,23,20,42,0,0,time.UTC)
	a:=core.NewRollingAnalytics()
	a.ObserveAt(core.LogEvent{
		Message:"HTTP GET /orders responded 500 in 700 ms",
		ObservedAt:now.Add(-time.Minute),
	},now)
	snapshot:=a.SnapshotWindow(now,5)
	rates,mins:=makeAnalyticsChartData(snapshot,5,analyticsHTTPRates)
	if len(mins)!=5 || len(rates.series)!=2 ||
		len(rates.series[0].values)!=1 || len(rates.series[1].values)!=1 ||
		!strings.Contains(rates.hint,"without status samples") {
		t.Fatalf("missing-status buckets were treated as zero: %+v",rates)
	}
	if rates.series[1].values[0].Value!=100 {
		t.Fatalf("known 5xx rate should be 100%%, got %+v",rates.series[1].values)
	}
	latency,_:=makeAnalyticsChartData(snapshot,5,analyticsLatency)
	if len(latency.series)!=3 ||
		len(latency.series[0].values)!=1 ||
		len(latency.series[2].values)!=1 {
		t.Fatalf("latency samples missing: %+v",latency)
	}
}

func TestF9ChartsRenderWithNtchartsAndMaintainLayouts(t *testing.T) {
	now:=time.Now().UTC()
	a:=testChartAnalytics(now)
	for _,tc:=range []struct{
		width,height int
		grid bool
	}{
		{120,34,true},{100,28,true},{80,22,false},{45,16,false},
	} {
		m:=model{
			config:Config{Formatter:core.Formatter{Color:false}},
			state:core.NewFilterState(100),input:textinput.New(),analytics:a,
			width:tc.width,height:tc.height,followsLive:true,
		}
		m=analyticsUIKey(m,"f9")
		if !m.analyticsOpen || !m.analyticsGrid {t.Fatal("F9 did not initialize chart mode")}
		view:=ansi.Strip(m.View())
		for _,expected:=range []string{"HTTP analytics (F9)","Requests:","HTTP status coverage","Approx p50/p95/p99"} {
			if !strings.Contains(view,expected) {t.Fatalf("screen %dx%d missing %q:\n%s",tc.width,tc.height,expected,view)}
		}
		if tc.grid {
			if !strings.Contains(view,"HTTP completions per minute") ||
				!strings.Contains(view,"HTTP 4xx / 5xx rate") {
				t.Fatalf("wide grid missing first-row graphs: %s",view)
			}
		} else if !strings.Contains(view,"HTTP completions per minute") {
			t.Fatalf("compact layout must show focused graph:\n%s",view)
		}
		for _,row:=range strings.Split(view,"\n") {
			if lipgloss.Width(row)>tc.width {
				t.Fatalf("chart row overflows %d cols (%d): %q",tc.width,lipgloss.Width(row),row)
			}
		}
		m=analyticsUIKey(m,"2")
		if m.analyticsGrid || m.analyticsMetric!=int(analyticsHTTPRates) ||
			!strings.Contains(ansi.Strip(m.View()),"HTTP 4xx / 5xx rate") {
			t.Fatal("2 did not focus HTTP error-rate chart")
		}
		m=analyticsUIKey(m,"3")
		if m.analyticsMetric!=int(analyticsLatency) ||
			!strings.Contains(ansi.Strip(m.View()),"Approximate latency percentile") {
			t.Fatal("3 did not focus latency chart")
		}
		m=analyticsUIKey(m,"t")
		if m.analyticsWindowMinutes!=5 || (!strings.Contains(ansi.Strip(m.View()),"5 calendar-minute") && !strings.Contains(ansi.Strip(m.View()),"| 5m")) {
			t.Fatal("T did not select 5 minute window")
		}
		m=analyticsUIKey(m,"e")
		if !m.analyticsEndpointExpanded || !strings.Contains(ansi.Strip(m.View()),"/orders/{id}") {
			t.Fatal("E did not open endpoint table")
		}
		m=analyticsUIKey(m,"esc")
		if m.analyticsOpen || !m.followsLive {t.Fatal("Esc modified live view")}
	}
}

func TestF9ChartsPreserveFilterAndShowEmptyCoverage(t *testing.T) {
	state:=core.NewFilterState(20)
	state.SetFilter("timeout")
	input:=textinput.New()
	input.SetValue("timeout")
	m:=model{
		config:Config{Formatter:core.Formatter{Color:false}},
		state:state,input:input,analytics:core.NewRollingAnalytics(),
		width:90,height:24,followsLive:true,
	}
	m=analyticsUIKey(m,"f9")
	for _,key:=range []string{"2","t","g","e","home","down","3","1"} {
		m=analyticsUIKey(m,key)
	}
	if !strings.Contains(ansi.Strip(m.View()),"No parsed samples") {
		t.Fatal("empty chart incorrectly asserted traffic")
	}
	m=analyticsUIKey(m,"f9")
	if m.analyticsOpen || m.state!=state || m.input.Value()!="timeout" ||
		m.state.Filter()!="timeout" || !m.followsLive {
		t.Fatal("chart interaction mutated the live log buffer or filter")
	}
}
