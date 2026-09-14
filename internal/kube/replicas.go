package kube

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/fernandocpaz/tailg/internal/core"
)

// ExplainReplicas describes the observed controller and scaling state of a
// pod. It intentionally uses only read-only kubectl JSON calls. A pod lookup
// is required; failures of later lookups are retained as warnings so that a
// partial explanation is still useful (in particular under restricted RBAC).
func (r Runner) ExplainReplicas(ctx context.Context, podName string) (core.ReplicaExplanation, error) {
	explanation := core.ReplicaExplanation{Pod: podName}
	pod, err := r.JSON(ctx, "get", "pod/"+podName)
	if err != nil {
		return explanation, err
	}
	if name := stringValue(mapValue(pod["metadata"])["name"]); name != "" {
		explanation.Pod = name
	}

	namespace := stringValue(mapValue(pod["metadata"])["namespace"])
	if namespace == "" {
		namespace = r.Namespace
	}
	workload, ownerWarning := r.resolvePodWorkload(ctx, pod)
	if ownerWarning != "" {
		explanation.Warnings = append(explanation.Warnings, ownerWarning)
	}

	if workload == nil {
		if ownerWarning != "" {
			explanation.Summary = fmt.Sprintf("Pod %s has controller metadata, but its replica owner could not be verified.", explanation.Pod)
		} else {
			buildStandalonePodExplanation(&explanation, pod)
		}
		finishExplanation(&explanation)
		return explanation, nil
	}
	explanation.Workload = workload.kind + "/" + workload.name
	if workload.unsupported {
		explanation.Summary = fmt.Sprintf("Pod %s is controlled by %s; replica details are unsupported.", explanation.Pod, explanation.Workload)
		finishExplanation(&explanation)
		return explanation, nil
	}

	buildWorkloadExplanation(&explanation, pod, workload)
	// An HPA is looked up only after a verified workload has been resolved. Its
	// target reference is matched by kind, name, and API group below.
	hpaRunner := r
	if namespace != "" {
		hpaRunner.Namespace = namespace
	}
	hpa, hpaErr := hpaRunner.findMatchingHPA(ctx, workload)
	if hpaErr != nil {
		explanation.Warnings = append(explanation.Warnings, "HPA lookup failed: "+shortError(hpaErr))
	} else if hpa != nil {
		addHPAExplanation(&explanation, hpa)
	} else {
		// The workload's configured replica value is evidence from its spec. No
		// claim about why that value was chosen is made here.
		if configured := desiredReplica(workload.kind, workload.object); configured != nil {
			explanation.Details = append(explanation.Details, fmt.Sprintf("Workload is configured to %d replicas; no matching HPA was observed.", *configured))
		}
	}
	finishExplanation(&explanation)
	return explanation, nil
}

type resolvedWorkload struct {
	kind        string
	name        string
	object      map[string]any
	apiGroup    string
	unsupported bool
}

