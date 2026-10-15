package kube

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/fernandocpaz/tailg/internal/core"
)

var networkResources = []string{"pods", "services", "endpointslices.discovery.k8s.io", "ingresses.networking.k8s.io", "httpproxies.projectcontour.io", "networkpolicies.networking.k8s.io", "events"}

type networkSnapshot struct {
	items    map[string][]map[string]any
	complete map[string]bool
	errors   []string
}

// NetworkReview never broadens the requested namespace and makes at most four
// bounded, read-only kubectl calls concurrently. Optional APIs fail separately.
func (r Runner) NetworkReview(ctx context.Context) core.NetworkReport {
	if r.Context == "" || r.Namespace == "" {
		return core.NetworkReport{At: time.Now(), Errors: []string{"Network review requires an explicit context and namespace"}}
	}
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	type response struct {
		payload map[string]any
		err     error
	}
	responses := make([]response, len(networkResources))
	var wg sync.WaitGroup
	workers := make(chan struct{}, 4)
	for i, resource := range networkResources {
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
		}(i, resource)
	}
	wg.Wait()
	snapshot := networkSnapshot{items: map[string][]map[string]any{}, complete: map[string]bool{}}
	for i, resource := range networkResources {
		snapshot.complete[resource] = false
		response := responses[i]
		if response.err != nil {
			snapshot.errors = append(snapshot.errors, resource+": "+response.err.Error())
			continue
		}
		items, ok := response.payload["items"].([]any)
		if !ok {
			snapshot.errors = append(snapshot.errors, resource+": invalid list response")
			continue
		}
		snapshot.complete[resource] = true
		for _, raw := range items {
			item := mapValue(raw)
			meta := mapValue(item["metadata"])
			if ns := stringValue(meta["namespace"]); ns != "" && ns != r.Namespace {
				continue
			}
			if stringValue(meta["name"]) != "" {
				snapshot.items[resource] = append(snapshot.items[resource], item)
			}
		}
	}
	return analyzeNetwork(snapshot, r.Context, r.Namespace, time.Now())
}

func networkName(item map[string]any) string { return stringValue(mapValue(item["metadata"])["name"]) }
func networkJSON(value any) string {
	data, _ := json.MarshalIndent(value, "", "  ")
	return string(data)
}

func networkSelector(selector, labels map[string]any) bool {
	for key, value := range selector {
		if labels[key] != value {
			return false
		}
	}
	return true
}

func networkReady(pod map[string]any) bool {
	if mapValue(pod["metadata"])["deletionTimestamp"] != nil {
		return false
	}
	for _, raw := range sliceValue(mapValue(pod["status"])["conditions"]) {
		c := mapValue(raw)
		if stringValue(c["type"]) == "Ready" {
			return stringValue(c["status"]) == "True"
		}
	}
	return false
}

func networkCondition(status *string, findings *[]string, level, message string) {
	rank := map[string]int{"OK": 0, "INFO": 0, "UNKNOWN": 1, "REVIEW": 2}
	if rank[level] > rank[*status] {
		*status = level
	}
	*findings = append(*findings, level+": "+message)
}

type networkAnalysis struct {
	snapshot    networkSnapshot
	namespace   string
	services    map[string]map[string]any
	serviceRows map[string]core.NetworkRow
	proxies     map[string]map[string]any
}

