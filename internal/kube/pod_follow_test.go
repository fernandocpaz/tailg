package kube

import (
	"context"
	"runtime"
	"testing"
)

func followTestPod(name, rs, uid, created string) map[string]any {
	metadata := map[string]any{"name": name, "uid": "pod-" + name, "creationTimestamp": created}
	if rs != "" {
		metadata["ownerReferences"] = []any{map[string]any{"kind": "ReplicaSet", "name": rs, "uid": uid, "controller": true}}
	}
	return map[string]any{"metadata": metadata, "spec": map[string]any{"containers": []any{map[string]any{"name": "api"}}}, "status": map[string]any{"phase": "Running"}}
}

func TestPodFollowerTracksVerifiedRolloutAndPreservesOneReplicaPerSlot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake kubectl uses shell")
	}
	runner := workloadIdentityRunner(t, `
  *"get replicaset/old -o json"*) printf '%s' '{"metadata":{"name":"old","uid":"old-uid","ownerReferences":[{"kind":"Deployment","name":"api","uid":"dep-uid","controller":true}]}}' ;;
  *"get replicaset/new -o json"*) printf '%s' '{"metadata":{"name":"new","uid":"new-uid","ownerReferences":[{"kind":"Deployment","name":"api","uid":"dep-uid","controller":true}]}}' ;;
  *"get replicaset/other -o json"*) printf '%s' '{"metadata":{"name":"other","uid":"other-uid","ownerReferences":[{"kind":"Deployment","name":"other-app","uid":"other-dep","controller":true}]}}' ;;
  *"get deployment/api -o json"*) printf '%s' '{"metadata":{"name":"api","uid":"dep-uid"}}' ;;
  *"get deployment/other-app -o json"*) printf '%s' '{"metadata":{"name":"other-app","uid":"other-dep"}}' ;;
`)
	f := runner.FollowPods([]string{"old-a", "old-b"})
	oldA := followTestPod("old-a", "old", "old-uid", "2026-10-14T01:00:00Z")
	oldB := followTestPod("old-b", "old", "old-uid", "2026-10-14T01:00:00Z")
	newA := followTestPod("new-a", "new", "new-uid", "2026-10-14T02:00:00Z")
	newB := followTestPod("new-b", "new", "new-uid", "2026-10-14T02:00:01Z")
	unrelated := followTestPod("unrelated", "other", "other-uid", "2026-10-14T03:00:00Z")
	check := func(pods []any, want ...string) {
		t.Helper()
		items, err := f.fromPods(context.Background(), pods)
		if err != nil || len(items) != len(want) {
			t.Fatalf("items=%v err=%v want=%v", items, err, want)
		}
		got := map[string]bool{}
		for _, item := range items {
			got[item.Pod] = true
		}
		for _, name := range want {
			if !got[name] {
				t.Fatalf("items=%v missing %s", items, name)
			}
		}
	}
	check([]any{oldA, oldB, newA, newB, unrelated}, "old-a", "old-b")
	// A terminating replica switches before Kubernetes finally deletes it.
	mapValue(oldA["metadata"])["deletionTimestamp"] = "2026-10-14T04:00:00Z"
	check([]any{oldA, oldB, newA, newB, unrelated}, "new-b", "old-b")
	check([]any{newA, newB, unrelated}, "new-a", "new-b")
	check([]any{unrelated}) // a rollout gap remains scoped to the chosen app
	check([]any{newA, newB, unrelated}, "new-a", "new-b")
}

func TestPodFollowerDoesNotGuessOwnerOrReplaceStandalonePod(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake kubectl uses shell")
	}
	runner := workloadIdentityRunner(t, `
  *"get replicaset/bad -o json"*) printf '%s' '{"metadata":{"name":"bad","uid":"wrong-uid"}}' ;;
`)
	f := runner.FollowPods([]string{"standalone", "unverified"})
	plain := followTestPod("standalone", "", "", "")
	bad := followTestPod("unverified", "bad", "expected-uid", "")
	items, err := f.fromPods(context.Background(), []any{plain, bad})
	if err != nil || len(items) != 2 {
		t.Fatalf("initial items=%v err=%v", items, err)
	}
	items, err = f.fromPods(context.Background(), []any{followTestPod("similar-name", "", "", "")})
	if err != nil || len(items) != 0 {
		t.Fatalf("unverified replacement=%v err=%v", items, err)
	}
}

func TestCompletedCronJobPodKeepsSuccessfulLifecycleMetadata(t *testing.T) {
    succeeded := followTestPod("auditlistener-job-123", "", "", "")
    succeeded["status"] = map[string]any{"phase": "Succeeded", "containerStatuses": []any{map[string]any{
        "name": "api", "state": map[string]any{"terminated": map[string]any{
            "exitCode": 0, "reason": "Completed",
        }},
    }}}
    failed := followTestPod("failed-job-456", "", "", "")
    failed["status"] = map[string]any{"phase": "Failed"}
    running := followTestPod("active-service-123", "", "", "")
    items := inventoryFromPods([]any{succeeded, failed, running})
    if len(items) != 3 {
        t.Fatalf("inventory = %+v", items)
    }
    for _, item := range items {
        if got, want := item.Completed, item.Pod == "auditlistener-job-123"; got != want {
            t.Fatalf("pod %s completed=%t, want %t", item.Pod, got, want)
        }
    }
    follower := Runner{}.FollowPods([]string{"auditlistener-job-123"})
    pinned, err := follower.fromPods(context.Background(), []any{succeeded})
    if err != nil || len(pinned) != 1 || !pinned[0].Completed {
        t.Fatalf("finished Job should remain readable and identified as completed: %+v, %v", pinned, err)
    }
}

func TestInventoryIncludesContainerRuntimeIdentity(t *testing.T) {
	pod := followTestPod("api", "", "", "")
	pod["spec"] = map[string]any{"containers": []any{map[string]any{"name": "api"}, map[string]any{"name": "sidecar"}}}
	pod["status"] = map[string]any{"containerStatuses": []any{
		map[string]any{"name": "api", "containerID": "containerd://api", "restartCount": 2},
		map[string]any{"name": "sidecar", "containerID": "containerd://sidecar", "restartCount": 7},
	}}
	items := inventoryFromPods([]any{pod})
	if len(items) != 2 || items[0].PodUID != "pod-api" || items[0].ContainerID != "containerd://api" || items[0].RestartCount != 2 || items[1].RestartCount != 7 {
		t.Fatalf("runtime metadata=%+v", items)
	}
}
