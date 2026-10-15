package kube

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fernandocpaz/tailg/internal/core"
)

// Called by TestMain so collection exercises real process arguments on Windows
// as well as Linux, with no shell or live cluster dependency.
func networkFixtureHelper() {
	dir := os.Getenv("TAILG_NETWORK_FIXTURE")
	if dir == "" {
		return
	}
	args := strings.Join(os.Args[1:], " ")
	if !strings.HasPrefix(args, "--context tkgs-qa -n apollo get ") || strings.Contains(args, "--all-namespaces") || strings.Contains(args, "secrets") {
		fmt.Fprintln(os.Stderr, "scope mismatch")
		os.Exit(3)
	}
	resource := ""
	for i, arg := range os.Args {
		if arg == "get" && i+1 < len(os.Args) {
			resource = os.Args[i+1]
		}
	}
	os.WriteFile(filepath.Join(dir, resource+".args"), []byte(args), 0600)
	if os.Getenv("TAILG_NETWORK_FAIL") == resource {
		fmt.Fprintln(os.Stderr, "Forbidden: cannot list "+resource)
		os.Exit(1)
	}
	data, err := os.ReadFile(filepath.Join(dir, resource+".json"))
	if err != nil {
		os.Exit(2)
	}
	os.Stdout.Write(data)
	os.Exit(0)
}

func networkFixture(t *testing.T) networkSnapshot {
	t.Helper()
	s := networkSnapshot{items: map[string][]map[string]any{}, complete: map[string]bool{}}
	for _, resource := range networkResources {
		data, err := os.ReadFile(filepath.Join("testdata", "network", resource+".json"))
		if err != nil {
			t.Fatal(err)
		}
		var list map[string]any
		if err := json.Unmarshal(data, &list); err != nil {
			t.Fatal(err)
		}
		for _, raw := range sliceValue(list["items"]) {
			s.items[resource] = append(s.items[resource], mapValue(raw))
		}
		s.complete[resource] = true
	}
	return s
}
func networkRow(t *testing.T, report core.NetworkReport, id string) core.NetworkRow {
	t.Helper()
	for _, row := range report.Rows {
		if row.ID() == id {
			return row
		}
	}
	t.Fatalf("missing %s", id)
	return core.NetworkRow{}
}

func TestNetworkReviewMapsRoutesAndChecksEachPort(t *testing.T) {
	report := analyzeNetwork(networkFixture(t), "tkgs-qa", "apollo", time.Now())
	ing := networkRow(t, report, "Ingress/web")
	if ing.Status != "OK" || !strings.Contains(ing.Detail, "Service/api:http | ready endpoints 1/1") || !strings.Contains(ing.Detail, "Pod/api-abc") || !strings.Contains(ing.Detail, "web-tls") {
		t.Fatalf("Ingress mapping: %+v", ing)
	}
	proxy := networkRow(t, report, "HTTPProxy/root")
	for _, want := range []string{"proxy.test", "HTTPProxy/root -> HTTPProxy/child", "include prefix /api", "prefix /v1", "Service/api:80", "Pod/api-abc"} {
		if !strings.Contains(proxy.Detail, want) {
			t.Fatalf("proxy missing %q: %s", want, proxy.Detail)
		}
	}
	if proxy.Status != "OK" {
		t.Fatalf("proxy: %+v", proxy)
	}
	svc := networkRow(t, report, "Service/api")
	if svc.Status != "REVIEW" || !strings.Contains(svc.Detail, "admin:9000/TCP") || !strings.Contains(svc.Detail, "ready endpoints 0/0") {
		t.Fatalf("second Service port reused the first port's endpoints: %+v", svc)
	}
	if len(report.Events) != 2 || report.Events[0].Reason != "SyncFailed" || report.Events[1].Reason != "FailedCreatePodSandBox" {
		t.Fatalf("event scope/order: %+v", report.Events)
	}
	policy := networkRow(t, report, "NetworkPolicy/api-policy")
	if policy.Status != "INFO" || len(policy.Objects) != 1 || policy.Objects[0] != "Pod/api-abc" || !strings.Contains(policy.Detail, "Policies are additive") {
		t.Fatalf("policy: %+v", policy)
	}
}