func analyzeNetwork(snapshot networkSnapshot, kubeContext, namespace string, now time.Time) core.NetworkReport {
	a := networkAnalysis{snapshot: snapshot, namespace: namespace, services: map[string]map[string]any{}, serviceRows: map[string]core.NetworkRow{}, proxies: map[string]map[string]any{}}
	report := core.NetworkReport{Context: kubeContext, Namespace: namespace, At: now, Errors: snapshot.errors, Sources: snapshot.complete}
	for _, svc := range snapshot.items["services"] {
		a.services[networkName(svc)] = svc
	}
	for _, proxy := range snapshot.items["httpproxies.projectcontour.io"] {
		a.proxies[networkName(proxy)] = proxy
	}
	for _, svc := range snapshot.items["services"] {
		row := a.service(svc)
		a.serviceRows[row.Name] = row
		report.Rows = append(report.Rows, row)
	}
	for _, ingress := range snapshot.items["ingresses.networking.k8s.io"] {
		report.Rows = append(report.Rows, a.ingress(ingress))
	}
	for _, proxy := range snapshot.items["httpproxies.projectcontour.io"] {
		report.Rows = append(report.Rows, a.proxy(proxy))
	}
	for _, policy := range snapshot.items["networkpolicies.networking.k8s.io"] {
		report.Rows = append(report.Rows, a.policy(policy))
	}
	for i := range report.Rows {
		row := &report.Rows[i]
		seen := map[string]bool{}
		var objects []string
		for _, object := range row.Objects {
			if !seen[object] {
				seen[object] = true
				objects = append(objects, object)
			}
		}
		row.Objects = objects
		if row.Status == "REVIEW" || row.Status == "UNKNOWN" {
			for _, line := range strings.Split(row.Detail, "\n") {
				if strings.HasPrefix(line, row.Status+":") {
					row.Summary += " | " + line
					break
				}
			}
		}
	}
	related := map[string]bool{}
	for _, row := range report.Rows {
		related[row.ID()] = true
		for _, object := range row.Objects {
			related[object] = true
		}
	}
	// Pod-level warnings (including CNI/sandbox failures) remain visible even
	// before a Service can select the pod. These events are not packet captures.
	for _, pod := range snapshot.items["pods"] {
		related["Pod/"+networkName(pod)] = true
	}
	for _, slice := range snapshot.items["endpointslices.discovery.k8s.io"] {
		related["EndpointSlice/"+networkName(slice)] = true
	}
	for _, raw := range snapshot.items["events"] {
		e := namespaceEvent(raw)
		if related[e.Object] || strings.HasPrefix(e.Object, "Pod/") || strings.HasPrefix(e.Object, "Ingress/") || strings.HasPrefix(e.Object, "HTTPProxy/") || strings.HasPrefix(e.Object, "Service/") || strings.HasPrefix(e.Object, "NetworkPolicy/") || strings.HasPrefix(e.Object, "EndpointSlice/") {
			report.Events = append(report.Events, e)
		}
	}
	sort.Slice(report.Events, func(i, j int) bool {
		if report.Events[i].LastAt.Equal(report.Events[j].LastAt) {
			return report.Events[i].ID < report.Events[j].ID
		}
		return report.Events[i].LastAt.After(report.Events[j].LastAt)
	})
	sort.Slice(report.Rows, func(i, j int) bool { return report.Rows[i].ID() < report.Rows[j].ID() })
	return report
}

func (a networkAnalysis) selectedPods(selector map[string]any) []map[string]any {
	var result []map[string]any
	for _, pod := range a.snapshot.items["pods"] {
		if networkSelector(selector, mapValue(mapValue(pod["metadata"])["labels"])) {
			result = append(result, pod)
		}
	}
	sort.Slice(result, func(i, j int) bool { return networkName(result[i]) < networkName(result[j]) })
	return result
}

// Match EndpointSlice ports to the Service port, not to the route's service
// port number: an EndpointSlice carries the resolved target port instead.
func (a networkAnalysis) endpoints(service string, port map[string]any) (int, int, []string) {
	all, ready, pods := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, slice := range a.snapshot.items["endpointslices.discovery.k8s.io"] {
		if stringValue(mapValue(mapValue(slice["metadata"])["labels"])["kubernetes.io/service-name"]) != service {
			continue
		}
		matches := false
		for _, raw := range sliceValue(slice["ports"]) {
			p := mapValue(raw)
			if stringValue(p["name"]) == stringValue(port["name"]) && valueOr(stringValue(p["protocol"]), "TCP") == valueOr(stringValue(port["protocol"]), "TCP") && intValue(p["port"]) > 0 {
				matches = true
			}
		}
		if !matches {
			continue
		}
		for _, raw := range sliceValue(slice["endpoints"]) {
			e := mapValue(raw)
			ref := mapValue(e["targetRef"])
			key := stringValue(ref["uid"])
			if key == "" {
				key = stringValue(ref["kind"]) + "/" + stringValue(ref["namespace"]) + "/" + stringValue(ref["name"])
			}
			if stringValue(ref["name"]) == "" {
				key = networkJSON(e["addresses"])
			}
			all[key] = true
			c := mapValue(e["conditions"])
			if (c["ready"] == nil || boolValue(c["ready"])) && !boolValue(c["terminating"]) {
				ready[key] = true
			}
			if stringValue(ref["kind"]) == "Pod" && (ref["namespace"] == nil || stringValue(ref["namespace"]) == a.namespace) {
				pods["Pod/"+stringValue(ref["name"])] = true
			}
		}
	}
	var names []string
	for pod := range pods {
		names = append(names, pod)
	}
	sort.Strings(names)
	return len(ready), len(all), names
}

