package kube

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/fernandocpaz/tailg/internal/core"
)

func TestExplainReplicasDeploymentAndHPA(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake kubectl is a shell script")
	}
	for _, test := range []struct {
		name     string
		hpa      string
		contains []string
	}{
		{name: "configured deployment", hpa: `{"items":[]}`, contains: []string{"Deployment/orders requests 3 replica(s)", "no matching HPA"}},
		{name: "autoscaled deployment", hpa: `{"items":[{"metadata":{"name":"orders-scale"},"spec":{"scaleTargetRef":{"apiVersion":"apps/v1","kind":"Deployment","name":"orders"},"minReplicas":2,"maxReplicas":10},"status":{"currentReplicas":3,"desiredReplicas":5,"conditions":[{"type":"ScalingActive","status":"True","reason":"ValidMetricFound"}]}}]}`, contains: []string{"HPA/orders-scale controls Deployment/orders", "requests 5 replica(s)", "2-10 range", "ScalingActive=True"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			runner := fakeReplicaRunner(t, test.hpa, false)
			explanation, err := runner.ExplainReplicas(context.Background(), "orders-pod")
			if err != nil {
				t.Fatal(err)
			}
			combined := explanation.Summary + " " + strings.Join(explanation.Details, " ")
			for _, expected := range test.contains {
				if !strings.Contains(combined, expected) {
					t.Fatalf("missing %q: %+v", expected, explanation)
				}
			}
			if explanation.Workload != "Deployment/orders" {
				t.Fatalf("workload = %q", explanation.Workload)
			}
		})
	}
}

func TestExplainReplicasReportsPartialHPA(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake kubectl is a shell script")
	}
	runner := fakeReplicaRunner(t, "", true)
	explanation, err := runner.ExplainReplicas(context.Background(), "orders-pod")
	if err != nil || !strings.Contains(strings.Join(explanation.Warnings, " "), "HPA lookup failed") || !strings.Contains(explanation.Summary, "requests 3") {
		t.Fatalf("partial explanation = %+v, err=%v", explanation, err)
	}
	if sameUID(map[string]any{"uid": "old"}, map[string]any{"metadata": map[string]any{"uid": "new"}}) {
		t.Fatal("replacement object was accepted as the owner")
	}
}

func TestWorkloadReplicaSummaries(t *testing.T) {
	tests := []struct {
		workload resolvedWorkload
		want     string
	}{
		{resolvedWorkload{kind: "DaemonSet", name: "agent", object: map[string]any{"status": map[string]any{"desiredNumberScheduled": 4, "numberReady": 4}}}, "one pod on each eligible node: 4 desired, 4 ready"},
		{resolvedWorkload{kind: "Job", name: "batch", object: map[string]any{"spec": map[string]any{"parallelism": 3}}}, "up to 3 pod(s)"},
		{resolvedWorkload{kind: "Deployment", name: "api", object: map[string]any{"spec": map[string]any{"replicas": 3, "strategy": map[string]any{"type": "RollingUpdate", "rollingUpdate": map[string]any{"maxSurge": 1}}}, "status": map[string]any{"currentReplicas": 4, "updatedReplicas": 2, "readyReplicas": 3}}}, "surge can temporarily create extra replicas"},
	}
	for _, test := range tests {
		explanation := core.ReplicaExplanation{}
		buildWorkloadExplanation(&explanation, nil, &test.workload)
		if !strings.Contains(explanation.Summary, test.want) {
			t.Errorf("%s summary = %q, want %q", test.workload.kind, explanation.Summary, test.want)
		}
	}
}

func fakeReplicaRunner(t *testing.T, hpa string, hpaFails bool) Runner {
	t.Helper()
	path := filepath.Join(t.TempDir(), "kubectl")
	hpaCase := "printf '%s' '" + hpa + "'"
	if hpaFails {
		hpaCase = "echo 'forbidden: cannot list horizontalpodautoscalers' >&2; exit 1"
	}
	script := `#!/bin/sh
case "$*" in
  *"get pod/orders-pod -o json"*) printf '%s' '{"metadata":{"name":"orders-pod","namespace":"default","ownerReferences":[{"apiVersion":"apps/v1","kind":"ReplicaSet","name":"orders-rs","uid":"rs-1","controller":true}]}}' ;;
  *"get replicaset/orders-rs -o json"*) printf '%s' '{"apiVersion":"apps/v1","metadata":{"name":"orders-rs","uid":"rs-1","ownerReferences":[{"apiVersion":"apps/v1","kind":"Deployment","name":"orders","uid":"dep-1","controller":true}]}}' ;;
  *"get deployment/orders -o json"*) printf '%s' '{"apiVersion":"apps/v1","metadata":{"name":"orders","uid":"dep-1","generation":2},"spec":{"replicas":3,"strategy":{"type":"RollingUpdate","rollingUpdate":{"maxSurge":1}}},"status":{"observedGeneration":2,"currentReplicas":3,"readyReplicas":3,"availableReplicas":3,"updatedReplicas":3}}' ;;
  *"get hpa -o json"*) ` + hpaCase + ` ;;
  *) echo "unexpected kubectl args: $*" >&2; exit 2 ;;
esac
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return Runner{Namespace: "default", Binary: path}
}