func TestNetworkIncompleteDataNeverBecomesMissingBackendEvidence(t *testing.T) {
	s := networkFixture(t)
	s.complete["services"] = false
	s.items["services"] = nil
	ing := networkRow(t, analyzeNetwork(s, "c", "apollo", time.Now()), "Ingress/web")
	if ing.Status != "UNKNOWN" || !strings.Contains(ing.Detail, "Service/api unavailable") {
		t.Fatalf("RBAC: %+v", ing)
	}
	s = networkFixture(t)
	s.complete["endpointslices.discovery.k8s.io"] = false
	s.items["endpointslices.discovery.k8s.io"] = nil
	ing = networkRow(t, analyzeNetwork(s, "c", "apollo", time.Now()), "Ingress/web")
	if ing.Status != "UNKNOWN" || strings.Contains(ing.Detail, "no ready") {
		t.Fatalf("unknown endpoints inferred empty: %+v", ing)
	}
	s = networkFixture(t)
	s.items["endpointslices.discovery.k8s.io"] = nil
	ing = networkRow(t, analyzeNetwork(s, "c", "apollo", time.Now()), "Ingress/web")
	if ing.Status != "REVIEW" || !strings.Contains(ing.Detail, "no ready") {
		t.Fatalf("empty endpoints not flagged: %+v", ing)
	}
}

func TestNetworkBackendReadinessAndServiceVariants(t *testing.T) {
	for _, test := range []struct {
		name      string
		condition map[string]any
		want      string
	}{
		{"unknown readiness defaults usable", map[string]any{}, "OK"},
		{"not ready", map[string]any{"ready": false}, "REVIEW"},
		{"terminating", map[string]any{"ready": true, "terminating": true}, "REVIEW"},
	} {
		t.Run(test.name, func(t *testing.T) {
			s := networkFixture(t)
			for _, slice := range s.items["endpointslices.discovery.k8s.io"] {
				mapValue(sliceValue(slice["endpoints"])[0])["conditions"] = test.condition
			}
			r := networkRow(t, analyzeNetwork(s, "c", "apollo", time.Now()), "Ingress/web")
			if r.Status != test.want {
				t.Fatalf("%+v", r)
			}
		})
	}
	s := networkFixture(t)
	spec := mapValue(s.items["services"][0]["spec"])
	delete(spec, "selector")
	r := networkRow(t, analyzeNetwork(s, "c", "apollo", time.Now()), "Service/api")
	if strings.Contains(r.Detail, "selector matches no pods") {
		t.Fatal("selectorless Service falsely selects every pod")
	}
	spec["type"] = "ExternalName"
	spec["externalName"] = "external.test"
	r = networkRow(t, analyzeNetwork(s, "c", "apollo", time.Now()), "Service/api")
	if r.Status != "INFO" || !strings.Contains(r.Detail, "no local pod") {
		t.Fatalf("ExternalName: %+v", r)
	}
	s = networkFixture(t)
	mapValue(s.items["services"][0]["spec"])["publishNotReadyAddresses"] = true
	r = networkRow(t, analyzeNetwork(s, "c", "apollo", time.Now()), "Ingress/web")
	if r.Status != "UNKNOWN" {
		t.Fatalf("published-not-ready classified healthy: %+v", r)
	}
}

func TestNetworkRoutesFlagMissingServicesAndWrongPorts(t *testing.T) {
	s := networkFixture(t)
	s.items["services"] = nil
	r := networkRow(t, analyzeNetwork(s, "c", "apollo", time.Now()), "Ingress/web")
	if r.Status != "REVIEW" || !strings.Contains(r.Detail, "Service/api unavailable") {
		t.Fatalf("missing Service: %+v", r)
	}
	s = networkFixture(t)
	spec := mapValue(s.items["ingresses.networking.k8s.io"][0]["spec"])
	rule := mapValue(sliceValue(spec["rules"])[0])
	http := mapValue(rule["http"])
	backend := mapValue(sliceValue(http["paths"])[0])
	mapValue(mapValue(backend["backend"])["service"])["port"] = map[string]any{"number": float64(81)}
	r = networkRow(t, analyzeNetwork(s, "c", "apollo", time.Now()), "Ingress/web")
	if r.Status != "REVIEW" || !strings.Contains(r.Detail, "does not expose port 81") {
		t.Fatalf("wrong Service port: %+v", r)
	}
	s = networkFixture(t)
	mapValue(s.items["services"][0]["spec"])["selector"] = map[string]any{"app": "no-match"}
	r = networkRow(t, analyzeNetwork(s, "c", "apollo", time.Now()), "Ingress/web")
	if r.Status != "REVIEW" || !strings.Contains(r.Detail, "selector matches no pods") {
		t.Fatalf("mismatched selector: %+v", r)
	}
}

