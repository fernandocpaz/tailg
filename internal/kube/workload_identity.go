package kube

import (
	"context"
	"errors"
	"fmt"

	"github.com/fernandocpaz/tailg/internal/core"
)

// WorkloadIdentities resolves each selected pod to the workload that actually
// owns it. Values are keyed by pod name and have the form Kind/name. If an
// identity cannot be verified, the pod itself is used as a safe fallback and
// the returned error records why attribution was incomplete.
func (r Runner) WorkloadIdentities(ctx context.Context, items []core.InventoryItem) (map[string]string, error) {
	identities := make(map[string]string)
	var errs []error
	seen := make(map[string]struct{})
	for _, item := range items {
		podName := item.Pod
		if _, ok := seen[podName]; ok {
			continue
		}
		seen[podName] = struct{}{}
		if podName == "" {
			errs = append(errs, errors.New("workload identity: selected pod has no name"))
			continue
		}
		identities[podName] = "Pod/" + podName
		if err := ctx.Err(); err != nil {
			errs = append(errs, fmt.Errorf("workload identity for pod %s: %w", podName, err))
			continue
		}

		pod, err := r.JSON(ctx, "get", "pod/"+podName)
		if err != nil {
			errs = append(errs, fmt.Errorf("workload identity for pod %s: could not read pod metadata: %w", podName, err))
			continue
		}
		returnedName := stringValue(mapValue(pod["metadata"])["name"])
		if returnedName == "" {
			errs = append(errs, fmt.Errorf("workload identity for pod %s: pod metadata has no name", podName))
			continue
		}
		if returnedName != podName {
			errs = append(errs, fmt.Errorf("workload identity for pod %s: API returned metadata for pod %s", podName, returnedName))
			continue
		}

		workload, warning := r.resolvePodWorkload(ctx, pod)
		if warning != "" {
			errs = append(errs, fmt.Errorf("workload identity for pod %s: %s", podName, warning))
			continue
		}
		if workload != nil {
			if workload.kind == "" || workload.name == "" {
				errs = append(errs, fmt.Errorf("workload identity for pod %s: resolved owner metadata is incomplete", podName))
				continue
			}
			identities[podName] = workload.kind + "/" + workload.name
		}
	}
	return identities, errors.Join(errs...)
}