func (a networkAnalysis) service(svc map[string]any) core.NetworkRow {
	name, spec := networkName(svc), mapValue(svc["spec"])
	row := core.NetworkRow{Kind: "Service", Name: name, Status: "OK"}
	var lines []string
	typeName := valueOr(stringValue(spec["type"]), "ClusterIP")
	lines = append(lines, "Type: "+typeName+" | Cluster IP: "+stringValue(spec["clusterIP"]))
	if typeName == "ExternalName" {
		row.Status = "INFO"
		row.Summary = "External DNS: " + stringValue(spec["externalName"])
		lines = append(lines, "ExternalName uses DNS; no local pod or EndpointSlice health is inferred.")
	} else {
		selector := mapValue(spec["selector"])
		if len(selector) == 0 {
			lines = append(lines, "Selectorless Service: manually managed or external endpoints; it does not select every pod.")
		} else if !a.snapshot.complete["pods"] {
			networkCondition(&row.Status, &lines, "UNKNOWN", "pod selection unavailable")
		} else {
			pods := a.selectedPods(selector)
			if len(pods) == 0 {
				networkCondition(&row.Status, &lines, "REVIEW", "selector matches no pods")
			}
			for _, pod := range pods {
				row.Objects = append(row.Objects, "Pod/"+networkName(pod))
				lines = append(lines, fmt.Sprintf("Pod/%s | IP %s | Ready %t", networkName(pod), stringValue(mapValue(pod["status"])["podIP"]), networkReady(pod)))
			}
		}
		var ports []string
		for _, raw := range sliceValue(spec["ports"]) {
			p := mapValue(raw)
			label := fmt.Sprintf("%s:%d/%s -> target %s", stringValue(p["name"]), intValue(p["port"]), valueOr(stringValue(p["protocol"]), "TCP"), valueOr(stringValue(p["targetPort"]), stringValue(p["port"])))
			ports = append(ports, label)
			if !a.snapshot.complete["endpointslices.discovery.k8s.io"] {
				networkCondition(&row.Status, &lines, "UNKNOWN", label+" | EndpointSlices unavailable")
				continue
			}
			ready, total, pods := a.endpoints(name, p)
			ports[len(ports)-1] += fmt.Sprintf(" ready %d/%d", ready, total)
			row.Objects = append(row.Objects, pods...)
			lines = append(lines, fmt.Sprintf("Port %s | ready endpoints %d/%d (deduplicated across IP families)", label, ready, total))
			if ready == 0 {
				networkCondition(&row.Status, &lines, "REVIEW", "no ready, non-terminating endpoints for "+label)
			}
		}
		row.Summary = typeName + " | " + strings.Join(ports, ", ")
		if boolValue(spec["publishNotReadyAddresses"]) {
			networkCondition(&row.Status, &lines, "UNKNOWN", "publishNotReadyAddresses is enabled; published endpoint readiness does not establish pod readiness")
		}
	}
	for _, slice := range a.snapshot.items["endpointslices.discovery.k8s.io"] {
		if stringValue(mapValue(mapValue(slice["metadata"])["labels"])["kubernetes.io/service-name"]) == name {
			row.Objects = append(row.Objects, "EndpointSlice/"+networkName(slice))
			lines = append(lines, "EndpointSlice/"+networkName(slice)+":\n"+networkJSON(map[string]any{"ports": slice["ports"], "endpoints": slice["endpoints"]}))
		}
	}
	if typeName == "LoadBalancer" && len(sliceValue(mapValue(mapValue(svc["status"])["loadBalancer"])["ingress"])) == 0 {
		networkCondition(&row.Status, &lines, "UNKNOWN", "load balancer has no published address")
	}
	lines = append(lines, "Service spec:\n"+networkJSON(spec), "Load balancer status:\n"+networkJSON(mapValue(svc["status"])))
	row.Detail = strings.Join(lines, "\n")
	return row
}

