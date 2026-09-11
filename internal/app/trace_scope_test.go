package app

import (
	"strings"
	"testing"
)

func TestChildArgsPreserveOriginalTraceScope(t *testing.T) {
	options := Options{
		Context:        "staging",
		TracePods:      []string{"api-old", "worker-old"},
		TraceSelectors: []string{"app=checkout"},
	}
	args := childArgs(options, "payments")("api-old")
	joined := strings.Join(args, " ")
	for _, expected := range []string{"pod/api-old payments", "--context staging", "--trace-pod api-old", "--trace-pod worker-old", "--trace-selector app=checkout"} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("child args missing %q: %s", expected, joined)
		}
	}
	if strings.Contains(joined, "--trace-pod api-new") {
		t.Fatalf("child args used displayed pod instead of original scope: %s", joined)
	}
}

func TestPickerTraceScopeIsRebuiltForEachSelection(t *testing.T) {
	firstPods, firstSelectors := deriveTraceScope(Options{}, []string{"first-pod"}, nil, "", "pod/first-pod", nil, false)
	if len(firstPods) != 1 || firstPods[0] != "first-pod" || len(firstSelectors) != 0 {
		t.Fatalf("first scope = %v, %v", firstPods, firstSelectors)
	}
	secondPods, secondSelectors := deriveTraceScope(Options{}, nil, []string{"app=second"}, "", "pod/*", nil, false)
	if len(secondPods) != 0 || len(secondSelectors) != 1 || secondSelectors[0] != "app=second" {
		t.Fatalf("second scope retained first selection: %v, %v", secondPods, secondSelectors)
	}
}
