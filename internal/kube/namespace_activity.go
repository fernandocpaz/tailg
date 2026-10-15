package kube

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/fernandocpaz/tailg/internal/core"
)

var activityResources = []struct{ resource, kind string }{
	{"deployments", "Deployment"}, {"statefulsets", "StatefulSet"}, {"daemonsets", "DaemonSet"},
	{"replicasets", "ReplicaSet"}, {"pods", "Pod"}, {"cronjobs", "CronJob"}, {"jobs", "Job"}, {"events", "Event"},
}

// NamespaceActivity lists the entire namespace, independent of the newest-20
// picker limit. Each resource succeeds or fails independently; at most four
// kubectl requests run concurrently. Never query an implicit or global scope.
func (r Runner) NamespaceActivity(ctx context.Context) core.NamespaceActivitySnapshot {
	result := core.NamespaceActivitySnapshot{Complete: map[string]bool{}}
	if r.Namespace == "" || r.Context == "" {
		result.At = time.Now()
		result.Errors = []string{"Namespace activity requires an explicit context and namespace"}
		return result
	}
	type response struct {
		payload map[string]any
		err     error
	}
	responses := make([]response, len(activityResources))
	var wg sync.WaitGroup
	workers := make(chan struct{}, 4)
	for i, resource := range activityResources {
		wg.Add(1)
		go func(i int, resource string) {
			defer wg.Done()
			select {
			case workers <- struct{}{}:
				defer func() { <-workers }()
			case <-ctx.Done():
				responses[i].err = ctx.Err()
				return
			}
			responses[i].payload, responses[i].err = r.JSON(ctx, "get", resource, "--request-timeout=10s")
		}(i, resource.resource)
	}
	wg.Wait()
	result.At = time.Now()
	for i, resource := range activityResources {
		response := responses[i]
		if response.err != nil {
			result.Errors = append(result.Errors, resource.resource+": "+response.err.Error())
			continue
		}
		items, valid := response.payload["items"].([]any)
		if !valid {
			result.Errors = append(result.Errors, resource.resource+": invalid list response (items missing)")
			continue
		}
		result.Complete[resource.resource] = true
		for _, raw := range items {
			item := mapValue(raw)
			meta := mapValue(item["metadata"])
			// Defensive check even though kubectl itself uses -n.
			if ns := stringValue(meta["namespace"]); ns != "" && ns != r.Namespace {
				continue
			}
			if resource.resource == "events" {
				result.Events = append(result.Events, namespaceEvent(item))
				continue
			}
			if stringValue(meta["name"]) == "" {
				continue
			}
			result.Objects = append(result.Objects, namespaceObject(item, resource.resource, resource.kind))
		}
	}
	sortActivityObjects(result.Objects)
	return result
}