func (a networkAnalysis) backend(name, port string, row *core.NetworkRow, lines *[]string) {
	row.Objects = append(row.Objects, "Service/"+name)
	svc, exists := a.services[name]
	if !exists {
		level := "REVIEW"
		if !a.snapshot.complete["services"] {
			level = "UNKNOWN"
		}
		networkCondition(&row.Status, lines, level, "Service/"+name+" unavailable")
		return
	}
	selector := mapValue(mapValue(svc["spec"])["selector"])
	if len(selector) > 0 {
		if !a.snapshot.complete["pods"] {
			networkCondition(&row.Status, lines, "UNKNOWN", "backend pod selection unavailable")
		} else if len(a.selectedPods(selector)) == 0 {
			networkCondition(&row.Status, lines, "REVIEW", "backend Service selector matches no pods")
		}
	}
	for _, raw := range sliceValue(mapValue(svc["spec"])["ports"]) {
		p := mapValue(raw)
		if port != stringValue(p["name"]) && port != stringValue(p["port"]) {
			continue
		}
		if valueOr(stringValue(p["protocol"]), "TCP") != "TCP" {
			networkCondition(&row.Status, lines, "REVIEW", "HTTP/TCP backend references a non-TCP Service port")
		}
		serviceRow := a.serviceRows[name]
		row.Objects = append(row.Objects, serviceRow.Objects...)
		var pods []string
		for _, object := range serviceRow.Objects {
			if strings.HasPrefix(object, "Pod/") {
				pods = append(pods, object)
			}
		}
		*lines = append(*lines, "Pods: "+strings.Join(pods, ", "))
		if stringValue(mapValue(svc["spec"])["type"]) == "ExternalName" {
			networkCondition(&row.Status, lines, "UNKNOWN", "external backend DNS/connectivity not tested")
			return
		}
		if !a.snapshot.complete["endpointslices.discovery.k8s.io"] {
			networkCondition(&row.Status, lines, "UNKNOWN", "backend EndpointSlices unavailable")
			return
		}
		ready, total, _ := a.endpoints(name, p)
		*lines = append(*lines, fmt.Sprintf("Service/%s:%s | ready endpoints %d/%d", name, port, ready, total))
		if ready == 0 {
			networkCondition(&row.Status, lines, "REVIEW", "backend has no ready, non-terminating endpoints")
		}
		if boolValue(mapValue(svc["spec"])["publishNotReadyAddresses"]) {
			networkCondition(&row.Status, lines, "UNKNOWN", "backend publishes not-ready addresses")
		}
		return
	}
	networkCondition(&row.Status, lines, "REVIEW", "Service/"+name+" does not expose port "+port)
}

func networkAddress(item map[string]any) string {
	var addresses []string
	for _, raw := range sliceValue(mapValue(mapValue(item["status"])["loadBalancer"])["ingress"]) {
		i := mapValue(raw)
		addresses = append(addresses, valueOr(stringValue(i["hostname"]), stringValue(i["ip"])))
	}
	return strings.Join(addresses, ", ")
}

func (a networkAnalysis) ingress(item map[string]any) core.NetworkRow {
	row := core.NetworkRow{Kind: "Ingress", Name: networkName(item), Status: "OK"}
	spec := mapValue(item["spec"])
	class := valueOr(stringValue(spec["ingressClassName"]), stringValue(mapValue(mapValue(item["metadata"])["annotations"])["kubernetes.io/ingress.class"]))
	lines := []string{"Class: " + valueOr(class, "controller default (not verified)"), "Published address: " + valueOr(networkAddress(item), "none reported")}
	var routes []string
	add := func(host, path string, backend map[string]any) {
		svc := mapValue(backend["service"])
		if len(svc) == 0 {
			networkCondition(&row.Status, &lines, "UNKNOWN", "non-Service Ingress backend not evaluated")
			return
		}
		p := mapValue(svc["port"])
		port := valueOr(stringValue(p["name"]), stringValue(p["number"]))
		route := fmt.Sprintf("%s%s -> Service/%s:%s", host, path, stringValue(svc["name"]), port)
		routes = append(routes, route)
		lines = append(lines, route)
		a.backend(stringValue(svc["name"]), port, &row, &lines)
	}
	if backend := mapValue(spec["defaultBackend"]); len(backend) > 0 {
		add("*", " (default)", backend)
	}
	for _, raw := range sliceValue(spec["rules"]) {
		rule := mapValue(raw)
		for _, raw := range sliceValue(mapValue(rule["http"])["paths"]) {
			path := mapValue(raw)
			add(valueOr(stringValue(rule["host"]), "*"), valueOr(stringValue(path["path"]), "/"), mapValue(path["backend"]))
		}
	}
	if networkAddress(item) == "" {
		networkCondition(&row.Status, &lines, "UNKNOWN", "no published ingress address; controller may not publish one")
	}
	if len(routes) == 0 {
		networkCondition(&row.Status, &lines, "UNKNOWN", "no Service routes found")
	}
	lines = append(lines, "TLS references (certificates and handshakes not checked):\n"+networkJSON(spec["tls"]), "Ingress spec:\n"+networkJSON(spec))
	row.Summary, row.Detail = strings.Join(routes, "; "), strings.Join(lines, "\n")
	return row
}

