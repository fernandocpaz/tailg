package tui

import (
	"fmt"
	"math"
	"strings"
	"time"

	tslc "github.com/NimbleMarkets/ntcharts/linechart/timeserieslinechart"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/fernandocpaz/tailg/internal/core"
)

const pickerChartHistoryLimit = 61 // ~15 min of 15-second samples, per visible workload.

type pickerResourcePoint struct {
	at time.Time
	cpu, memory float64 // millicores and MiB, respectively
	cpuKnown, memoryKnown bool
}
type pickerLogPoint struct {
	at time.Time
	errors, warnings float64
}
type pickerChartState struct {
	open bool
	metric int // 0=CPU, 1=memory, 2=log alerts
	grid bool
	resources map[string][]pickerResourcePoint
	logs map[string][]pickerLogPoint
}

func (m *pickerModel) togglePickerMonitoring() tea.Cmd {
	m.monitorApps = nil
	m.charts.resources, m.charts.logs = nil, nil
	if m.monitorEnabled {
		m.clearDeploymentNotifications()
	} else {
		m.beginDeploymentNotifications()
	}
	return m.podMonitorState.toggle(preparePodPickerRows(m.apps), m.kubeContext, m.namespace)
}

func (m *pickerModel) openPickerCharts() tea.Cmd {
	if len(m.apps)==0 || m.index<0 || m.index>=len(m.apps) {
		m.note="Select an application with running pods before opening F9 charts"
		return nil
	}
	if len(m.apps[m.index].Pods)==0 {
		m.note="Selected workload has no current pods to chart"
		return nil
	}
	m.charts.open=true
	m.charts.grid=true
	m.charts.metric=0
	m.note=""
	if !m.monitorEnabled {
		// Reuse the existing explicit monitor pipeline and its 15s Metrics API
		// poll, without introducing another Kubernetes query loop.
		return m.togglePickerMonitoring()
	}
	return nil
}

func (m *pickerModel) recordPickerMetrics(result namespaceMetricsResultMsg) {
	if !m.monitorEnabled || result.generation!=m.monitorGeneration ||
		result.scope!=activityScope(m.kubeContext,m.namespace) {return}
	at:=result.at
	if at.IsZero(){at=time.Now()}
	if m.charts.resources==nil {
		m.charts.resources=make(map[string][]pickerResourcePoint,len(m.apps))
	}
	for _,app:=range m.apps {
		if len(app.Pods)==0 {continue}
		point:=pickerResourcePoint{at:at}
		if result.err==nil {
			usage,ok:=m.appResources(app)
			if ok {
				point.cpu,point.cpuKnown=usage.CPUMilli,usage.CPUValid
				point.memory,point.memoryKnown=usage.MemoryBytes/(1024*1024),usage.MemoryValid
			}
		}
		m.charts.resources[app.Name]=appendBoundedResource(m.charts.resources[app.Name],point)
	}
}

func appendBoundedResource(points []pickerResourcePoint,point pickerResourcePoint) []pickerResourcePoint {
	points=append(points,point)
	if len(points)>pickerChartHistoryLimit {
		points=append([]pickerResourcePoint(nil),points[len(points)-pickerChartHistoryLimit:]...)
	}
	return points
}

func (m *pickerModel) recordPickerLogScan(result podHealthScanMsg) {
	if !m.monitorEnabled || result.generation!=m.monitorGeneration {return}
	at:=result.scannedAt
	if at.IsZero(){at=time.Now()}
	if m.charts.logs==nil {m.charts.logs=make(map[string][]pickerLogPoint,len(m.apps))}
	for _,app:=range m.apps {
		if len(app.Pods)==0 {continue}
		var sample pickerLogPoint
		sample.at=at
		complete:=true
		seen:=map[string]bool{}
		for _,pod:=range preparePodPickerRows([]core.AppChoice{app}) {
			name:=pod.pod.Name
			if name=="" || seen[name] {continue}
			seen[name]=true
			entry,ok:=result.samples[name]
			if !ok || entry.err!=nil {
				complete=false
				break
			}
			sample.errors+=float64(entry.errors)
			sample.warnings+=float64(entry.warnings)
		}
		if !complete || len(seen)==0 {continue} // Never chart an incomplete scan as zero.
		points:=append(m.charts.logs[app.Name],sample)
		if len(points)>pickerChartHistoryLimit {
			points=append([]pickerLogPoint(nil),points[len(points)-pickerChartHistoryLimit:]...)
		}
		m.charts.logs[app.Name]=points
	}
}