func (r Runner) resolvePodWorkload(ctx context.Context, pod map[string]any) (*resolvedWorkload, string) {
	refs := ownerReferences(mapValue(pod["metadata"]))
	if len(refs) == 0 {
		return nil, ""
	}
	ref, ok := controllerReference(refs)
	if !ok {
		return nil, "Pod has owner references but no owner marked controller=true; replica ownership is unverified."
	}
	kind, name := canonicalKind(stringValue(ref["kind"])), stringValue(ref["name"])
	if kind == "" || name == "" {
		return nil, "Pod controller owner reference is incomplete; replica ownership is unverified."
	}
	if !supportedWorkloadKind(kind) && kind != "ReplicaSet" {
		return &resolvedWorkload{kind: kind, name: name, unsupported: true}, ""
	}
	obj, err := r.JSON(ctx, "get", resourceToken(kind, name))
	if err != nil {
		return &resolvedWorkload{kind: kind, name: name}, "Could not read " + kind + "/" + name + ": " + shortError(err)
	}
	if !sameUID(ref, obj) {
		return nil, fmt.Sprintf("Pod owner %s/%s UID did not match the observed object; replacement ownership was not attributed.", kind, name)
	}
	if kind != "ReplicaSet" {
		if !supportedWorkloadKind(kind) {
			return &resolvedWorkload{kind: kind, name: name, object: obj, unsupported: true}, ""
		}
		return &resolvedWorkload{kind: kind, name: name, object: obj, apiGroup: apiGroup(obj)}, ""
	}

	// ReplicaSets are commonly an implementation detail of Deployments. Follow
	// their actual controller owner reference rather than deriving a Deployment
	// name from the ReplicaSet name.
	rsRefs := ownerReferences(mapValue(obj["metadata"]))
	rsOwner, hasOwner := controllerReference(rsRefs)
	if !hasOwner || canonicalKind(stringValue(rsOwner["kind"])) != "Deployment" {
		return &resolvedWorkload{kind: "ReplicaSet", name: name, object: obj, apiGroup: apiGroup(obj)}, ""
	}
	deploymentName := stringValue(rsOwner["name"])
	deployment, deploymentErr := r.JSON(ctx, "get", resourceToken("Deployment", deploymentName))
	if deploymentErr != nil {
		return &resolvedWorkload{kind: "ReplicaSet", name: name, object: obj, apiGroup: apiGroup(obj)}, "Could not read Deployment/" + deploymentName + ": " + shortError(deploymentErr)
	}
	if !sameUID(rsOwner, deployment) {
		return &resolvedWorkload{kind: "ReplicaSet", name: name, object: obj, apiGroup: apiGroup(obj)}, fmt.Sprintf("ReplicaSet owner Deployment/%s UID did not match; deployment ownership was not attributed.", deploymentName)
	}
	return &resolvedWorkload{kind: "Deployment", name: deploymentName, object: deployment, apiGroup: apiGroup(deployment)}, ""
}

func ownerReferences(metadata map[string]any) []map[string]any {
	var refs []map[string]any
	for _, raw := range sliceValue(metadata["ownerReferences"]) {
		if ref := mapValue(raw); len(ref) > 0 {
			refs = append(refs, ref)
		}
	}
	return refs
}

func controllerReference(refs []map[string]any) (map[string]any, bool) {
	for _, ref := range refs {
		if boolValue(ref["controller"]) {
			return ref, true
		}
	}
	return nil, false
}

func sameUID(ref, object map[string]any) bool {
	want := stringValue(ref["uid"])
	have := stringValue(mapValue(object["metadata"])["uid"])
	// UID was introduced with ownerReferences and is present on valid objects.
	// Permit fixtures and older API responses that omit it, but never accept a
	// contradictory UID.
	return want == "" || have == "" || want == have
}

func resourceToken(kind, name string) string {
	return strings.ToLower(kind) + "/" + name
}

func canonicalKind(kind string) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "deployment":
		return "Deployment"
	case "replicaset":
		return "ReplicaSet"
	case "statefulset":
		return "StatefulSet"
	case "daemonset":
		return "DaemonSet"
	case "job":
		return "Job"
	default:
		return strings.TrimSpace(kind)
	}
}

func supportedWorkloadKind(kind string) bool {
	switch canonicalKind(kind) {
	case "Deployment", "StatefulSet", "DaemonSet", "Job":
		return true
	default:
		return false
	}
}

func apiGroup(object map[string]any) string {
	version := stringValue(object["apiVersion"])
	if group, _, ok := strings.Cut(version, "/"); ok {
		return group
	}
	return ""
}

