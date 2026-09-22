package kube

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

type Runner struct {
	Namespace string
	Context   string
	Binary    string
}

func NewRunner(namespace, kubeContext string) Runner {
	return Runner{Namespace: namespace, Context: kubeContext, Binary: "kubectl"}
}

func (r Runner) baseArgs() []string {
	var args []string
	if r.Context != "" {
		args = append(args, "--context", r.Context)
	}
	if r.Namespace != "" {
		args = append(args, "-n", r.Namespace)
	}
	return args
}

func (r Runner) Command(ctx context.Context, args ...string) *exec.Cmd {
	binary := r.Binary
	if binary == "" {
		binary = "kubectl"
	}
	return exec.CommandContext(ctx, binary, append(r.baseArgs(), args...)...)
}

func (r Runner) Run(ctx context.Context, args ...string) (string, error) {
	cmd := r.Command(ctx, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = strings.TrimSpace(stdout.String())
		}
		if message == "" {
			message = err.Error()
		}
		return stdout.String(), fmt.Errorf("kubectl %s: %s", strings.Join(args, " "), message)
	}
	return stdout.String(), nil
}

func (r Runner) JSON(ctx context.Context, args ...string) (map[string]any, error) {
	output, err := r.Run(ctx, append(args, "-o", "json")...)
	if err != nil {
		return nil, err
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		return nil, fmt.Errorf("parse kubectl JSON: %w", err)
	}
	return result, nil
}

func (r Runner) CurrentContext(ctx context.Context) (string, string, error) {
	withoutNamespace := r
	withoutNamespace.Namespace = ""
	payload, err := withoutNamespace.JSON(ctx, "config", "view", "--minify")
	if err != nil {
		return "", "", err
	}
	current := stringValue(payload["current-context"])
	if current == "" {
		current = r.Context
	}
	namespace := "default"
	for _, raw := range sliceValue(payload["contexts"]) {
		item := mapValue(raw)
		if stringValue(item["name"]) == current {
			if selected := stringValue(mapValue(item["context"])["namespace"]); selected != "" {
				namespace = selected
			}
			break
		}
	}
	return current, namespace, nil
}

type ContextInfo struct {
	Name      string
	Namespace string
	Current   bool
}

// Contexts reads kubeconfig without a context override so every configured
// context remains visible, even when tailg was started with --context.
func (r Runner) Contexts(ctx context.Context) ([]ContextInfo, error) {
	configRunner := r
	configRunner.Context = ""
	configRunner.Namespace = ""
	payload, err := configRunner.JSON(ctx, "config", "view")
	if err != nil {
		return nil, err
	}
	current := stringValue(payload["current-context"])
	result := make([]ContextInfo, 0, len(sliceValue(payload["contexts"])))
	for _, raw := range sliceValue(payload["contexts"]) {
		entry := mapValue(raw)
		name := stringValue(entry["name"])
		if name == "" {
			continue
		}
		namespace := stringValue(mapValue(entry["context"])["namespace"])
		if namespace == "" {
			namespace = "default"
		}
		result = append(result, ContextInfo{Name: name, Namespace: namespace, Current: name == current})
	}
	return result, nil
}

// Namespaces lists namespaces in the selected cluster. The active namespace
// override must not be attached to a cluster-scoped request.
func (r Runner) Namespaces(ctx context.Context) ([]string, error) {
	clusterRunner := r
	clusterRunner.Namespace = ""
	payload, err := clusterRunner.JSON(ctx, "get", "namespaces")
	if err != nil {
		return nil, err
	}
	var namespaces []string
	for _, raw := range sliceValue(payload["items"]) {
		name := stringValue(mapValue(mapValue(raw)["metadata"])["name"])
		if name != "" {
			namespaces = append(namespaces, name)
		}
	}
	return namespaces, nil
}

func mapValue(value any) map[string]any {
	if result, ok := value.(map[string]any); ok {
		return result
	}
	return map[string]any{}
}

func sliceValue(value any) []any {
	if result, ok := value.([]any); ok {
		return result
	}
	return nil
}

func stringValue(value any) string {
	if value == nil {
		return ""
	}
	return fmt.Sprint(value)
}

func intValue(value any) int {
	switch number := value.(type) {
	case float64:
		return int(number)
	case int:
		return number
	default:
		var parsed int
		fmt.Sscan(fmt.Sprint(value), &parsed)
		return parsed
	}
}

func boolValue(value any) bool {
	result, _ := value.(bool)
	return result
}
