package kube

import (
	"context"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/fernandocpaz/tailg/internal/core"
)

var appLabelKeys = []string{"app.kubernetes.io/name", "app", "k8s-app", "app.kubernetes.io/instance", "component"}

func (r Runner) ResolveTarget(ctx context.Context, target string) (string, error) {
	if strings.Contains(target, "/") {
		if _, err := r.JSON(ctx, "get", target); err != nil {
			return "", err
		}
		return target, nil
	}
	for _, kind := range []string{"deployment", "statefulset", "daemonset", "job", "pod"} {
		candidate := kind + "/" + target
		if _, err := r.JSON(ctx, "get", candidate); err == nil {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("could not find deployment, statefulset, daemonset, job, or pod named %q", target)
}

func (r Runner) SelectorForResource(ctx context.Context, target string) (string, error) {
	kind, name, _ := strings.Cut(target, "/")
	if kind == "pod" {
		return "", nil
	}
	payload, err := r.JSON(ctx, "get", target)
	if err != nil {
		return "", err
	}
	selector := mapValue(mapValue(payload["spec"])["selector"])
	labels := mapValue(selector["matchLabels"])
	var parts []string
	for key, value := range labels {
		parts = append(parts, key+"="+stringValue(value))
	}
	sort.Strings(parts)
	if len(parts) > 0 {
		return strings.Join(parts, ","), nil
	}
	if kind == "job" {
		return "job-name=" + name, nil
	}
	return "", fmt.Errorf("%s has no usable spec.selector.matchLabels", target)
}

func (r Runner) Inventory(ctx context.Context, target, selector string) ([]core.InventoryItem, error) {
	if strings.HasPrefix(target, "pod/") && target != "pod/*" {
		payload, err := r.JSON(ctx, "get", target)
		if err != nil {
			return nil, err
		}
		return inventoryFromPods([]any{payload}), nil
	}
	args := []string{"get", "pods"}
	if selector != "" {
		args = append(args, "-l", selector)
	}
	payload, err := r.JSON(ctx, args...)
	if err != nil {
		return nil, err
	}
	return inventoryFromPods(sliceValue(payload["items"])), nil
}

func (r Runner) InventoryForPods(ctx context.Context, pods []string) ([]core.InventoryItem, error) {
	var result []core.InventoryItem
	for _, pod := range uniqueStrings(pods) {
		items, err := r.Inventory(ctx, "pod/"+pod, "")
		if err != nil {
			return nil, err
		}
		result = append(result, items...)
	}
	return dedupeInventory(result), nil
}

func (r Runner) InventoryForSelectors(ctx context.Context, selectors []string) ([]core.InventoryItem, error) {
	var result []core.InventoryItem
	for _, selector := range uniqueStrings(selectors) {
		items, err := r.Inventory(ctx, "pod/*", selector)
		if err != nil {
			return nil, err
		}
		result = append(result, items...)
	}
	return dedupeInventory(result), nil
}

func (r Runner) Apps(ctx context.Context) ([]core.AppChoice, error) {
	payload, err := r.JSON(ctx, "get", "pods")
	if err != nil {
		return nil, err
	}
	effectiveNamespace := r.Namespace
	if effectiveNamespace == "" {
		_, effectiveNamespace, err = r.CurrentContext(ctx)
		if err != nil {
			return nil, err
		}
	}
	type group struct {
		pods      []string
		selector  string
		readyPods int
		phases    map[string]int
		restarts  int
	}
	groups := map[string]*group{}
	for _, raw := range sliceValue(payload["items"]) {
		pod := mapValue(raw)
		name := podName(pod)
		if name == "" {
			continue
		}
		appName, selector := appIdentity(pod)
		selected := groups[appName]
		if selected == nil {
			selected = &group{selector: selector, phases: map[string]int{}}
			groups[appName] = selected
		}
		selected.pods = append(selected.pods, name)
		if selected.selector == "" {
			selected.selector = selector
		}
		ready, total := readyCounts(pod)
		if total > 0 && ready == total {
			selected.readyPods++
		}
		selected.phases[podPhase(pod)]++
		selected.restarts += restartCount(pod)
	}
	apps := make([]core.AppChoice, 0, len(groups))
	for name, group := range groups {
		sort.Strings(group.pods)
		apps = append(apps, core.AppChoice{
			Namespace: effectiveNamespace, Name: name, Pods: group.pods, Selector: group.selector,
			Ready: fmt.Sprintf("%d/%d", group.readyPods, len(group.pods)), Phases: phaseSummary(group.phases), Restarts: group.restarts,
		})
	}
	sort.Slice(apps, func(i, j int) bool {
		iRunning := strings.HasPrefix(apps[i].Phases, "Running")
		jRunning := strings.HasPrefix(apps[j].Phases, "Running")
		if iRunning != jRunning {
			return iRunning
		}
		return strings.ToLower(apps[i].Name) < strings.ToLower(apps[j].Name)
	})
	return apps, nil
}

func (r Runner) MatchApps(ctx context.Context, targets string) ([]string, []string, string, error) {
	apps, err := r.Apps(ctx)
	if err != nil {
		return nil, nil, "", err
	}
	patterns := strings.Split(targets, ",")
	var selected []core.AppChoice
	for _, patternText := range patterns {
		patternText = strings.TrimSpace(patternText)
		if patternText == "" {
			continue
		}
		var matches []core.AppChoice
		for _, app := range apps {
			matched := strings.EqualFold(app.Name, patternText)
			if strings.ContainsAny(patternText, "*?[") {
				matched, _ = path.Match(strings.ToLower(patternText), strings.ToLower(app.Name))
			}
			if matched {
				matches = append(matches, app)
			}
		}
		if len(matches) == 0 {
			return nil, nil, "", fmt.Errorf("no apps match %q", patternText)
		}
		selected = append(selected, matches...)
	}
	var pods, selectors []string
	for _, app := range selected {
		if app.Selector != "" {
			selectors = append(selectors, app.Selector)
		} else {
			pods = append(pods, app.Pods...)
		}
	}
	namespace := r.Namespace
	if namespace == "" && len(apps) > 0 {
		namespace = apps[0].Namespace
	}
	return uniqueStrings(pods), uniqueStrings(selectors), namespace, nil
}

func inventoryFromPods(pods []any) []core.InventoryItem {
	var result []core.InventoryItem
	for _, raw := range pods {
		pod := mapValue(raw)
		name := podName(pod)
		for _, container := range sliceValue(mapValue(pod["spec"])["containers"]) {
			containerName := stringValue(mapValue(container)["name"])
			if name != "" && containerName != "" {
				result = append(result, core.InventoryItem{Pod: name, Container: containerName})
			}
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Key() < result[j].Key() })
	return result
}

func podName(pod map[string]any) string { return stringValue(mapValue(pod["metadata"])["name"]) }
func podPhase(pod map[string]any) string {
	phase := stringValue(mapValue(pod["status"])["phase"])
	if phase == "" {
		return "Unknown"
	}
	return phase
}

func readyCounts(pod map[string]any) (int, int) {
	statuses := sliceValue(mapValue(pod["status"])["containerStatuses"])
	ready := 0
	for _, raw := range statuses {
		if boolValue(mapValue(raw)["ready"]) {
			ready++
		}
	}
	total := len(statuses)
	if total == 0 {
		total = len(sliceValue(mapValue(pod["spec"])["containers"]))
	}
	return ready, total
}

func restartCount(pod map[string]any) int {
	total := 0
	for _, raw := range sliceValue(mapValue(pod["status"])["containerStatuses"]) {
		total += intValue(mapValue(raw)["restartCount"])
	}
	return total
}

func appIdentity(pod map[string]any) (string, string) {
	metadata := mapValue(pod["metadata"])
	labels := mapValue(metadata["labels"])
	for _, key := range appLabelKeys {
		if value := stringValue(labels[key]); value != "" {
			return value, key + "=" + value
		}
	}
	owners := sliceValue(metadata["ownerReferences"])
	var selected map[string]any
	for _, raw := range owners {
		owner := mapValue(raw)
		if selected == nil {
			selected = owner
		}
		if boolValue(owner["controller"]) {
			selected = owner
			break
		}
	}
	if selected != nil {
		name := stringValue(selected["name"])
		if stringValue(selected["kind"]) == "ReplicaSet" {
			parts := strings.Split(name, "-")
			if len(parts) > 1 {
				name = strings.Join(parts[:len(parts)-1], "-")
			}
		}
		if name != "" {
			return name, ""
		}
	}
	return podName(pod), ""
}

func phaseSummary(phases map[string]int) string {
	if len(phases) == 0 {
		return "Unknown"
	}
	if len(phases) == 1 {
		for phase := range phases {
			return phase
		}
	}
	keys := make([]string, 0, len(phases))
	for phase := range phases {
		keys = append(keys, phase)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, phase := range keys {
		parts = append(parts, fmt.Sprintf("%s=%d", phase, phases[phase]))
	}
	return strings.Join(parts, ",")
}

func dedupeInventory(items []core.InventoryItem) []core.InventoryItem {
	seen := map[string]bool{}
	var result []core.InventoryItem
	for _, item := range items {
		if !seen[item.Key()] {
			seen[item.Key()] = true
			result = append(result, item)
		}
	}
	return result
}

func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	var result []string
	for _, value := range values {
		if value != "" && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return result
}
