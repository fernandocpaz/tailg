package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/fernandocpaz/tailg/internal/core"
	"github.com/fernandocpaz/tailg/internal/kube"
)

func pickerChartsKey(key string) tea.KeyMsg {
	if key=="f9" {return tea.KeyMsg{Type:tea.KeyF9}}
	if key=="esc" {return tea.KeyMsg{Type:tea.KeyEsc}}
	return tea.KeyMsg{Type:tea.KeyRunes,Runes:[]rune(key)}
}
func pickerChartsDemo() pickerModel {
	apps:=[]core.AppChoice{
		{Name:"api",Pods:[]string{"api-1","api-2"}},
		{Name:"worker",Pods:[]string{"worker-1"}},
		{Name:"batch",CronJob:&core.CronJobChoice{}},
	}
	m:=newPickerModel(apps,len(apps),"tkgs-qa","apollo",nil)
	m.width,m.height=128,34
	return m
}

func TestPickerF9OpensRealChartsAndStartsMonitoring(t *testing.T) {
	m:=pickerChartsDemo()
	view:=ansi.Strip(m.View())
	if !strings.Contains(view,"F9") || !strings.Contains(view,"Charts") {
		t.Fatal("picker must advertise charts without opening logs")
	}
	changed,cmd:=m.Update(pickerChartsKey("f9"))
	m=changed.(pickerModel)
	if !m.charts.open || !m.monitorEnabled || cmd==nil || m.index!=0 {
		t.Fatalf("F9 did not start monitoring charts: %+v",m.charts)
	}
	t.Cleanup(m.monitorCancel)
	if !strings.Contains(ansi.Strip(m.View()),"F9 WORKLOAD CHARTS") {
		t.Fatalf("missing chart modal: %s",ansi.Strip(m.View()))
	}
	metricTime:=time.Now()
	scope:=activityScope(m.kubeContext,m.namespace)
	changed,_=m.Update(namespaceMetricsResultMsg{
		generation:m.monitorGeneration,scope:scope,at:metricTime,
		metrics:kube.NamespaceResourceMetrics{Pods:map[string]kube.PodResourceUsage{
			"api-1":{CPUMilli:100,MemoryBytes:128*1024*1024,CPUValid:true,MemoryValid:true},
			"api-2":{CPUMilli:200,MemoryBytes:256*1024*1024,CPUValid:true,MemoryValid:true},
			"worker-1":{CPUMilli:50,MemoryBytes:64*1024*1024,CPUValid:true,MemoryValid:true},
		}},
	})
	m=changed.(pickerModel)
	if len(m.charts.resources["api"])!=1 || m.charts.resources["api"][0].cpu!=300 ||
		m.charts.resources["api"][0].memory!=384 {
		t.Fatalf("missing or incorrect metric aggregation: %+v",m.charts.resources)
	}
	view=ansi.Strip(m.View())
	if !strings.Contains(view,"CPU of all current workload pods") ||
		!strings.Contains(view,"Memory of all current workload pods") {
		t.Fatalf("chart grid missing plots:\n%s",view)
	}
	for _,row:=range strings.Split(view,"\n") {
		if ansi.StringWidth(row)>m.width {t.Fatalf("chart overflow: %q",row)}
	}
	changed,_=m.Update(pickerChartsKey("2"))
	m=changed.(pickerModel)
	if m.charts.metric!=1 || m.charts.grid ||
		!strings.Contains(ansi.Strip(m.View()),"Memory of all current workload pods") {
		t.Fatal("2 must focus memory chart")
	}
	changed,_=m.Update(pickerChartsKey("3"))
	m=changed.(pickerModel)
	if m.charts.metric!=2 || !strings.Contains(ansi.Strip(m.View()),"Waiting for first complete log scan") {
		t.Fatal("no completed log scan must not be displayed as zero alerts")
	}
	changed,_=m.Update(podHealthScanMsg{
		generation:m.monitorGeneration,scannedAt:metricTime,
		samples:map[string]podHealthSample{"api-1":{errors:3,warnings:2},"api-2":{errors:1},"worker-1":{lines:5}},
	})
	m=changed.(pickerModel)
	if len(m.charts.logs["api"])!=1 || m.charts.logs["api"][0].errors!=4 {
		t.Fatalf("error trend aggregation incorrect: %+v",m.charts.logs)
	}
	changed,_=m.Update(pickerChartsKey("esc"))
	m=changed.(pickerModel)
	if m.charts.open || !m.monitorEnabled || m.index!=0 {
		t.Fatal("closing charts must restore picker and keep monitoring")
	}
	changed,_=m.Update(pickerChartsKey("f9"))
	m=changed.(pickerModel)
	if !m.charts.open || len(m.charts.resources["api"])!=1 {
		t.Fatal("reopening charts must preserve collected monitor samples")
	}
}

