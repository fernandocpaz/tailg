package kube

import (
	"encoding/json"
	"slices"
)

func isNativeSidecar(container map[string]any) bool {
	return stringValue(container["restartPolicy"]) == "Always"
}

// podSidecars finds native sidecars and recognizes legacy mesh sidecars from
// injector metadata. Regular containers have no general sidecar marker.
func podSidecars(pod map[string]any) map[string]bool {
	spec := mapValue(pod["spec"])
	sidecars := map[string]bool{}
	for _, raw := range sliceValue(spec["initContainers"]) {
		container := mapValue(raw)
		if name := stringValue(container["name"]); name != "" && isNativeSidecar(container) {
			sidecars[name] = true
		}
	}

	annotations := mapValue(mapValue(pod["metadata"])["annotations"])
	var istio struct {
		Containers []string `json:"containers"`
	}
	if status := stringValue(annotations["sidecar.istio.io/status"]); status != "" {
		if err := json.Unmarshal([]byte(status), &istio); err != nil {
			istio.Containers = nil
		}
	}
	linkerdInjected := stringValue(annotations["linkerd.io/proxy-version"]) != ""
	if len(istio.Containers) == 0 && !linkerdInjected {
		return sidecars
	}
	for _, raw := range sliceValue(spec["containers"]) {
		name := stringValue(mapValue(raw)["name"])
		if name != "" && (slices.Contains(istio.Containers, name) || (linkerdInjected && name == "linkerd-proxy")) {
			sidecars[name] = true
		}
	}
	return sidecars
}