func buildStandalonePodExplanation(explanation *core.ReplicaExplanation, pod map[string]any) {
	explanation.Summary = fmt.Sprintf("Pod %s is standalone; no controller replica target was found.", explanation.Pod)
	if phase := stringValue(mapValue(pod["status"])["phase"]); phase != "" {
		explanation.Details = append(explanation.Details, "Pod phase: "+phase+".")
	}
	if ready, ok := podReady(pod); ok {
		explanation.Details = append(explanation.Details, fmt.Sprintf("Pod ready condition: %s.", ready))
	}
}

func podReady(pod map[string]any) (string, bool) {
	for _, raw := range sliceValue(mapValue(pod["status"])["conditions"]) {
		condition := mapValue(raw)
		if stringValue(condition["type"]) != "Ready" {
			continue
		}
		status := stringValue(condition["status"])
		if status == "" {
			return "unknown", true
		}
		return status, true
	}
	return "", false
}

func buildWorkloadExplanation(explanation *core.ReplicaExplanation, pod map[string]any, workload *resolvedWorkload) {
	spec := mapValue(workload.object["spec"])
	status := mapValue(workload.object["status"])
	var counts []string
	add := func(label string, value any) {
		if value != nil {
			counts = append(counts, label+" "+strconv.Itoa(intValue(value)))
		}
	}
	switch workload.kind {
	case "DaemonSet":
		add("desired eligible nodes", status["desiredNumberScheduled"])
		add("current scheduled", status["currentNumberScheduled"])
		add("ready", status["numberReady"])
		add("available", status["numberAvailable"])
		add("updated", status["updatedNumberScheduled"])
		desired, desiredOK := numberField(status, "desiredNumberScheduled")
		ready, readyOK := numberField(status, "numberReady")
		explanation.Summary = workloadLabel(workload) + " runs one pod on each eligible node"
		if desiredOK {
			explanation.Summary += fmt.Sprintf(": %d desired", desired)
			if readyOK {
				explanation.Summary += fmt.Sprintf(", %d ready", ready)
			}
		}
		explanation.Summary += "."
	case "Job":
		add("parallelism", spec["parallelism"])
		add("active", status["active"])
		add("succeeded", status["succeeded"])
		add("failed", status["failed"])
		parallelism, ok := numberField(spec, "parallelism")
		if !ok {
			parallelism = 1
		}
		explanation.Summary = fmt.Sprintf("%s allows up to %d pod(s) to run concurrently.", workloadLabel(workload), parallelism)
	default:
		add("desired", spec["replicas"])
		add("current", status["currentReplicas"])
		add("ready", status["readyReplicas"])
		add("available", status["availableReplicas"])
		if workload.kind == "Deployment" {
			add("updated", status["updatedReplicas"])
		}
		desired, desiredOK := numberField(spec, "replicas")
		current, currentOK := numberField(status, "currentReplicas")
		ready, readyOK := numberField(status, "readyReplicas")
		if desiredOK {
			explanation.Summary = fmt.Sprintf("%s requests %d replica(s)", workloadLabel(workload), desired)
			if currentOK {
				explanation.Summary += fmt.Sprintf("; %d currently exist", current)
			}
			if readyOK {
				explanation.Summary += fmt.Sprintf(" and %d are ready", ready)
			}
			explanation.Summary += "."
		}
	}
	if len(counts) > 0 {
		explanation.Details = append(explanation.Details, strings.Join(counts, ", ")+".")
	}
	if workload.kind == "Deployment" {
		addDeploymentRollout(explanation, workload.object)
	}
	if workload.kind == "Job" {
		if condition := jobCondition(status); condition != "" {
			explanation.Details = append(explanation.Details, "Job condition: "+condition+".")
		}
	}
	if len(counts) == 0 {
		explanation.Warnings = append(explanation.Warnings, "The API response did not include replica counts for "+explanation.Workload+".")
	}
	if explanation.Summary == "" {
		explanation.Summary = fmt.Sprintf("Pod %s is managed by %s.", explanation.Pod, explanation.Workload)
	}
	_ = pod // Reserved for future pod-specific readiness evidence; no counts are inferred from it.
}

