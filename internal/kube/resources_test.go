package kube

import "testing"

func TestMappedResourcesIncludeVolumesAndEnv(t *testing.T) {
	pod := map[string]any{"spec": map[string]any{"volumes": []any{map[string]any{"name": "settings", "configMap": map[string]any{"name": "app-config"}}, map[string]any{"name": "secrets", "projected": map[string]any{"sources": []any{map[string]any{"secret": map[string]any{"name": "db-secret"}}}}}}, "containers": []any{map[string]any{"name": "api", "envFrom": []any{map[string]any{"secretRef": map[string]any{"name": "api-secret"}}}, "env": []any{map[string]any{"name": "MODE", "valueFrom": map[string]any{"configMapKeyRef": map[string]any{"name": "app-config", "key": "mode"}}}}}}}}
	resources := MappedResourcesFromPod(pod)
	if len(resources) != 3 {
		t.Fatalf("resources=%+v", resources)
	}
	if resources[0].Kind != "ConfigMap" || resources[0].Name != "app-config" || len(resources[0].Sources) != 2 {
		t.Fatalf("configmap=%+v", resources[0])
	}
}
