package kube

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestResourceQuantityParsesKubernetesUnits(t *testing.T) {
	for _, test := range []struct{
		input string
		want float64
		ok bool
	}{
		{"125000000n", .125, true}, {"250m", .25, true},
		{"1", 1, true}, {"1.5", 1.5, true},
		{"1024Ki", 1048576, true}, {"256Mi", 268435456, true},
		{"1Gi", 1073741824, true}, {"1G", 1e9, true},
		{"-1", 0, false}, {"bogus", 0, false},
		{"100oops", 0, false}, {"", 0, false},
	} {
		got, ok := resourceQuantity(test.input)
		if ok != test.ok || math.Abs(got-test.want) > 1e-6 {
			t.Errorf("resourceQuantity(%q) = (%g, %t), want (%g, %t)", test.input, got, ok, test.want, test.ok)
		}
	}
}

func TestPodMetricsAggregatesContainersWithRequestsAndLimits(t *testing.T) {
	raw := []byte(`{"items":[
		{"metadata":{"name":"api-a"},"containers":[
			{"name":"api","usage":{"cpu":"125000000n","memory":"256Mi"}},
			{"name":"sidecar","usage":{"cpu":"250m","memory":"128Mi"}}]},
		{"metadata":{"name":"job-completed"},"containers":[]}
	]}`)
	usage, err := parsePodResourceMetrics(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(usage) != 1 || math.Abs(usage["api-a"].CPUMilli-375) > .001 || usage["api-a"].MemoryBytes != 384*1024*1024 {
		t.Fatalf("unexpected pod usage: %+v", usage)
	}
	addPodResourceLimits(usage, []any{map[string]any{
		"metadata":map[string]any{"name":"api-a"},
		"spec":map[string]any{"containers":[]any{
			map[string]any{"name":"api","resources":map[string]any{
				"limits":map[string]any{"cpu":"500m","memory":"512Mi"},
				"requests":map[string]any{"cpu":"200m","memory":"128Mi"}}},
			map[string]any{"name":"sidecar","resources":map[string]any{
				"limits":map[string]any{"cpu":"500m","memory":"512Mi"},
				"requests":map[string]any{"cpu":"100m","memory":"64Mi"}}},
		}},
	}})
	a := usage["api-a"]
	if !a.CPULimitKnown || !a.MemoryLimitKnown || !a.CPURequestKnown || !a.MemoryRequestKnown ||
		a.CPULimitMilli != 1000 || a.CPURequestMilli != 300 || a.MemoryLimitBytes != 1024*1024*1024 {
		t.Fatalf("incorrect total limits: %+v", a)
	}
}

func TestIncompleteMetricsNeverClaimsFullUtilization(t *testing.T) {
	usage, err := parsePodResourceMetrics([]byte(`{"items":[
		{"metadata":{"name":"api"},"containers":[{"name":"api","usage":{"cpu":"100m","memory":"bad"}}]}
	]}`))
	if err != nil {
		t.Fatal(err)
	}
	addPodResourceLimits(usage, []any{map[string]any{
		"metadata":map[string]any{"name":"api"},
		"spec":map[string]any{"containers":[]any{
			map[string]any{"name":"api","resources":map[string]any{"limits":map[string]any{"cpu":"200m","memory":"256Mi"}}},
			map[string]any{"name":"sidecar","resources":map[string]any{"limits":map[string]any{"cpu":"200m","memory":"256Mi"}}},
		}},
	}})
	if !usage["api"].CPUValid || usage["api"].MemoryValid || usage["api"].CPULimitKnown || usage["api"].MemoryLimitKnown {
		t.Fatalf("partial collection incorrectly reported complete data: %+v", usage["api"])
	}
	if _, err := parsePodResourceMetrics([]byte("not json")); err == nil {
		t.Fatal("invalid metrics payload should be an error")
	}
}

func TestNamespaceMetricsQueriesExpectedScopeAndSurvivesPodSpecDenial(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake kubectl shell requires POSIX")
	}
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "calls")
	t.Setenv("METRICS_ARGS_FILE", argsFile)
	script := `#!/bin/sh
printf '%s\n' "$*" >> "$METRICS_ARGS_FILE"
case "$*" in
  *"get --raw /apis/metrics.k8s.io/v1beta1/namespaces/apollo/pods"*)
    echo '{"items":[{"metadata":{"name":"api"},"containers":[{"name":"api","usage":{"cpu":"320m","memory":"512Mi"}}]}]}'
    ;;
  *"get pods -o json"*)
    echo 'Forbidden' >&2
    exit 1
    ;;
  *) exit 2 ;;
esac
`
	binary := filepath.Join(dir,"kubectl")
	if err := os.WriteFile(binary, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	result, err := (Runner{Namespace:"apollo",Context:"tkgs-qa",Binary:binary}).NamespaceResourceMetrics(context.Background())
	if err != nil || len(result.Pods) != 1 || result.Pods["api"].CPUMilli != 320 || result.LimitsWarning == "" {
		t.Fatalf("metrics must survive pod specs access denied: %+v, %v", result, err)
	}
	calls, err := os.ReadFile(argsFile)
	if err != nil || !strings.Contains(string(calls), "--context tkgs-qa -n apollo get --raw") ||
		!strings.Contains(string(calls), "--context tkgs-qa -n apollo get pods -o json") {
		t.Fatalf("wrong kubectl scope: %q %v", calls, err)
	}
}