func workloadLabel(workload *resolvedWorkload) string {
	return workload.kind + "/" + workload.name
}

func desiredReplica(kind string, object map[string]any) *int {
	if kind == "DaemonSet" || kind == "Job" {
		return nil
	}
	if value, ok := mapValue(object["spec"])["replicas"]; ok {
		result := intValue(value)
		return &result
	}
	return nil
}

func addDeploymentRollout(explanation *core.ReplicaExplanation, object map[string]any) {
	spec, status, metadata := mapValue(object["spec"]), mapValue(object["status"]), mapValue(object["metadata"])
	if observed, ok := numberField(status, "observedGeneration"); ok {
		if generation, present := numberField(metadata, "generation"); present {
			explanation.Details = append(explanation.Details, fmt.Sprintf("Rollout observed generation %d of %d.", observed, generation))
			if observed < generation {
				explanation.Warnings = append(explanation.Warnings, "Deployment rollout status is behind the current generation.")
			}
		}
	}
	strategy := mapValue(spec["strategy"])
	if strings.EqualFold(stringValue(strategy["type"]), "Recreate") {
		return
	}
	rolling := mapValue(strategy["rollingUpdate"])
	desired, desiredOK := numberField(spec, "replicas")
	current, currentOK := numberField(status, "currentReplicas")
	updated, updatedOK := numberField(status, "updatedReplicas")
	if desiredOK && currentOK && current > desired {
		explanation.Summary = fmt.Sprintf("Deployment rollout has %d pod(s) for a desired %d; rolling-update surge can temporarily create extra replicas.", current, desired)
	} else if desiredOK && updatedOK && updated < desired {
		explanation.Summary = fmt.Sprintf("Deployment rollout is replacing replicas: %d of %d desired pod(s) are updated.", updated, desired)
	}
	if surge, ok := rolling["maxSurge"]; ok {
		value := stringValue(surge)
		if value != "" {
			explanation.Details = append(explanation.Details, "Rolling update maxSurge allows up to "+value+" extra pod(s) above desired during rollout.")
			explanation.Warnings = append(explanation.Warnings, "maxSurge is a rollout allowance; exact operator intent cannot be proven from this data.")
		}
	}
}

func numberField(object map[string]any, key string) (int, bool) {
	value, ok := object[key]
	if !ok || value == nil {
		return 0, false
	}
	return intValue(value), true
}

func jobCondition(status map[string]any) string {
	for _, raw := range sliceValue(status["conditions"]) {
		condition := mapValue(raw)
		typeName := stringValue(condition["type"])
		state := stringValue(condition["status"])
		if typeName != "" && state != "" {
			return typeName + "=" + state
		}
	}
	return ""
}

func (r Runner) findMatchingHPA(ctx context.Context, workload *resolvedWorkload) (map[string]any, error) {
	payload, err := r.JSON(ctx, "get", "hpa")
	if err != nil {
		return nil, err
	}
	items := sliceValue(payload["items"])
	if len(items) == 0 && stringValue(mapValue(payload["metadata"])["name"]) != "" {
		items = []any{payload}
	}
	for _, raw := range items {
		hpa := mapValue(raw)
		metadata := mapValue(hpa["metadata"])
		if namespace := stringValue(metadata["namespace"]); namespace != "" && r.Namespace != "" && namespace != r.Namespace {
			continue
		}
		ref := mapValue(mapValue(hpa["spec"])["scaleTargetRef"])
		if canonicalKind(stringValue(ref["kind"])) != workload.kind || stringValue(ref["name"]) != workload.name {
			continue
		}
		if targetGroup := refAPIGroup(ref); targetGroup != "" && workload.apiGroup != "" && targetGroup != workload.apiGroup {
			continue
		}
		return hpa, nil
	}
	return nil, nil
}