func (a networkAnalysis) proxy(item map[string]any) core.NetworkRow {
	row := core.NetworkRow{Kind: "HTTPProxy", Name: networkName(item), Status: "OK"}
	var lines, routes []string
	budget := 256
	var walk func(map[string]any, string, []string, []string, map[string]bool)
	walk = func(proxy map[string]any, host string, chain, matches []string, visiting map[string]bool) {
		if budget == 0 {
			return
		}
		budget--
		name := networkName(proxy)
		if visiting[name] {
			networkCondition(&row.Status, &lines, "REVIEW", "HTTPProxy include cycle at "+name)
			return
		}
		if len(chain) >= 32 {
			networkCondition(&row.Status, &lines, "UNKNOWN", "HTTPProxy include depth exceeds 32")
			return
		}
		visiting[name] = true
		defer delete(visiting, name)
		chain = append(append([]string(nil), chain...), "HTTPProxy/"+name)
		row.Objects = append(row.Objects, "HTTPProxy/"+name)
		spec, status := mapValue(proxy["spec"]), mapValue(proxy["status"])
		vh := mapValue(spec["virtualhost"])
		if stringValue(vh["fqdn"]) != "" {
			host = stringValue(vh["fqdn"])
		}
		current := stringValue(status["currentStatus"])
		if current != "" && !strings.EqualFold(current, "valid") {
			networkCondition(&row.Status, &lines, "REVIEW", name+" controller status: "+current)
		}
		valid := strings.EqualFold(current, "valid")
		for _, raw := range sliceValue(status["conditions"]) {
			c := mapValue(raw)
			if stringValue(c["type"]) == "Valid" {
				if stringValue(c["status"]) == "True" {
					valid = true
				} else {
					valid = false
					networkCondition(&row.Status, &lines, "REVIEW", name+" Valid="+stringValue(c["status"])+" "+stringValue(c["message"]))
				}
				if c["observedGeneration"] != nil && intValue(c["observedGeneration"]) < intValue(mapValue(proxy["metadata"])["generation"]) {
					networkCondition(&row.Status, &lines, "UNKNOWN", name+" controller status is for an older generation")
				}
			}
		}
		if !valid {
			level := "UNKNOWN"
			if current != "" {
				level = "REVIEW"
			}
			networkCondition(&row.Status, &lines, level, name+" controller status: "+valueOr(current, "not reported"))
		}
		lines = append(lines, "Include chain: "+strings.Join(chain, " -> "), "Controller status:\n"+networkJSON(status))
		add := func(route map[string]any, tcp bool) {
			conditions := networkMatch(route["conditions"])
			if tcp {
				conditions = "TCP"
			}
			for _, raw := range sliceValue(route["services"]) {
				svc := mapValue(raw)
				service, port := stringValue(svc["name"]), stringValue(svc["port"])
				path := fmt.Sprintf("%s | %s -> Service/%s:%s", host, strings.Join(append(append([]string(nil), matches...), conditions), " | "), service, port)
				routes = append(routes, path)
				lines = append(lines, path)
				a.backend(service, port, &row, &lines)
			}
		}
		if tcp := mapValue(spec["tcpproxy"]); len(tcp) > 0 {
			add(tcp, true)
		} else {
			for _, raw := range sliceValue(spec["routes"]) {
				add(mapValue(raw), false)
			}
		}
		includes := append([]any(nil), sliceValue(spec["includes"])...)
		tcp := mapValue(spec["tcpproxy"])
		if include := mapValue(tcp["include"]); len(include) > 0 {
			includes = append(includes, include)
		}
		if include := mapValue(tcp["includes"]); len(include) > 0 {
			includes = append(includes, include)
		}
		for _, raw := range includes {
			include := mapValue(raw)
			ns := valueOr(stringValue(include["namespace"]), a.namespace)
			name := stringValue(include["name"])
			lines = append(lines, "Include conditions: "+networkJSON(include["conditions"]))
			if ns != a.namespace {
				networkCondition(&row.Status, &lines, "UNKNOWN", "include "+ns+"/"+name+" is outside this namespace; not inspected")
				continue
			}
			child, ok := a.proxies[name]
			if !ok {
				networkCondition(&row.Status, &lines, "REVIEW", "included HTTPProxy/"+name+" is missing")
				continue
			}
			if budget == 0 {
				networkCondition(&row.Status, &lines, "UNKNOWN", "include expansion limited to 256 visits")
				break
			}
			walk(child, host, chain, append(append([]string(nil), matches...), "include "+networkMatch(include["conditions"])), visiting)
		}
	}
	rootHost := stringValue(mapValue(mapValue(item["spec"])["virtualhost"])["fqdn"])
	if rootHost == "" {
		rootHost = "(delegated; parent host outside this view may apply)"
	}
	class := valueOr(stringValue(mapValue(mapValue(item["metadata"])["annotations"])["kubernetes.io/ingress.class"]), stringValue(mapValue(item["spec"])["ingressClassName"]))
	lines = append(lines, "Class: "+valueOr(class, "controller default (not verified)"), "Published address: "+valueOr(networkAddress(item), "none reported"))
	walk(item, rootHost, nil, nil, map[string]bool{})
	lines = append(lines, "HTTPProxy spec (TLS references only; certificates not read):\n"+networkJSON(mapValue(item["spec"])))
	row.Summary = strings.Join(routes, "; ")
	if row.Summary == "" {
		row.Summary = "Includes / controller status"
	}
	row.Detail = strings.Join(lines, "\n")
	return row
}

