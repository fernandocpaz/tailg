package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/fernandocpaz/tailg/internal/kube"
)

const nodeDiagnosticTimeout = 30 * time.Second

type nodeDiagnosticState struct {
	open, busy bool
	request    int
	scope      string
	app        string
	pods       []string
	report     kube.WorkloadNodeDiagnostics
	scroll     int
}

type nodeDiagnosticResultMsg struct {
	request int
	scope   string
	app     string
	report  kube.WorkloadNodeDiagnostics
}

func loadNodeDiagnostics(parent context.Context, kubeContext, namespace, app string, pods []string, request int) tea.Cmd {
	scope:=activityScope(kubeContext,namespace)
	pods=append([]string(nil),pods...)
	return func() tea.Msg {
		ctx,cancel:=context.WithTimeout(parent,nodeDiagnosticTimeout)
		defer cancel()
		report:=kube.NewRunner(namespace,kubeContext).WorkloadNodeDiagnostics(ctx,app,pods)
		return nodeDiagnosticResultMsg{request:request,scope:scope,app:app,report:report}
	}
}

func (m *pickerModel) openNodeDiagnostics() tea.Cmd {
	if m.index<0 || m.index>=len(m.apps) {
		m.note="Highlight an application to inspect its assigned nodes"
		return nil
	}
	app:=m.apps[m.index]
	if len(app.Pods)==0 {
		m.note="Selected application has no current pods to diagnose"
		return nil
	}
	m.nodeDiag.open=true
	m.nodeDiag.busy=false
	m.nodeDiag.app=app.Name
	m.nodeDiag.scope=activityScope(m.kubeContext,m.namespace)
	m.nodeDiag.pods=append([]string(nil),app.Pods...)
	m.nodeDiag.report=kube.WorkloadNodeDiagnostics{}
	m.nodeDiag.scroll=0
	return m.refreshNodeDiagnostics()
}

func (m *pickerModel) refreshNodeDiagnostics() tea.Cmd {
	if !m.nodeDiag.open || m.nodeDiag.busy {return nil}
	m.nodeDiag.busy=true
	m.nodeDiag.request++
	parent:=m.refreshContext
	if parent==nil {parent=context.Background()}
	return loadNodeDiagnostics(parent,m.kubeContext,m.namespace,m.nodeDiag.app,m.nodeDiag.pods,m.nodeDiag.request)
}

func (m *pickerModel) updateNodeDiagnostics(msg tea.Msg) (bool,tea.Cmd) {
	result,ok:=msg.(nodeDiagnosticResultMsg)
	if !ok {return false,nil}
	if !m.nodeDiag.open || !m.nodeDiag.busy || result.scope!=activityScope(m.kubeContext,m.namespace) ||
		result.scope!=m.nodeDiag.scope || result.app!=m.nodeDiag.app || result.request!=m.nodeDiag.request {
		return true,nil
	}
	m.nodeDiag.busy=false
	m.nodeDiag.report=result.report
	m.nodeDiag.scroll=0
	return true,nil
}

func (m pickerModel) updateNodeDiagnosticKey(key tea.KeyMsg) (tea.Model,tea.Cmd) {
	switch key.String() {
	case "ctrl+c":
		m.podMonitorState.stop()
		m.result.Action=PickerQuit
		return m,tea.Quit
	case "esc","q","d","D":
		m.nodeDiag.open=false
		m.nodeDiag.request++ // invalidate in-flight results
		m.nodeDiag.busy=false
		return m,nil
	case "r","R":
		return m,m.refreshNodeDiagnostics()
	case "up","k":
		m.nodeDiag.scroll=max(0,m.nodeDiag.scroll-1)
	case "down","j":
		_,limit:=m.nodeDiagnosticModal()
		m.nodeDiag.scroll=min(limit,m.nodeDiag.scroll+1)
	case "pgup","pgdown","home","end":
		_,limit:=m.nodeDiagnosticModal()
		step:=max(1,m.height/2)
		switch key.String() {
		case "pgup":m.nodeDiag.scroll=max(0,m.nodeDiag.scroll-step)
		case "pgdown":m.nodeDiag.scroll=min(limit,m.nodeDiag.scroll+step)
		case "home":m.nodeDiag.scroll=0
		case "end":m.nodeDiag.scroll=limit
		}
	}
	return m,nil
}