func TestPickerF9IgnoresStaleAndIncompleteMetrics(t *testing.T) {
	m:=pickerChartsDemo()
	changed,_:=m.Update(pickerChartsKey("f9"))
	m=changed.(pickerModel)
	t.Cleanup(m.monitorCancel)
	at:=time.Now()
	for _,msg:=range []namespaceMetricsResultMsg{
		{generation:m.monitorGeneration-1,scope:activityScope(m.kubeContext,m.namespace),
			at:at,metrics:kube.NamespaceResourceMetrics{Pods:map[string]kube.PodResourceUsage{"api-1":{CPUValid:true,CPUMilli:500}}}},
		{generation:m.monitorGeneration,scope:activityScope(m.kubeContext,"other"),
			at:at,metrics:kube.NamespaceResourceMetrics{Pods:map[string]kube.PodResourceUsage{"api-1":{CPUValid:true,CPUMilli:500}}}},
	} {
		changed,_=m.Update(msg)
		m=changed.(pickerModel)
	}
	if len(m.charts.resources["api"])!=0 {t.Fatal("accepted stale or cross-scope metrics")}
	changed,_=m.Update(namespaceMetricsResultMsg{
		generation:m.monitorGeneration,scope:activityScope(m.kubeContext,m.namespace),at:at,
		metrics:kube.NamespaceResourceMetrics{Pods:map[string]kube.PodResourceUsage{
			"api-1":{CPUValid:true,CPUMilli:300},
		}},
	})
	m=changed.(pickerModel)
	if p:=m.charts.resources["api"][0];p.cpuKnown || p.memoryKnown {
		t.Fatalf("incomplete pod metrics must not be charted: %+v",p)
	}
	changed,_=m.Update(namespaceMetricsResultMsg{
		generation:m.monitorGeneration,scope:activityScope(m.kubeContext,m.namespace),at:at,
		err:errors.New("403 forbidden"),
	})
	m=changed.(pickerModel)
	if !strings.Contains(ansi.Strip(m.View()),"Metrics unavailable") {
		t.Fatal("RBAC denied metrics must appear as unavailable")
	}
	changed,_=m.Update(pickerChartsKey("m"))
	m=changed.(pickerModel)
	if m.monitorEnabled || m.charts.resources!=nil || m.charts.logs!=nil {
		t.Fatal("stopping monitor should clear chart history")
	}
	if !strings.Contains(ansi.Strip(m.View()),"Monitor is off") {
		t.Fatal("chart must explain how to enable collection")
	}
}

func TestPickerF9CompactTerminalAndHistoryBound(t *testing.T) {
	m:=pickerChartsDemo()
	m.width,m.height=78,22
	changed,_:=m.Update(pickerChartsKey("f9"))
	m=changed.(pickerModel)
	t.Cleanup(m.monitorCancel)
	view:=ansi.Strip(m.View())
	if !strings.Contains(view,"F9 WORKLOAD CHARTS") || !strings.Contains(view,"CPU of all current") {
		t.Fatalf("compact screen is missing readable focused chart:\n%s",view)
	}
	if strings.Contains(view,"Memory of all current workload pods") {
		t.Fatal("compact screen must focus only one chart")
	}
	for _,row:=range strings.Split(view,"\n") {
		if ansi.StringWidth(row)>m.width {t.Fatalf("chart too wide: %q",row)}
	}
	at:=time.Now().Add(-15*time.Minute)
	for i:=0;i<150;i++ {
		changed,_=m.Update(namespaceMetricsResultMsg{
			generation:m.monitorGeneration,scope:activityScope(m.kubeContext,m.namespace),
			at:at.Add(time.Duration(i)*time.Second),
			metrics:kube.NamespaceResourceMetrics{Pods:map[string]kube.PodResourceUsage{
				"api-1":{CPUValid:true,MemoryValid:true,CPUMilli:100,MemoryBytes:1024*1024},
				"api-2":{CPUValid:true,MemoryValid:true,CPUMilli:100,MemoryBytes:1024*1024},
				"worker-1":{CPUValid:true,MemoryValid:true,CPUMilli:100,MemoryBytes:1024*1024},
			}},
		})
		m=changed.(pickerModel)
	}
	if got:=len(m.charts.resources["api"]);got!=pickerChartHistoryLimit {
		t.Fatalf("history unbounded or missing: %d",got)
	}
	changed,_=m.Update(pickerChartsKey("down"))
	m=changed.(pickerModel)
	if m.index!=1 {t.Fatal("charts must navigate running workloads")}
	changed,_=m.Update(pickerChartsKey("esc"))
	if changed.(pickerModel).index!=1 {t.Fatal("selected workload must persist after closing chart")}
}

func TestPickerF9RequiresRunningPods(t *testing.T) {
	m:=pickerModel{width:80,height:24,apps:[]core.AppChoice{{Name:"batch"}}}
	changed,cmd:=m.Update(pickerChartsKey("f9"))
	picker:=changed.(pickerModel)
	if picker.charts.open || picker.monitorEnabled || cmd!=nil || picker.note=="" {
		t.Fatal("F9 should explain why charts unavailable for a podless workload")
	}
}