func (m pickerModel) pickerChartSeries(name string, metric int) ([]pickerLineSeries,string) {
    if m.external&&metric==0{
        series:=pickerLineSeries{name:"CPU % limit",color:"42"}
        for _,point:=range m.charts.resources[name]{
            if point.cpuKnown{series.points=append(series.points,tslc.TimePoint{Time:point.at,Value:point.cpu})}
        }
        return []pickerLineSeries{series},"CPU usage as % of configured workload limit (unknown when unset)"
    }
	switch metric {
	case 1:
		s:=pickerLineSeries{name:"Memory MiB",color:"39"}
		for _,p:=range m.charts.resources[name] {
			if p.memoryKnown {s.points=append(s.points,tslc.TimePoint{Time:p.at,Value:p.memory})}
		}
		return []pickerLineSeries{s},"Memory of all current workload pods (MiB)"
	case 2:
		e,w:=pickerLineSeries{name:"Errors",color:"196"},pickerLineSeries{name:"Warnings",color:"214"}
		for _,p:=range m.charts.logs[name] {
			e.points=append(e.points,tslc.TimePoint{Time:p.at,Value:p.errors})
			w.points=append(w.points,tslc.TimePoint{Time:p.at,Value:p.warnings})
		}
		return []pickerLineSeries{e,w},"Parsed log errors/warnings per sampled scan (~1m)"
	default:
		s:=pickerLineSeries{name:"CPU millicores",color:"42"}
		for _,p:=range m.charts.resources[name] {
			if p.cpuKnown {s.points=append(s.points,tslc.TimePoint{Time:p.at,Value:p.cpu})}
		}
		return []pickerLineSeries{s},"CPU of all current workload pods (millicores)"
	}
}

type pickerLineSeries struct {
	name,color string
	points []tslc.TimePoint
}

// Generate graphs directly from a bounded per-workload monitor history; no
// synthetic points, fake data, or replayed log entries.
func (m pickerModel) pickerChartLines(app string,metric,width,height int) []string {
	series,title:=m.pickerChartSeries(app,metric)
	lines:=[]string{title}
	if width<28 || height<5 {return append(lines,"Resize terminal to show plots.")}
	have:=false
	maxY:=float64(1)
	for _,s:=range series {
		if len(s.points)>0 {have=true}
		for _,p:=range s.points {maxY=math.Max(maxY,p.Value)}
	}
	if !have {
		reason:="Collecting monitor samples..."
		if !m.monitorEnabled {reason="Monitor is off. Press M to collect samples."}
		if metric==2 {reason="Waiting for first complete log scan (~1m)..."}
		if metric!=2 && m.metricsWarning!="" && m.metricsPods==nil {
			reason="Metrics unavailable: "+m.metricsWarning
		}
		return append(lines,truncatePlain(reason,width))
	}
	end:=time.Now()
	start:=end.Add(-15*time.Minute)
	maxY=math.Max(1,math.Ceil(maxY*1.15))
	chart:=tslc.New(width,height,tslc.WithTimeRange(start,end),tslc.WithYRange(0,maxY),
		tslc.WithXYSteps(2,2),
		tslc.WithXLabelFormatter(func(_ int,v float64)string{return time.Unix(int64(v),0).Local().Format("15:04")}),
		tslc.WithYLabelFormatter(func(_ int,v float64)string{return fmt.Sprintf("%.0f",v)}),
	)
	for _,s:=range series {
		style:=lipgloss.NewStyle()
		style=style.Foreground(lipgloss.Color(s.color))
		chart.SetDataSetStyle(s.name,style)
		for _,p:=range s.points {
			// Previous monitoring samples are discarded outside the displayed
			// time range, rather than suggesting they were recorded recently.
			if !p.Time.Before(start) && !p.Time.After(end) {
				chart.PushDataSet(s.name,p)
			}
		}
	}
	chart.DrawBrailleAll()
	lines=append(lines,strings.Split(chart.View(),"\n")...)
	switch metric {
	case 2:lines=append(lines,"Red errors · amber warnings · observed log scans")
	default:lines=append(lines,fmt.Sprintf("%d collected sample(s) · last 15m",len(series[0].points)))
	}
	return lines
}