func (m pickerModel) nodeDiagnosticLines() []string {
	report:=m.nodeDiag.report
	lines:=[]string{
		"Read-only Kubernetes pod and node snapshot; metrics are optional.",
		"Node CPU usage is a point-in-time sample; pressure conditions are authoritative Kubernetes states.",
		"CPU/memory usage versus node allocatable is not per-pod usage or proof of throttling.",
		"",
	}
	if m.nodeDiag.busy {
		lines=append(lines,"Reading selected pods and their assigned nodes...")
		if !report.CapturedAt.IsZero() {lines=append(lines,"Previous snapshot shown while refreshing")}
	}
	if report.CapturedAt.IsZero() {
		return lines
	}
	lines=append(lines,fmt.Sprintf("Collected: %s | %d selected pod(s), %d assigned node(s)",
		report.CapturedAt.Local().Format("15:04:05"),len(report.Pods),len(report.Nodes)))
	for _,warning:=range report.Warnings {
		lines=append(lines,"INCOMPLETE: "+warning)
	}
	lines=append(lines,"","PODS")
	if len(report.Pods)==0 {
		lines=append(lines,"No matched pod records returned.")
	}
	for _,pod:=range report.Pods {
		node:=pod.Node
		if node=="" {node="(not scheduled)"}
		signals:=[]string{}
		if pod.OOMKilled {signals=append(signals,"OOMKilled")}
		if pod.Restarts>0 {signals=append(signals,fmt.Sprintf("%d restart(s)",pod.Restarts))}
		if pod.ReadyKnown && !pod.Ready && pod.Phase=="Running" {signals=append(signals,"not Ready")}
		signals=append(signals,pod.Waiting...)
		signals=append(signals,pod.Warnings...)
		if len(signals)==0 {signals=append(signals,"no reported pod-level warning")}
		lines=append(lines,fmt.Sprintf("Pod/%s | node %s | %s | %s",
			pod.Name,node,pod.Phase,strings.Join(signals,"; ")))
	}
	lines=append(lines,"","ASSIGNED NODES")
	if len(report.Nodes)==0 {lines=append(lines,"No assigned node details available.")}
	for _,node:=range report.Nodes {
		lines=append(lines,fmt.Sprintf("Node/%s | Ready=%s | scheduling disabled=%t",
			node.Name,node.Ready,node.SchedulingDisabled))
		for _,signal:=range kube.NodePressureSummary(node) {
			lines=append(lines,"  ATTENTION: "+signal)
		}
		for _,condition:=range node.Conditions {
			if condition.Type=="Ready" || strings.HasSuffix(condition.Type,"Pressure") {
				line:=fmt.Sprintf("  %s=%s",condition.Type,condition.Status)
				if condition.Reason!="" {line+=" ("+condition.Reason+")"}
				if condition.Status=="True" && strings.HasSuffix(condition.Type,"Pressure") && condition.Message!="" {
					line+=" · "+condition.Message
				}
				lines=append(lines,line)
			}
		}
		cpu:="—"
		if node.CPUUsedKnown {cpu=metricCPU(node.CPUUsed,true)}
		if node.CPUCapacityKnown {
			cpu+=" / allocatable "+metricCPU(node.CPUAllocatable,true)
			if node.CPUUsedKnown {cpu+=metricPercent(node.CPUUsed,node.CPUAllocatable,true)}
		}
		mem:="—"
		if node.MemoryUsedKnown {mem=metricMemory(node.MemoryUsed,true)}
		if node.MemoryCapacityKnown {
			mem+=" / allocatable "+metricMemory(node.MemoryAllocatable,true)
			if node.MemoryUsedKnown {mem+=metricPercent(node.MemoryUsed,node.MemoryAllocatable,true)}
		}
		lines=append(lines,"  CPU: "+cpu+"  Memory: "+mem)
		if len(node.Taints)>0 {lines=append(lines,"  Taints: "+strings.Join(node.Taints,", "))}
		if node.MetricsWarning!="" {lines=append(lines,"  INCOMPLETE: "+node.MetricsWarning)}
	}
	return lines
}

func (m pickerModel) nodeDiagnosticModal() (string,int) {
	width,height:=m.modalViewport()
	if width<40 || height<12 {
		return ansi.Hardwrap("Resize to at least 40 × 12 for node diagnostics.\nEsc closes.",max(1,width),true),0
	}
	inner:=min(116,width-4)-4
	title:=headerStyle.Render(truncatePlain("Node diagnostics · "+m.nodeDiag.app,inner))
	scope:=truncatePlain(m.kubeContext+" / "+m.namespace,inner)
	status:="D: assigned-node inspection"
	if m.nodeDiag.busy {status+=" · loading"}
	head:=pickerWrapLines([]string{title,scope,status},inner)
	footer:=pickerWrapLines([]string{"↑↓ PgUp/PgDn Home/End scroll · R refresh · D/Esc close"},inner)
	content:=[]string{}
	for _,line:=range m.nodeDiagnosticLines() {
		line=activityText(line)
		content=append(content,strings.Split(ansi.Hardwrap(line,max(1,inner),true),"\n")...)
	}
	capacity:=max(1,height-4-len(head)-len(footer)-1)
	maximum:=max(0,len(content)-capacity)
	start:=min(maximum,max(0,m.nodeDiag.scroll))
	body:=append([]string(nil),content[start:min(len(content),start+capacity)]...)
	body=append(body,fmt.Sprintf("Lines %d–%d of %d",start+1,min(len(content),start+capacity),len(content)))
	all:=append(head,body...)
	all=append(all,footer...)
	all=all[:min(len(all),height-2)]
	panel:=lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(accentColor).
		Foreground(modalForeground).Background(modalBackground).Padding(0,1).Width(inner+2)
	return panel.Render(strings.Join(all,"\n")),maximum
}