func TestNetworkProxyIncludesAreBoundedAndNamespaceScoped(t *testing.T) {
	for _, test := range []struct{ name, namespace, want, status string }{
		{"missing", "", "is missing", "REVIEW"}, {"root", "", "include cycle", "REVIEW"}, {"child", "elsewhere", "outside this namespace", "UNKNOWN"},
	} {
		s := networkFixture(t)
		mapValue(s.items["httpproxies.projectcontour.io"][0]["spec"])["includes"] = []any{map[string]any{"name": test.name, "namespace": test.namespace}}
		r := networkRow(t, analyzeNetwork(s, "c", "apollo", time.Now()), "HTTPProxy/root")
		if r.Status != test.status || !strings.Contains(r.Detail, test.want) {
			t.Fatalf("include: %+v", r)
		}
	}
	s := networkFixture(t)
	proxy := s.items["httpproxies.projectcontour.io"][0]
	proxy["status"] = map[string]any{"currentStatus": "invalid", "description": "TLS secret missing"}
	r := networkRow(t, analyzeNetwork(s, "c", "apollo", time.Now()), "HTTPProxy/root")
	if r.Status != "REVIEW" || !strings.Contains(r.Detail, "TLS secret missing") {
		t.Fatalf("invalid status: %+v", r)
	}
	s = networkFixture(t)
	proxy = s.items["httpproxies.projectcontour.io"][0]
	includes := make([]any, 300)
	for i := range includes {
		includes[i] = map[string]any{"name": "child"}
	}
	mapValue(proxy["spec"])["includes"] = includes
	r = networkRow(t, analyzeNetwork(s, "c", "apollo", time.Now()), "HTTPProxy/root")
	if r.Status != "UNKNOWN" || !strings.Contains(r.Detail, "256 visits") {
		t.Fatal("include expansion was not bounded")
	}
}

func TestNetworkCollectionScopesPartialFailuresAndCancellation(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TAILG_NETWORK_FIXTURE", dir)
	t.Setenv("GORACE", os.Getenv("GORACE")+" atexit_sleep_ms=0")
	for _, resource := range networkResources {
		data, err := os.ReadFile(filepath.Join("testdata", "network", resource+".json"))
		if err != nil {
			t.Fatal(err)
		}
		if resource == "services" {
			var payload map[string]any
			if err := json.Unmarshal(data, &payload); err != nil {
				t.Fatal(err)
			}
			payload["items"] = append(sliceValue(payload["items"]), map[string]any{"metadata": map[string]any{"name": "foreign", "namespace": "elsewhere"}})
			data, _ = json.Marshal(payload)
		}
		if err = os.WriteFile(filepath.Join(dir, resource+".json"), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	runner := Runner{Binary: path, Context: "tkgs-qa", Namespace: "apollo"}
	t.Setenv("TAILG_NETWORK_FAIL", "httpproxies.projectcontour.io")
	report := runner.NetworkReview(context.Background())
	if len(report.Errors) != 1 || len(report.Rows) != 3 {
		t.Fatalf("partial collection: %+v", report)
	}
	for _, resource := range networkResources {
		if _, err := os.Stat(filepath.Join(dir, resource+".args")); err != nil {
			t.Fatal(err)
		}
	}
	os.WriteFile(filepath.Join(dir, "services.json"), []byte(`{"kind":"List"}`), 0600)
	if report = runner.NetworkReview(context.Background()); len(report.Errors) != 2 {
		t.Fatalf("malformed list accepted: %+v", report.Errors)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	report = runner.NetworkReview(ctx)
	if len(report.Errors) != len(networkResources) {
		t.Fatalf("cancellation: %+v", report.Errors)
	}
	runner.Namespace = ""
	if report = runner.NetworkReview(context.Background()); len(report.Errors) != 1 || len(report.Rows) != 0 {
		t.Fatal("implicit scope permitted")
	}
}
