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
