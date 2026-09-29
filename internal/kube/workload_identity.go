package kube

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/fernandocpaz/tailg/internal/core"
)

// WorkloadIdentities resolves each selected pod to the workload that actually
// owns it. Pod metadata is fetched once; owner objects are fetched at most once
// per kind and name. Values are keyed by pod name and have the form Kind/name.
// If an identity cannot be verified, the pod itself is used as a safe fallback
// and the returned error records why attribution was incomplete.
func (r Runner) WorkloadIdentities(ctx context.Context, items []core.InventoryItem) (map[string]string, error) {
	identities := make(map[string]string)
	selected := make(map[string]bool)
	for _, item := range items {
		if item.Pod != "" {
			selected[item.Pod] = true
		}
	}
	for pod := range selected {
		identities[pod] = "Pod/" + pod
	}
	if len(selected) == 0 {
		return identities, nil
	}

	payload, err := r.JSON(ctx, "get", "pods")
	if err != nil {
		return identities, fmt.Errorf("workload identity: could not read pod inventory: %w", err)
	}
	pods := make(map[string]map[string]any)
	for _, raw := range sliceValue(payload["items"]) {
		pod := mapValue(raw)
		name := stringValue(mapValue(pod["metadata"])["name"])
		if name != "" {
			pods[name] = pod
		}
	}

	type resourceResult struct {
		object map[string]any
		err    error
	}
	resourceCache := make(map[string]resourceResult)
	getResource := func(ctx context.Context, kind, name string) (map[string]any, error) {
		key := strings.ToLower(kind) + "\x00" + name
		if cached, ok := resourceCache[key]; ok {
			return cached.object, cached.err
		}
		object, fetchErr := r.JSON(ctx, "get", resourceToken(kind, name))
		resourceCache[key] = resourceResult{object: object, err: fetchErr}
		return object, fetchErr
	}

	podNames := make([]string, 0, len(selected))
	for podName := range selected {
		podNames = append(podNames, podName)
	}
	sort.Strings(podNames)
	var errs []error
	omittedErrors := 0
	addError := func(err error) {
		if len(errs) < 10 {
			errs = append(errs, err)
		} else {
			omittedErrors++
		}
	}
	for _, podName := range podNames {
		if err := ctx.Err(); err != nil {
			addError(fmt.Errorf("workload identity for pod %s: %w", podName, err))
			continue
		}
		pod, ok := pods[podName]
		if !ok {
			addError(fmt.Errorf("workload identity for pod %s: pod was absent from the metadata snapshot", podName))
			continue
		}
		workload, warning := r.resolvePodWorkloadWith(ctx, pod, getResource)
		if warning != "" {
			addError(fmt.Errorf("workload identity for pod %s: %s", podName, warning))
			continue
		}
		if workload == nil {
			continue
		}
		if workload.kind == "" || workload.name == "" {
			addError(fmt.Errorf("workload identity for pod %s: resolved owner metadata is incomplete", podName))
			continue
		}
		identities[podName] = workload.kind + "/" + workload.name
	}
	if omittedErrors > 0 {
		errs = append(errs, fmt.Errorf("%d additional pod workload identities could not be verified", omittedErrors))
	}
	return identities, errors.Join(errs...)
}