func refAPIGroup(ref map[string]any) string {
	version := stringValue(ref["apiVersion"])
	if group, _, ok := strings.Cut(version, "/"); ok {
		return group
	}
	return ""
}

func addHPAExplanation(explanation *core.ReplicaExplanation, hpa map[string]any) {
	spec, status := mapValue(hpa["spec"]), mapValue(hpa["status"])
	var values []string
	if value, ok := spec["minReplicas"]; ok {
		values = append(values, "min "+strconv.Itoa(intValue(value)))
	}
	if value, ok := spec["maxReplicas"]; ok {
		values = append(values, "max "+strconv.Itoa(intValue(value)))
	}
	if value, ok := status["currentReplicas"]; ok {
		values = append(values, "current "+strconv.Itoa(intValue(value)))
	}
	if value, ok := status["desiredReplicas"]; ok {
		values = append(values, "desired "+strconv.Itoa(intValue(value)))
	}
	if len(values) > 0 {
		explanation.Details = append(explanation.Details, "HPA: "+strings.Join(values, ", ")+".")
	}
	hpaName := valueOr(stringValue(mapValue(hpa["metadata"])["name"]), "matching HPA")
	desired, desiredOK := numberField(status, "desiredReplicas")
	current, currentOK := numberField(status, "currentReplicas")
	minReplicas, minOK := numberField(spec, "minReplicas")
	maxReplicas, maxOK := numberField(spec, "maxReplicas")
	explanation.Summary = "HPA/" + hpaName + " controls " + explanation.Workload
	if desiredOK {
		explanation.Summary += fmt.Sprintf(" and currently requests %d replica(s)", desired)
	}
	if currentOK {
		explanation.Summary += fmt.Sprintf("; %d currently exist", current)
	}
	if minOK && maxOK {
		explanation.Summary += fmt.Sprintf(" within its %d-%d range", minReplicas, maxReplicas)
	}
	explanation.Summary += "."
	for _, raw := range sliceValue(status["conditions"]) {
		condition := mapValue(raw)
		typeName, state := stringValue(condition["type"]), stringValue(condition["status"])
		if typeName == "" || state == "" {
			continue
		}
		reason := stringValue(condition["reason"])
		if reason != "" {
			explanation.Details = append(explanation.Details, "HPA condition: "+typeName+"="+state+" ("+reason+").")
		} else {
			explanation.Details = append(explanation.Details, "HPA condition: "+typeName+"="+state+".")
		}
	}
	if lastScale := stringValue(status["lastScaleTime"]); lastScale != "" {
		explanation.Details = append(explanation.Details, "HPA last scale: "+lastScale+".")
	}
}

func finishExplanation(explanation *core.ReplicaExplanation) {
	if explanation.Summary == "" {
		explanation.Summary = fmt.Sprintf("No replica explanation is available for pod %s.", explanation.Pod)
	}
	// Keep details suitable for the narrow TUI panel and avoid dumping API
	// payloads or condition messages into the interface.
	if len(explanation.Details) > 12 {
		explanation.Details = explanation.Details[:12]
	}
	if len(explanation.Warnings) > 8 {
		explanation.Warnings = explanation.Warnings[:8]
	}
	for i := range explanation.Details {
		explanation.Details[i] = compactLine(explanation.Details[i], 240)
	}
	for i := range explanation.Warnings {
		explanation.Warnings[i] = compactLine(explanation.Warnings[i], 240)
	}
	// Counts are intentionally left in their semantic order; duplicate warnings
	// are removed.
	explanation.Warnings = uniqueStrings(explanation.Warnings)
}

func compactLine(value string, max int) string {
	value = strings.Join(strings.Fields(value), " ")
	runes := []rune(value)
	if len(runes) > max {
		return string(runes[:max-1]) + "…"
	}
	return value
}

func shortError(err error) string {
	if err == nil {
		return "unknown error"
	}
	return compactLine(err.Error(), 180)
}
