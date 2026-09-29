package kube

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/fernandocpaz/tailg/internal/core"
)

func TestWorkloadIdentitiesResolveStableOwnersAndCachePods(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake kubectl is a shell script")
	}
	// The two ReplicaSets are different rollout revisions, but each verifies
	// the same Deployment through its actual controller reference.
	runner := workloadIdentityRunner(t, `
  *"get pods -o json"*) printf '%s' '{"items":[{"metadata":{"name":"api-old","ownerReferences":[{"kind":"ReplicaSet","name":"api-old-rs","uid":"rs-old","controller":true}]}},{"metadata":{"name":"api-new","ownerReferences":[{"kind":"ReplicaSet","name":"api-new-rs","uid":"rs-new","controller":true}]}},{"metadata":{"name":"worker"}},{"metadata":{"name":"ledger","ownerReferences":[{"kind":"StatefulSet","name":"ledger","uid":"ss-ledger","controller":true}]}}]}' ;;
  *"get replicaset/api-old-rs -o json"*) printf '%s' '{"metadata":{"name":"api-old-rs","uid":"rs-old","ownerReferences":[{"kind":"Deployment","name":"api","uid":"dep-api","controller":true}]}}' ;;
  *"get replicaset/api-new-rs -o json"*) printf '%s' '{"metadata":{"name":"api-new-rs","uid":"rs-new","ownerReferences":[{"kind":"Deployment","name":"api","uid":"dep-api","controller":true}]}}' ;;
  *"get deployment/api -o json"*) printf '%s' '{"metadata":{"name":"api","uid":"dep-api"}}' ;;
  *"get statefulset/ledger -o json"*) printf '%s' '{"metadata":{"name":"ledger","uid":"ss-ledger"}}' ;;
`)
	got, err := runner.WorkloadIdentities(context.Background(), []core.InventoryItem{
		{Pod: "api-old", Container: "api"}, {Pod: "api-new", Container: "api"},
		{Pod: "api-old", Container: "api"}, {Pod: "worker", Container: "api"},
		{Pod: "ledger", Container: "api"},
	})
	if err != nil {
		t.Fatalf("WorkloadIdentities() error = %v", err)
	}
	want := map[string]string{"api-old": "Deployment/api", "api-new": "Deployment/api", "worker": "Pod/worker", "ledger": "StatefulSet/ledger"}
	for pod, expected := range want {
		if got[pod] != expected {
			t.Errorf("identity[%q] = %q, want %q", pod, got[pod], expected)
		}
	}
	if got["api-old"] == got["api-new"] && (got["api-old"] != "Deployment/api") {
		t.Fatal("replica names should collapse only to their verified Deployment")
	}
	if got["api-old"] == got["worker"] {
		t.Fatal("distinct workloads sharing a container name were conflated")
	}
}

func TestWorkloadIdentitiesFallsBackWithContextualErrors(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake kubectl is a shell script")
	}
	runner := workloadIdentityRunner(t, `
  *"get pods -o json"*) printf '%s' '{"items":[{"metadata":{"name":"forbidden","ownerReferences":[{"kind":"StatefulSet","name":"db-forbidden","uid":"db-forbidden","controller":true}]}},{"metadata":{"name":"uid-mismatch","ownerReferences":[{"kind":"StatefulSet","name":"db","uid":"old","controller":true}]}}]}' ;;
  *"get statefulset/db-forbidden -o json"*) echo 'forbidden: statefulsets is forbidden' >&2; exit 1 ;;
  *"get statefulset/db -o json"*) printf '%s' '{"metadata":{"name":"db","uid":"replacement"}}' ;;
`)
	got, err := runner.WorkloadIdentities(context.Background(), []core.InventoryItem{
		{Pod: "forbidden"}, {Pod: "wrong"}, {Pod: "uid-mismatch"},
	})
	if err == nil {
		t.Fatal("WorkloadIdentities() error = nil, want partial-coverage errors")
	}
	for pod, expected := range map[string]string{
		"forbidden": "Pod/forbidden", "wrong": "Pod/wrong", "uid-mismatch": "Pod/uid-mismatch",
	} {
		if got[pod] != expected {
			t.Errorf("identity[%q] = %q, want fallback %q", pod, got[pod], expected)
		}
	}
	for _, phrase := range []string{"forbidden", "pod was absent from the metadata snapshot", "UID did not match"} {
		if !strings.Contains(err.Error(), phrase) {
			t.Errorf("error %q does not include %q", err, phrase)
		}
	}
}

func workloadIdentityRunner(t *testing.T, cases string) Runner {
	t.Helper()
	path := filepath.Join(t.TempDir(), "kubectl")
	script := `#!/bin/sh
case "$*" in
` + cases + `
  *) echo "unexpected kubectl args: $*" >&2; exit 2 ;;
esac
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return Runner{Namespace: "selected-ns", Binary: path}
}
