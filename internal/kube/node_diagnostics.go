package kube

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"
)

const maxDiagnosticNodes = 6

// PodNodeDiagnostic is a point-in-time view of a pod from a single namespace.
type PodNodeDiagnostic struct {
	Name         string
	Node         string
	Phase        string
	Restarts     int
	Ready        bool
	ReadyKnown   bool
	OOMKilled    bool
	Waiting      []string
	Warnings     []string
}

type NodePressureCondition struct {
	Type    string
	Status  string
	Reason  string
	Message string
}

// NodeDiagnostic combines Kubernetes node conditions with optional
// Metrics Server usage. Absent metrics are never interpreted as zero.
type NodeDiagnostic struct {
	Name              string
	Ready             string
	SchedulingDisabled bool
	Conditions        []NodePressureCondition
	Taints            []string
	CPUAllocatable    float64 // millicores
	MemoryAllocatable float64 // bytes
	CPUCapacityKnown  bool
	MemoryCapacityKnown bool
	CPUUsed           float64 // millicores
	MemoryUsed        float64 // bytes
	CPUUsedKnown      bool
	MemoryUsedKnown   bool
	MetricsWarning    string
	Problem           string
}

type WorkloadNodeDiagnostics struct {
	Context, Namespace, Application string
	CapturedAt time.Time
	Pods       []PodNodeDiagnostic
	Nodes      []NodeDiagnostic
	Warnings   []string
}

// WorkloadNodeDiagnostics reads only named pods from the active namespace and
// their assigned node objects. It avoids listing all nodes or all namespaces.
// Missing permissions or Metrics Server are reported as incomplete evidence.
func (r Runner) WorkloadNodeDiagnostics(ctx context.Context, app string, names []string) WorkloadNodeDiagnostics {
	result := WorkloadNodeDiagnostics{Context:r.Context, Namespace:r.Namespace, Application:app, CapturedAt:time.Now()}
	if r.Context == "" || r.Namespace == "" {
		result.Warnings = append(result.Warnings,"Explicit Kubernetes context and namespace are required")
		return result
	}
	if len(names) == 0 {
		result.Warnings = append(result.Warnings,"No current pods for the selected application")
		return result
	}
	want := make(map[string]struct{},len(names))
	for _, name := range names {
		if name != "" { want[name]=struct{}{} }
	}
	payload, err := r.JSON(ctx,"get","pods","--request-timeout=10s")
	if err != nil {
		result.Warnings = append(result.Warnings,"Namespace pod inventory unavailable: "+err.Error())
		return result
	}
	list, ok := payload["items"].([]any)
	if !ok {
		result.Warnings=append(result.Warnings,"Namespace pod inventory response is missing items")
		return result
	}
	assigned := make(map[string]struct{})
	for _, raw := range list {
		pod := mapValue(raw)
		meta := mapValue(pod["metadata"])
		name := stringValue(meta["name"])
		if _, ok := want[name]; !ok { continue }
		if ns:=stringValue(meta["namespace"]); ns != "" && ns!=r.Namespace {continue}
		status := parsePodNodeDiagnostic(pod)
		result.Pods = append(result.Pods,status)
		delete(want,name)
		if status.Node!="" { assigned[status.Node]=struct{}{} }
	}
	for missing := range want {
		result.Warnings=append(result.Warnings,fmt.Sprintf("Pod/%s is no longer returned by namespace inventory",missing))
	}
	sort.Slice(result.Pods,func(i,j int)bool{return result.Pods[i].Name<result.Pods[j].Name})
	nodes:=make([]string,0,len(assigned))
	for name:=range assigned {nodes=append(nodes,name)}
	sort.Strings(nodes)
	if len(nodes)>maxDiagnosticNodes {
		result.Warnings=append(result.Warnings,fmt.Sprintf("Showing first %d of %d assigned nodes (read-query limit)",maxDiagnosticNodes,len(nodes)))
		nodes=nodes[:maxDiagnosticNodes]
	}
	// Avoid repeatedly querying cluster-scoped nodes across all workloads:
	// only get named nodes, and tolerate RBAC-denied node or metric reads.
	nodeRunner:=r
	nodeRunner.Namespace=""
	for _,name:=range nodes {
		if err:=ctx.Err();err!=nil {
			result.Warnings=append(result.Warnings,"Diagnostics timed out; remaining node evidence is unavailable")
			break
		}
		node:=NodeDiagnostic{Name:name}
		details,err:=nodeRunner.JSON(ctx,"get","node",name,"--request-timeout=5s")
		if err!=nil {
			node.Problem="Node details unavailable (RBAC/network): "+err.Error()
			result.Nodes=append(result.Nodes,node)
			continue
		}
		node=parseNodeDiagnostic(details)
		if node.Name=="" {node.Name=name}
		// The Metrics API is optional; node conditions remain useful without it.
		// The raw API endpoint is generated from a Kubernetes-reported node name.
		raw,err:=nodeRunner.Run(ctx,"get","--raw","/apis/metrics.k8s.io/v1beta1/nodes/"+url.PathEscape(name))
		if err!=nil {
			node.MetricsWarning="Node usage unavailable (Metrics Server/RBAC)"
		} else if !addNodeUsage(&node,[]byte(raw),name) {
			node.MetricsWarning="Node usage unavailable (invalid metrics response)"
		}
		result.Nodes=append(result.Nodes,node)
	}
	sort.Strings(result.Warnings)
	result.CapturedAt=time.Now()
	return result
}

