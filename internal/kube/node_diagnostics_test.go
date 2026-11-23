package kube

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestNodeDiagnosticsDetectPodOOMAndWaitingReasons(t *testing.T) {
	pod := parsePodNodeDiagnostic(map[string]any{
		"metadata": map[string]any{"name":"api-42"},
		"spec": map[string]any{"nodeName":"worker-1"},
		"status": map[string]any{
			"phase":"Running",
			"conditions":[]any{map[string]any{"type":"Ready","status":"False"}},
			"containerStatuses":[]any{
				map[string]any{"name":"api","restartCount":3,"lastState":map[string]any{"terminated":map[string]any{"reason":"OOMKilled"}}},
				map[string]any{"name":"sidecar","restartCount":1,"state":map[string]any{"waiting":map[string]any{"reason":"CrashLoopBackOff"}}},
			},
		},
	})
	if pod.Name!="api-42" || pod.Node!="worker-1" || pod.Restarts!=4 || !pod.OOMKilled || pod.Ready || !pod.ReadyKnown ||
		len(pod.Waiting)!=1 || pod.Waiting[0]!="sidecar: CrashLoopBackOff" {
		t.Fatalf("unexpected pod diagnostic: %+v",pod)
	}
}

func TestNodeDiagnosticsParseNodeConditionsAllocatableAndMetrics(t *testing.T) {
	node:=parseNodeDiagnostic(map[string]any{
		"metadata":map[string]any{"name":"worker-1"},
		"spec":map[string]any{
			"unschedulable":true,
			"taints":[]any{map[string]any{"key":"maintenance","effect":"NoSchedule"}},
		},
		"status":map[string]any{
			"allocatable":map[string]any{"cpu":"4000m","memory":"8Gi"},
			"conditions":[]any{
				map[string]any{"type":"Ready","status":"False","reason":"KubeletNotReady"},
				map[string]any{"type":"MemoryPressure","status":"True","reason":"KubeletHasInsufficientMemory"},
				map[string]any{"type":"DiskPressure","status":"False"},
			},
		},
	})
	if !addNodeUsage(&node,[]byte(`{"metadata":{"name":"worker-1"},"usage":{"cpu":"3700m","memory":"7800Mi"}}`),"worker-1") {
		t.Fatal("node metrics should parse")
	}
	if node.Name!="worker-1" || node.Ready!="False" || !node.SchedulingDisabled ||
		!node.CPUCapacityKnown || !node.MemoryCapacityKnown || !node.CPUUsedKnown || !node.MemoryUsedKnown ||
		node.CPUAllocatable!=4000 || node.CPUUsed!=3700 || len(node.Taints)!=1 {
		t.Fatalf("incomplete node diagnostic: %+v",node)
	}
	signals:=strings.Join(NodePressureSummary(node),"|")
	for _,expected:=range []string{"Node Ready=False","Scheduling disabled","MemoryPressure=True","Node memory usage","Node CPU usage"} {
		if !strings.Contains(signals,expected) {t.Errorf("missing signal %q in %q",expected,signals)}
	}
	empty:=NodeDiagnostic{Name:"n",Ready:"True"}
	if len(NodePressureSummary(empty))!=0 {t.Fatal("unknown metrics should not imply pressure")}
	if addNodeUsage(&empty,[]byte(`{"metadata":{"name":"different"},"usage":{"cpu":"4","memory":"5Gi"}}`),"n") {
		t.Fatal("other-node metrics must be rejected")
	}
}