func (m pickerModel) pickerChartsModal() string {
	width,height:=m.modalViewport()
	if width<40 || height<14 {
		return ansi.Hardwrap("Resize to at least 40 × 14 for F9 charts.\nEsc closes.",max(1,width),true)
	}
	inner:=min(132,width-6)-4
	app:=""
	if len(m.apps)>0 && m.index>=0 && m.index<len(m.apps) {app=m.apps[m.index].Name}
	state:="MONITOR ON · 15s CPU/MEM · ~1m logs"
	if !m.monitorEnabled {state="MONITOR OFF · press M to collect"}
	head:=[]string{
		headerStyle.Render(truncatePlain("F9 WORKLOAD CHARTS · "+app,inner)),
		truncatePlain(m.kubeContext+" / "+m.namespace+" · "+state,inner),
	}
	foot:=[]string{
		"1 CPU · 2 Memory · 3 Logs · G Grid · ↑↓ Workloads",
		"M Monitor on/off · F9/Esc close",
	}
	for i,line:=range foot {foot[i]=truncatePlain(line,inner)}
	available:=max(5,height-2-len(head)-len(foot)-1)
	var content []string
	if m.charts.grid && width>=104 && height>=27 {
		gap:=2
		w:=(inner-gap)/2
		plot:=min(7,max(5,(available-8)/2))
		left:=m.pickerChartLines(app,0,w,plot)
		right:=m.pickerChartLines(app,1,w,plot)
		content=append(content,strings.Split(lipgloss.JoinHorizontal(lipgloss.Top,analyticsChartBlock(left,w),
			strings.Repeat(" ",gap),analyticsChartBlock(right,w)),"\n")...)
		content=append(content,m.pickerChartLines(app,2,inner,plot)...)
	} else {
		plot:=max(5,min(12,available-3))
		content=m.pickerChartLines(app,m.charts.metric,inner,plot)
	}
	if len(content)>available {content=content[:available]}
	for i,line:=range content {content[i]=ansi.Truncate(line,inner,"…")}
	all:=append(head,content...)
	all=append(all,foot...)
	style:=lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(accentColor).
		Foreground(modalForeground).Background(modalBackground).Padding(0,1).Width(inner+2)
	return style.Render(strings.Join(all,"\n"))
}

func (m pickerModel) updatePickerChartsKey(key tea.KeyMsg) (tea.Model,tea.Cmd) {
	switch key.String() {
	case "ctrl+c":
		m.podMonitorState.stop()
		m.result.Action=PickerQuit
		return m,tea.Quit
	case "esc","f9":
		m.charts.open=false
	case "m","M":
		return m,m.togglePickerMonitoring()
	case "1","2","3":
		m.charts.metric=int(key.String()[0]-'1')
		m.charts.grid=false
	case "g","G":
		m.charts.grid=!m.charts.grid
	case "up","k":
		for i:=m.index-1;i>=0;i-- {
			if len(m.apps[i].Pods)>0 {
				m.index=i
				break
			}
		}
	case "down","j":
		for i:=m.index+1;i<len(m.apps);i++ {
			if len(m.apps[i].Pods)>0 {
				m.index=i
				break
			}
		}
	}
	return m,nil
}
