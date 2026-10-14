package kube

import (
	"context"
	"sort"
	"sync"

	"github.com/fernandocpaz/tailg/internal/core"
)

type podSlot struct {
	current  string
	workload string
}

// PodFollower keeps one stream source per selected pod. It stays with that pod
// until deletion/termination, then follows a verified controller replacement.
// Standalone pods and Jobs remain pinned. Calls may come from live inventory
// and history searches concurrently.
type PodFollower struct {
	mu     sync.Mutex
	runner Runner
	slots  []podSlot
}

func (r Runner) FollowPods(pods []string) *PodFollower {
	f := &PodFollower{runner: r}
	for _, name := range uniqueStrings(pods) {
		f.slots = append(f.slots, podSlot{current: name})
	}
	return f
}

func (f *PodFollower) Inventory(ctx context.Context) ([]core.InventoryItem, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.slots) == 0 {
		return nil, nil
	}
	payload, err := f.runner.JSON(ctx, "get", "pods")
	if err != nil {
		return nil, err
	}
	return f.fromPods(ctx, sliceValue(payload["items"]))
}

func (f *PodFollower) fromPods(ctx context.Context, rawPods []any) ([]core.InventoryItem, error) {
	pods := make(map[string]map[string]any, len(rawPods))
	for _, raw := range rawPods {
		pod := mapValue(raw)
		pods[podName(pod)] = pod
	}
	// Resolve owner objects once per snapshot, even with many selected replicas.
	cache := map[string]map[string]any{}
	get := func(ctx context.Context, kind, name string) (map[string]any, error) {
		key := resourceToken(kind, name)
		if obj, ok := cache[key]; ok {
			return obj, nil
		}
		obj, err := f.runner.JSON(ctx, "get", key)
		if err == nil {
			cache[key] = obj
		}
		return obj, err
	}
	identity := func(pod map[string]any) string {
		owner, warning := f.runner.resolvePodWorkloadWith(ctx, pod, get)
		if owner == nil || warning != "" {
			return ""
		}
		switch owner.kind {
		case "Deployment", "StatefulSet", "DaemonSet", "ReplicaSet":
			return owner.kind + "/" + owner.name
		}
		return ""
	}
	available := func(pod map[string]any) bool {
		return pod != nil && stringValue(mapValue(pod["metadata"])["deletionTimestamp"]) == ""
	}
	used := map[string]bool{}
	// Reserve surviving selections before choosing replacements so two slots
	// cannot latch onto the same replica.
	for i := range f.slots {
		slot := &f.slots[i]
		if pod := pods[slot.current]; pod != nil {
			if slot.workload == "" {
				slot.workload = identity(pod)
			}
			if available(pod) {
				used[slot.current] = true
			}
		}
	}
	var candidates []map[string]any
	for _, pod := range pods {
		if available(pod) && podPhase(pod) != "Succeeded" && podPhase(pod) != "Failed" {
			candidates = append(candidates, pod)
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		left := parseKubeTimestamp(mapValue(candidates[i]["metadata"])["creationTimestamp"])
		right := parseKubeTimestamp(mapValue(candidates[j]["metadata"])["creationTimestamp"])
		if !left.Equal(right) {
			return left.After(right)
		}
		return podName(candidates[i]) < podName(candidates[j])
	})
	var selected []any
	identities := map[string]string{}
	for i := range f.slots {
		slot := &f.slots[i]
		pod := pods[slot.current]
		if !available(pod) && slot.workload != "" {
			pod = nil
			for _, candidate := range candidates {
				name := podName(candidate)
				if used[name] {
					continue
				}
				owner, ok := identities[name]
				if !ok {
					owner = identity(candidate)
					identities[name] = owner
				}
				if owner == slot.workload {
					slot.current, pod, used[name] = name, candidate, true
					break
				}
			}
		}
		if pod != nil {
			selected = append(selected, pod)
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return dedupeInventory(inventoryFromPods(selected)), nil
}