func networkMatch(value any) string {
	if len(sliceValue(value)) == 0 {
		return "prefix /"
	}
	var parts []string
	for _, raw := range sliceValue(value) {
		condition := mapValue(raw)
		found := false
		for _, key := range []string{"prefix", "exact", "regex"} {
			if condition[key] != nil {
				parts = append(parts, key+" "+stringValue(condition[key]))
				found = true
			}
		}
		if !found {
			data, _ := json.Marshal(condition)
			parts = append(parts, string(data))
		}
	}
	return strings.Join(parts, ", ")
}

func networkPolicyMatches(selector, labels map[string]any) bool {
	if !networkSelector(mapValue(selector["matchLabels"]), labels) {
		return false
	}
	for _, raw := range sliceValue(selector["matchExpressions"]) {
		e := mapValue(raw)
		value, exists := labels[stringValue(e["key"])]
		found := false
		for _, v := range sliceValue(e["values"]) {
			if value == v {
				found = true
			}
		}
		switch stringValue(e["operator"]) {
		case "In":
			if !exists || !found {
				return false
			}
		case "NotIn":
			if found {
				return false
			}
		case "Exists":
			if !exists {
				return false
			}
		case "DoesNotExist":
			if exists {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func (a networkAnalysis) policy(item map[string]any) core.NetworkRow {
	spec := mapValue(item["spec"])
	row := core.NetworkRow{Kind: "NetworkPolicy", Name: networkName(item), Status: "INFO"}
	var lines []string
	if !a.snapshot.complete["pods"] {
		row.Status = "UNKNOWN"
		lines = append(lines, "Pod selection unavailable")
	} else {
		for _, pod := range a.snapshot.items["pods"] {
			if networkPolicyMatches(mapValue(spec["podSelector"]), mapValue(mapValue(pod["metadata"])["labels"])) {
				row.Objects = append(row.Objects, "Pod/"+networkName(pod))
			}
		}
		sort.Strings(row.Objects)
	}
	row.Summary = fmt.Sprintf("Selects %d pods | policy rules require review", len(row.Objects))
	lines = append(lines, "Selected pods: "+strings.Join(row.Objects, ", "), "Policies are additive. Effective access depends on all ingress/egress policies, peer namespaces and CNI enforcement; no allow/deny conclusion is inferred.", "NetworkPolicy spec:\n"+networkJSON(spec))
	row.Detail = strings.Join(lines, "\n")
	return row
}
