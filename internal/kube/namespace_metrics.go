package kube

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"strconv"
	"strings"
)

// PodResourceUsage represents actual CPU/memory use reported by Metrics Server.
// Requested/limited quantities are shown only if every measured container has
// the corresponding declaration. Missing metrics never imply zero usage.
type PodResourceUsage struct {
	CPUMilli          float64
	MemoryBytes       float64
	CPUValid          bool
	MemoryValid       bool
	CPULimitMilli     float64
	MemoryLimitBytes  float64
	CPURequestMilli   float64
	MemoryRequestBytes float64
	CPULimitKnown     bool
	MemoryLimitKnown  bool
	CPURequestKnown   bool
	MemoryRequestKnown bool
	Containers        int
}

type NamespaceResourceMetrics struct {
	Pods          map[string]PodResourceUsage
	LimitsWarning string // Optional pod-spec query failed; raw usage remains available.
}

// Kubernetes quantity suffixes include decimal and binary SI units.
// Only finite nonnegative values are accepted. Unknown units never show as 0.
func resourceQuantity(value string) (float64, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false
	}
	numeric := 0
	for numeric < len(value) {
		c := value[numeric]
		if (c >= '0' && c <= '9') || c == '.' || c == '+' || c == '-' || c == 'e' || c == 'E' {
			numeric++
		} else {
			break
		}
	}
	if numeric == 0 {
		return 0, false
	}
	number, err := strconv.ParseFloat(value[:numeric], 64)
	if err != nil || math.IsNaN(number) || math.IsInf(number, 0) || number < 0 {
		return 0, false
	}
	scales := map[string]float64{
		"": 1, "n": 1e-9, "u": 1e-6, "m": 1e-3,
		"k": 1e3, "K": 1e3, "M": 1e6, "G": 1e9, "T": 1e12,
		"P": 1e15, "E": 1e18,
		"Ki": 1024, "Mi": 1024 * 1024, "Gi": 1024 * 1024 * 1024,
		"Ti": math.Pow(1024, 4), "Pi": math.Pow(1024, 5), "Ei": math.Pow(1024, 6),
	}
	factor, ok := scales[value[numeric:]]
	if !ok || math.IsInf(number*factor, 0) {
		return 0, false
	}
	return number * factor, true
}

type metricContainer struct {
	Name  string            `json:"name"`
	Usage map[string]string `json:"usage"`
}
type metricPod struct {
	Metadata struct{ Name string `json:"name"` } `json:"metadata"`
	Containers []metricContainer `json:"containers"`
}
type metricPodsResponse struct {
	Items []metricPod `json:"items"`
}

func parsePodResourceMetrics(raw []byte) (map[string]PodResourceUsage, error) {
	var payload metricPodsResponse
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("decode Kubernetes pod metrics: %w", err)
	}
	pods := make(map[string]PodResourceUsage, len(payload.Items))
	for _, pod := range payload.Items {
		if pod.Metadata.Name == "" || len(pod.Containers) == 0 {
			continue
		}
		usage := PodResourceUsage{CPUValid: true, MemoryValid: true}
		for _, container := range pod.Containers {
			cpu, okCPU := resourceQuantity(container.Usage["cpu"])
			mem, okMem := resourceQuantity(container.Usage["memory"])
			usage.CPUValid = usage.CPUValid && okCPU
			usage.MemoryValid = usage.MemoryValid && okMem
			if okCPU {
				usage.CPUMilli += cpu * 1000
			}
			if okMem {
				usage.MemoryBytes += mem
			}
			usage.Containers++
		}
		pods[pod.Metadata.Name] = usage
	}
	return pods, nil
}

func addPodResourceLimits(pods map[string]PodResourceUsage, raw []any) {
	for _, entry := range raw {
		pod := mapValue(entry)
		name := podName(pod)
		usage, ok := pods[name]
		if !ok {
			continue
		}
		var total int
		cpuLimitOK, memLimitOK, cpuReqOK, memReqOK := true, true, true, true
		for _, item := range sliceValue(mapValue(pod["spec"])["containers"]) {
			container := mapValue(item)
			total++
			resources := mapValue(container["resources"])
			limits, requests := mapValue(resources["limits"]), mapValue(resources["requests"])
			if cpu, ok := resourceQuantity(stringValue(limits["cpu"])); ok {
				usage.CPULimitMilli += cpu * 1000
			} else {
				cpuLimitOK = false
			}
			if mem, ok := resourceQuantity(stringValue(limits["memory"])); ok {
				usage.MemoryLimitBytes += mem
			} else {
				memLimitOK = false
			}
			if cpu, ok := resourceQuantity(stringValue(requests["cpu"])); ok {
				usage.CPURequestMilli += cpu * 1000
			} else {
				cpuReqOK = false
			}
			if mem, ok := resourceQuantity(stringValue(requests["memory"])); ok {
				usage.MemoryRequestBytes += mem
			} else {
				memReqOK = false
			}
		}
		// Never claim a complete limit/request if metrics cover fewer
		// containers than the pod spec (or vice versa).
		matching := total > 0 && total == usage.Containers
		usage.CPULimitKnown = matching && cpuLimitOK && usage.CPULimitMilli > 0
		usage.MemoryLimitKnown = matching && memLimitOK && usage.MemoryLimitBytes > 0
		usage.CPURequestKnown = matching && cpuReqOK && usage.CPURequestMilli > 0
		usage.MemoryRequestKnown = matching && memReqOK && usage.MemoryRequestBytes > 0
		pods[name] = usage
	}
}

func (r Runner) NamespaceResourceMetrics(ctx context.Context) (NamespaceResourceMetrics, error) {
	namespace := r.Namespace
	if namespace == "" {
		namespace = "default"
	}
	// One namespace-scoped Metrics API request rather than a kubectl top process
	// per pod. Do not use top -o json: that flag is not portable across kubectl.
	raw, err := r.Run(ctx, "get", "--raw", "/apis/metrics.k8s.io/v1beta1/namespaces/"+url.PathEscape(namespace)+"/pods")
	if err != nil {
		return NamespaceResourceMetrics{}, err
	}
	pods, err := parsePodResourceMetrics([]byte(raw))
	if err != nil {
		return NamespaceResourceMetrics{}, err
	}
	result := NamespaceResourceMetrics{Pods: pods}
	specs, err := r.JSON(ctx, "get", "pods")
	if err != nil {
		result.LimitsWarning = "request/limit information unavailable"
		return result, nil
	}
	addPodResourceLimits(result.Pods, sliceValue(specs["items"]))
	return result, nil
}