func parsePodNodeDiagnostic(item map[string]any) PodNodeDiagnostic {
	meta:=mapValue(item["metadata"])
	spec:=mapValue(item["spec"])
	status:=mapValue(item["status"])
	pod:=PodNodeDiagnostic{Name:stringValue(meta["name"]),Node:stringValue(spec["nodeName"]),Phase:stringValue(status["phase"])}
	if pod.Node=="" {pod.Warnings=append(pod.Warnings,"Pending scheduling / node unassigned")}
	if pod.Phase!="Running" && pod.Phase!="Succeeded" && pod.Phase!="" {
		pod.Warnings=append(pod.Warnings,"Pod phase: "+pod.Phase)
	}
	reason:=stringValue(status["reason"])
	if reason!="" && reason!="Succeeded" {pod.Warnings=append(pod.Warnings,"Pod reason: "+reason)}
	ready:=false
	for _,raw:=range sliceValue(status["conditions"]) {
		condition:=mapValue(raw)
		if stringValue(condition["type"])=="Ready" {
			pod.ReadyKnown=true
			if stringValue(condition["status"])=="True" {ready=true}
		}
	}
	pod.Ready=ready
	for _,group:=range []string{"containerStatuses","initContainerStatuses"} {
		for _,raw:=range sliceValue(status[group]) {
			container:=mapValue(raw)
			pod.Restarts+=intValue(container["restartCount"])
			terminated:=mapValue(mapValue(container["lastState"])["terminated"])
			currentTerminated:=mapValue(mapValue(container["state"])["terminated"])
			if stringValue(terminated["reason"])=="OOMKilled" || stringValue(currentTerminated["reason"])=="OOMKilled" {
				pod.OOMKilled=true
			}
			waiting:=mapValue(mapValue(container["state"])["waiting"])
			if statusReason:=stringValue(waiting["reason"]);statusReason!="" {
				pod.Waiting=append(pod.Waiting,stringValue(container["name"])+": "+statusReason)
			}
		}
	}
	sort.Strings(pod.Waiting)
	return pod
}

func parseNodeDiagnostic(item map[string]any) NodeDiagnostic {
	meta:=mapValue(item["metadata"])
	spec:=mapValue(item["spec"])
	status:=mapValue(item["status"])
	node:=NodeDiagnostic{Name:stringValue(meta["name"]),SchedulingDisabled:boolValue(spec["unschedulable"])}
	for _,raw:=range sliceValue(status["conditions"]) {
		condition:=mapValue(raw)
		kind:=stringValue(condition["type"])
		if kind!="Ready" && kind!="MemoryPressure" && kind!="DiskPressure" && kind!="PIDPressure" {continue}
		state:=NodePressureCondition{Type:kind,Status:stringValue(condition["status"]),Reason:stringValue(condition["reason"]),Message:stringValue(condition["message"])}
		node.Conditions=append(node.Conditions,state)
		if kind=="Ready" {node.Ready=state.Status}
	}
	if node.Ready=="" {node.Ready="Unknown"}
	sort.Slice(node.Conditions,func(i,j int)bool{return node.Conditions[i].Type<node.Conditions[j].Type})
	for _,raw:=range sliceValue(spec["taints"]) {
		taint:=mapValue(raw)
		if effect:=stringValue(taint["effect"]);effect!="" {
			node.Taints=append(node.Taints,fmt.Sprintf("%s:%s",stringValue(taint["key"]),effect))
		}
	}
	sort.Strings(node.Taints)
	capacity:=mapValue(status["allocatable"])
	if v,ok:=resourceQuantity(stringValue(capacity["cpu"]));ok && v>0 {
		node.CPUAllocatable=v*1000;node.CPUCapacityKnown=true
	}
	if v,ok:=resourceQuantity(stringValue(capacity["memory"]));ok && v>0 {
		node.MemoryAllocatable=v;node.MemoryCapacityKnown=true
	}
	return node
}

func addNodeUsage(node *NodeDiagnostic,raw []byte, expectedName string) bool {
	var value struct {
		Metadata struct { Name string `json:"name"` } `json:"metadata"`
		Usage map[string]string `json:"usage"`
	}
	if err:=json.Unmarshal(raw,&value);err!=nil||value.Metadata.Name!=expectedName {return false}
	if cpu,ok:=resourceQuantity(value.Usage["cpu"]);ok {node.CPUUsed=cpu*1000;node.CPUUsedKnown=true}
	if mem,ok:=resourceQuantity(value.Usage["memory"]);ok {node.MemoryUsed=mem;node.MemoryUsedKnown=true}
	return node.CPUUsedKnown||node.MemoryUsedKnown
}

// NodePressureSummary offers explicit signals, not a binary "healthy" claim.
func NodePressureSummary(node NodeDiagnostic) []string {
	var result []string
	if node.Problem!="" {return []string{node.Problem}}
	if node.Ready!="True" {result=append(result,"Node Ready="+node.Ready)}
	if node.SchedulingDisabled {result=append(result,"Scheduling disabled")}
	for _,condition:=range node.Conditions {
		if strings.HasSuffix(condition.Type,"Pressure") && condition.Status=="True" {
			result=append(result,condition.Type+"=True")
		}
	}
	if node.MemoryCapacityKnown && node.MemoryUsedKnown && node.MemoryUsed/node.MemoryAllocatable>=0.9 {
		result=append(result,"Node memory usage ≥90% allocatable")
	}
	if node.CPUCapacityKnown && node.CPUUsedKnown && node.CPUUsed/node.CPUAllocatable>=0.9 {
		result=append(result,"Node CPU usage ≥90% allocatable (instantaneous sample)")
	}
	return result
}
