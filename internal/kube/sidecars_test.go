package kube

import (
	"encoding/json"
	"maps"
	"slices"
	"strings"
	"testing"
)

func TestSidecarDetectionAndInventory(t *testing.T) {
	var pod map[string]any
	if err := json.Unmarshal([]byte(`{
		"metadata": {"name": "api", "uid": "api-uid", "annotations": {
			"sidecar.istio.io/status": "{\"containers\":[\"istio-proxy\",\"removed\"],\"initContainers\":[\"migrate\"]}",
			"linkerd.io/proxy-version": "stable-example"
		}},
		"spec": {
			"containers": [{"name":"app"}, {"name":"worker"}, {"name":"istio-proxy"}, {"name":"linkerd-proxy"}],
			"initContainers": [{"name":"migrate"}, {"name":"logger","restartPolicy":"Always"}],
			"ephemeralContainers": [{"name":"debug"}]
		},
		"status": {
			"phase": "Succeeded",
			"initContainerStatuses": [{"name":"logger","containerID":"containerd://logger","restartCount":2}]
		}
	}`), &pod); err != nil {
		t.Fatal(err)
	}
	if got := slices.Sorted(maps.Keys(podSidecars(pod))); !slices.Equal(got, []string{"istio-proxy", "linkerd-proxy", "logger"}) {
		t.Fatalf("detected sidecars = %v", got)
	}

	var names []string
	for _, item := range inventoryFromPods([]any{pod}) {
		names = append(names, item.Container)
		if item.Container == "logger" && (item.PodUID != "api-uid" || item.ContainerID != "containerd://logger" || item.RestartCount != 2 || !item.Completed) {
			t.Fatalf("native sidecar lost runtime state: %+v", item)
		}
	}
	if !slices.Equal(names, []string{"app", "istio-proxy", "linkerd-proxy", "logger", "worker"}) {
		t.Fatalf("log inventory = %v", names)
	}

	report := ImageVersionsReport(map[string]any{"items": []any{pod}}, "apollo")
	for name, role := range map[string]string{"logger": "sidecar", "linkerd-proxy": "sidecar", "istio-proxy": "sidecar", "worker": "app", "migrate": "init", "debug": "ephemeral"} {
		found := false
		for _, row := range strings.Split(report, "\n") {
			cells := strings.Split(row, "|")
			if len(cells) > 3 && strings.TrimSpace(cells[2]) == name && strings.TrimSpace(cells[3]) == role {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("missing %s with role %s:\n%s", name, role, report)
		}
	}

	annotations := mapValue(mapValue(pod["metadata"])["annotations"])
	annotations["sidecar.istio.io/status"] = `{"containers":["istio-proxy", false]}`
	delete(annotations, "linkerd.io/proxy-version")
	if got := slices.Sorted(maps.Keys(podSidecars(pod))); !slices.Equal(got, []string{"logger"}) {
		t.Fatalf("invalid or missing annotations produced sidecars: %v", got)
	}
}
