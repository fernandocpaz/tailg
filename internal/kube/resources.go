package kube

import (
	"context"
	"encoding/base64"
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/fernandocpaz/tailg/internal/core"
)

func (r Runner) MappedResources(ctx context.Context, podName string) ([]core.MappedResource, error) {
	pod, err := r.JSON(ctx, "get", "pod/"+podName)
	if err != nil {
		return nil, err
	}
	return MappedResourcesFromPod(pod), nil
}

func MappedResourcesFromPod(pod map[string]any) []core.MappedResource {
	spec := mapValue(pod["spec"])
	found := map[string]*core.MappedResource{}
	remember := func(kind, name, source string) {
		if name == "" {
			return
		}
		key := kind + "\x00" + name
		resource := found[key]
		if resource == nil {
			resource = &core.MappedResource{Kind: kind, Name: name}
			found[key] = resource
		}
		for _, existing := range resource.Sources {
			if existing == source {
				return
			}
		}
		resource.Sources = append(resource.Sources, source)
	}
	for _, raw := range sliceValue(spec["volumes"]) {
		volume := mapValue(raw)
		volumeName := valueOr(stringValue(volume["name"]), "unnamed")
		remember("ConfigMap", stringValue(mapValue(volume["configMap"])["name"]), "volume/"+volumeName)
		remember("Secret", stringValue(mapValue(volume["secret"])["secretName"]), "volume/"+volumeName)
		for _, sourceRaw := range sliceValue(mapValue(volume["projected"])["sources"]) {
			source := mapValue(sourceRaw)
			remember("ConfigMap", stringValue(mapValue(source["configMap"])["name"]), "projected volume/"+volumeName)
			remember("Secret", stringValue(mapValue(source["secret"])["name"]), "projected volume/"+volumeName)
		}
	}
	groups := []struct {
		name   string
		values []any
	}{
		{"container", sliceValue(spec["containers"])}, {"initContainer", sliceValue(spec["initContainers"])}, {"ephemeralContainer", sliceValue(spec["ephemeralContainers"])},
	}
	for _, group := range groups {
		for _, raw := range group.values {
			container := mapValue(raw)
			containerName := valueOr(stringValue(container["name"]), "unnamed")
			prefix := group.name + "/" + containerName
			for _, envRaw := range sliceValue(container["envFrom"]) {
				env := mapValue(envRaw)
				remember("ConfigMap", stringValue(mapValue(env["configMapRef"])["name"]), prefix+" envFrom")
				remember("Secret", stringValue(mapValue(env["secretRef"])["name"]), prefix+" envFrom")
			}
			for _, envRaw := range sliceValue(container["env"]) {
				env := mapValue(envRaw)
				envName := valueOr(stringValue(env["name"]), "unnamed")
				from := mapValue(env["valueFrom"])
				cm := mapValue(from["configMapKeyRef"])
				secret := mapValue(from["secretKeyRef"])
				remember("ConfigMap", stringValue(cm["name"]), fmt.Sprintf("%s env/%s key/%s", prefix, envName, valueOr(stringValue(cm["key"]), "?")))
				remember("Secret", stringValue(secret["name"]), fmt.Sprintf("%s env/%s key/%s", prefix, envName, valueOr(stringValue(secret["key"]), "?")))
			}
		}
	}
	result := make([]core.MappedResource, 0, len(found))
	for _, resource := range found {
		result = append(result, *resource)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Kind+strings.ToLower(result[i].Name) < result[j].Kind+strings.ToLower(result[j].Name)
	})
	return result
}

func (r Runner) ResourceDetail(ctx context.Context, resource core.MappedResource) (string, error) {
	typeName := "configmap"
	if resource.Kind == "Secret" {
		typeName = "secret"
	}
	payload, err := r.JSON(ctx, "get", typeName, resource.Name)
	if err != nil {
		return "", err
	}
	metadata := mapValue(payload["metadata"])
	lines := []string{resource.Kind + ": " + resource.Name, "Namespace: " + valueOr(stringValue(metadata["namespace"]), valueOr(r.Namespace, "default")), "Mapped by: " + strings.Join(resource.Sources, ", ")}
	if resource.Kind == "Secret" {
		lines = append(lines, "Type: "+valueOr(stringValue(payload["type"]), "Opaque"))
	}
	lines = append(lines, "")
	values := map[string]string{}
	if resource.Kind == "ConfigMap" {
		for key, value := range mapValue(payload["data"]) {
			values[key] = stringValue(value)
		}
		for key, value := range mapValue(payload["binaryData"]) {
			values[key] = fmt.Sprintf("<binary data: %d base64 characters>", len(stringValue(value)))
		}
	} else {
		for key, value := range mapValue(payload["data"]) {
			values[key] = decodeSecret(stringValue(value))
		}
		for key, value := range mapValue(payload["stringData"]) {
			values[key] = stringValue(value)
		}
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return strings.ToLower(keys[i]) < strings.ToLower(keys[j]) })
	if len(keys) == 0 {
		lines = append(lines, "(no data keys)")
	}
	for _, key := range keys {
		lines = append(lines, key+":")
		for _, line := range strings.Split(values[key], "\n") {
			lines = append(lines, "  "+line)
		}
	}
	return strings.Join(lines, "\n"), nil
}

func decodeSecret(value string) string {
	raw, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return "<invalid base64>"
	}
	for _, char := range string(raw) {
		if unicode.IsControl(char) && char != '\r' && char != '\n' && char != '\t' {
			return fmt.Sprintf("<binary data: %d bytes>", len(raw))
		}
	}
	return string(raw)
}

func valueOr(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