func namespaceObject(item map[string]any, resource, kind string) core.NamespaceObject {
	meta, spec, status := mapValue(item["metadata"]), mapValue(item["spec"]), mapValue(item["status"])
	object := core.NamespaceObject{Resource: resource, Kind: kind, Name: stringValue(meta["name"]), UID: stringValue(meta["uid"]), CreatedAt: parseKubeTimestamp(meta["creationTimestamp"]), Generation: intValue(meta["generation"])}
	template := mapValue(spec["template"])
	if resource == "cronjobs" {
		template = mapValue(mapValue(mapValue(spec["jobTemplate"])["spec"])["template"])
	}
	podSpec := mapValue(template["spec"])
	if resource == "pods" {
		podSpec = spec
	}
	images := map[string]string{}
	for _, field := range []string{"containers", "initContainers"} {
		for _, raw := range sliceValue(podSpec[field]) {
			container := mapValue(raw)
			name := stringValue(container["name"])
			if field == "initContainers" {
				name = "init/" + name
			}
			images[name] = stringValue(container["image"])
		}
	}
	object.Images = core.SortedImages(images)
	if resource == "deployments" || resource == "statefulsets" || resource == "replicasets" {
		object.Replicas = 1
		if spec["replicas"] != nil {
			object.Replicas = intValue(spec["replicas"])
		}
	}
	annotation := stringValue(mapValue(mapValue(template["metadata"])["annotations"])["kubectl.kubernetes.io/restartedAt"])
	if annotation != "" {
		object.Configuration = "Restart requested at " + annotation
	}
	if resource == "cronjobs" {
		zone := stringValue(spec["timeZone"])
		if zone == "" {
			zone = "controller local"
		}
		object.Configuration = fmt.Sprintf("Schedule: %s; time zone: %s; suspended: %t", stringValue(spec["schedule"]), zone, boolValue(spec["suspend"]))
	}
	add := func(key, action, detail string, at time.Time) {
		object.Seeds = append(object.Seeds, core.NamespaceChange{ID: object.UID + "/" + object.Key() + "/" + key, At: at, Object: kind + "/" + object.Name, Action: action, Detail: detail})
	}
	for _, raw := range sliceValue(meta["ownerReferences"]) {
		owner := mapValue(raw)
		if boolValue(owner["controller"]) {
			object.Owner = stringValue(owner["kind"]) + "/" + stringValue(owner["name"])
			break
		}
	}
	createdDetail := "Images observed at discovery: " + object.Images
	if object.Owner != "" {
		createdDetail += "; controller: " + object.Owner
	}
	add("created", "Created", createdDetail, object.CreatedAt)
	if resource == "replicasets" {
		for _, raw := range sliceValue(meta["ownerReferences"]) {
			owner := mapValue(raw)
			if stringValue(owner["kind"]) == "Deployment" && boolValue(owner["controller"]) {
				revision := stringValue(mapValue(meta["annotations"])["deployment.kubernetes.io/revision"])
				detail := "ReplicaSet " + object.Name + " created for Deployment/" + stringValue(owner["name"]) + "; revision " + revision + "; " + object.Images
				add("rollout", "Rollout revision created", detail, object.CreatedAt)
				object.Seeds[len(object.Seeds)-1].Object = "Deployment/" + stringValue(owner["name"])
			}
		}
	}
	if restarted := parseKubeTimestamp(annotation); !restarted.IsZero() {
		add("restart-request/"+annotation, "Restart requested", object.Configuration, restarted)
	}
	if resource == "pods" {
		object.Restarts = map[string]int{}
		object.RestartTimes = map[string]time.Time{}
		for _, field := range []string{"containerStatuses", "initContainerStatuses"} {
			for _, raw := range sliceValue(status[field]) {
				container := mapValue(raw)
				count := intValue(container["restartCount"])
				if count <= 0 {
					continue
				}
				at := parseKubeTimestamp(mapValue(mapValue(container["state"])["running"])["startedAt"])
				if at.IsZero() {
					at = parseKubeTimestamp(mapValue(mapValue(container["lastState"])["terminated"])["finishedAt"])
				}
				name := stringValue(container["name"])
				object.Restarts[field+"/"+name] = count
				object.RestartTimes[field+"/"+name] = at
				add(fmt.Sprintf("%s/%s/restart/%d", field, name, count), "Container restarted", fmt.Sprintf("%s · restart count %d · last termination: %s", name, count, stringValue(mapValue(mapValue(container["lastState"])["terminated"])["reason"])), at)
			}
		}
	}
	if resource == "jobs" {
		for _, raw := range sliceValue(status["conditions"]) {
			condition := mapValue(raw)
			action := stringValue(condition["type"])
			if stringValue(condition["status"]) != "True" || (action != "Complete" && action != "Failed") {
				continue
			}
			add("condition/"+action, "Job "+strings.ToLower(action), stringValue(condition["reason"])+" "+stringValue(condition["message"]), firstActivityTime(condition["lastTransitionTime"], status["completionTime"]))
		}
	}
	return object
}

func firstActivityTime(values ...any) time.Time {
	for _, value := range values {
		if at := parseKubeTimestamp(value); !at.IsZero() {
			return at
		}
	}
	return time.Time{}
}
func namespaceEvent(item map[string]any) core.NamespaceEvent {
	meta, series := mapValue(item["metadata"]), mapValue(item["series"])
	involved := mapValue(item["involvedObject"])
	if len(involved) == 0 {
		involved = mapValue(item["regarding"])
	}
	event := core.NamespaceEvent{ID: stringValue(meta["uid"]), Type: stringValue(item["type"]), Reason: stringValue(item["reason"]), Object: stringValue(involved["kind"]) + "/" + stringValue(involved["name"]), Message: stringValue(item["message"]), Source: stringValue(item["reportingController"])}
	if event.ID == "" {
		event.ID = stringValue(meta["name"])
	}
	if event.Message == "" {
		event.Message = stringValue(item["note"])
	}
	if event.Source == "" {
		event.Source = stringValue(mapValue(item["source"])["component"])
	}
	event.FirstAt = firstActivityTime(item["firstTimestamp"], item["deprecatedFirstTimestamp"], item["eventTime"], meta["creationTimestamp"])
	event.LastAt = firstActivityTime(series["lastObservedTime"], item["lastTimestamp"], item["deprecatedLastTimestamp"], item["eventTime"], meta["creationTimestamp"])
	event.Count = max(1, intValue(item["count"]), intValue(item["deprecatedCount"]), intValue(series["count"]))
	return event
}

// Stable ordering is useful for fixtures and consumers before tracking begins.
func sortActivityObjects(objects []core.NamespaceObject) {
	sort.Slice(objects, func(i, j int) bool { return objects[i].Key() < objects[j].Key() })
}