func TestNodeDiagnosticsReadOnlyScopedQueriesAndPartialMetrics(t *testing.T) {
	if runtime.GOOS=="windows" {t.Skip("POSIX fake kubectl required")}
	dir:=t.TempDir()
	log:=filepath.Join(dir,"commands")
	t.Setenv("NODE_DIAG_CALLS",log)
	script:=`#!/bin/sh
printf '%s
' "$*" >> "$NODE_DIAG_CALLS"
case "$*" in
  *"get pods --request-timeout=10s -o json"*)
    echo '{"items":[{"metadata":{"name":"api-1","namespace":"apollo"},"spec":{"nodeName":"node-1"},"status":{"phase":"Running","containerStatuses":[{"name":"api","restartCount":2}]}},{"metadata":{"name":"other","namespace":"apollo"},"spec":{"nodeName":"node-2"},"status":{"phase":"Running"}}]}'
    ;;
  *"get node node-1 --request-timeout=5s -o json"*)
    echo '{"metadata":{"name":"node-1"},"status":{"allocatable":{"cpu":"2","memory":"4Gi"},"conditions":[{"type":"Ready","status":"True"},{"type":"DiskPressure","status":"True","reason":"KubeletHasDiskPressure"}]}}'
    ;;
  *"get --raw /apis/metrics.k8s.io/v1beta1/nodes/node-1"*)
    echo 'metrics API denied' >&2; exit 1 ;;
  *) echo "unexpected command" >&2; exit 3 ;;
esac
`
	binary:=filepath.Join(dir,"kubectl")
	if err:=os.WriteFile(binary,[]byte(script),0755);err!=nil{t.Fatal(err)}
	got:=(Runner{Namespace:"apollo",Context:"tkgs-qa",Binary:binary}).
		WorkloadNodeDiagnostics(context.Background(),"api",[]string{"api-1"})
	if len(got.Pods)!=1 || got.Pods[0].Name!="api-1" || len(got.Nodes)!=1 ||
		got.Nodes[0].Name!="node-1" || got.Nodes[0].Ready!="True" ||
		got.Nodes[0].MetricsWarning=="" {
		t.Fatalf("partial metrics should preserve node conditions: %+v",got)
	}
	calls,err:=os.ReadFile(log)
	if err!=nil {t.Fatal(err)}
	lines:=strings.Split(strings.TrimSpace(string(calls)),"\n")
	if len(lines)!=3 {
		t.Fatalf("expected 3 bounded read-only calls, got %d: %q",len(lines),calls)
	}
	for _,line:=range lines {
		if strings.Contains(line,"delete") || strings.Contains(line,"patch") ||
			strings.Contains(line,"-A") || strings.Contains(line,"--all-namespaces") ||
			strings.Contains(line,"get nodes") {
			t.Fatalf("unscoped or mutating call: %q",line)
		}
		if strings.Contains(line,"get node node-1") && strings.Contains(line," -n ") {
			t.Fatalf("node request must not include namespace: %q",line)
		}
	}
	if !strings.Contains(lines[0],"--context tkgs-qa -n apollo get pods") ||
		!strings.Contains(lines[1],"--context tkgs-qa get node node-1") ||
		!strings.Contains(lines[2],"--context tkgs-qa get --raw") {
		t.Fatalf("wrong scope: %q",lines)
	}
}

func TestNodeDiagnosticsNodeAccessDeniedDoesNotClaimHealthy(t *testing.T) {
	if runtime.GOOS=="windows" {t.Skip("POSIX fake kubectl required")}
	dir:=t.TempDir()
	script:=`#!/bin/sh
case "$*" in
  *"get pods --request-timeout=10s -o json"*)
    echo '{"items":[{"metadata":{"name":"api-1"},"spec":{"nodeName":"node-1"},"status":{"phase":"Running"}}]}'
    ;;
  *"get node node-1 --request-timeout=5s -o json"*)
    echo "Forbidden" >&2; exit 1 ;;
  *) exit 3 ;;
esac
`
	binary:=filepath.Join(dir,"kubectl")
	if err:=os.WriteFile(binary,[]byte(script),0755);err!=nil{t.Fatal(err)}
	got:=(Runner{Namespace:"apollo",Context:"tkgs-qa",Binary:binary}).
		WorkloadNodeDiagnostics(context.Background(),"api",[]string{"api-1"})
	if len(got.Pods)!=1 || len(got.Nodes)!=1 || got.Nodes[0].Problem=="" ||
		len(NodePressureSummary(got.Nodes[0]))==0 {
		t.Fatalf("RBAC denial hidden or pod data lost: %+v",got)
	}
	if got.Nodes[0].Ready=="True" {
		t.Fatal("denied node detail should never default to Ready")
	}
}
