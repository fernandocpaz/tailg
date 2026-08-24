package bundle

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/fernandocpaz/tailg/internal/core"
	"github.com/fernandocpaz/tailg/internal/kube"
)

type Options struct {
	Directory  string
	Deployment bool
	Target     string
	Selector   string
	Items      []core.InventoryItem
	Since      string
	Tail       int
}

type Manifest struct {
	GeneratedAt       string   `json:"generatedAt"`
	Target            string   `json:"target"`
	Namespace         string   `json:"namespace"`
	Context           string   `json:"context,omitempty"`
	DeploymentFocused bool     `json:"deploymentFocused"`
	Pods              []string `json:"pods"`
	Files             []string `json:"files"`
	Warnings          []string `json:"warnings,omitempty"`
}

func Generate(ctx context.Context, runner kube.Runner, options Options) (string, error) {
	directory := options.Directory
	if directory == "" || directory == "." {
		directory = fmt.Sprintf("tailg-bundle-%s-%s", safe(options.Target), time.Now().Format("20060102-150405"))
	}
	absolute, err := filepath.Abs(directory)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(absolute, 0o755); err != nil {
		return "", err
	}
	manifest := Manifest{GeneratedAt: time.Now().UTC().Format(time.RFC3339), Target: options.Target, Namespace: valueOr(runner.Namespace, "default"), Context: runner.Context, DeploymentFocused: options.Deployment, Pods: core.UniquePods(options.Items)}
	write := func(name, content string) {
		path := filepath.Join(absolute, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			manifest.Warnings = append(manifest.Warnings, err.Error())
			return
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			manifest.Warnings = append(manifest.Warnings, err.Error())
			return
		}
		manifest.Files = append(manifest.Files, filepath.ToSlash(name))
	}
	collect := func(name string, args ...string) {
		content, runErr := runner.Run(ctx, args...)
		if runErr != nil {
			manifest.Warnings = append(manifest.Warnings, runErr.Error())
			content += "\nERROR: " + runErr.Error() + "\n"
		}
		write(name, content)
	}
	collect("cluster/context.txt", "config", "current-context")
	collect("namespace/events.txt", "get", "events", "--sort-by=.lastTimestamp", "-o", "wide")
	collect("namespace/pods-wide.txt", "get", "pods", "-o", "wide")
	if options.Target != "" && options.Target != "pod/*" {
		collect("target/describe.txt", "describe", options.Target)
		collect("target/resource.yaml", "get", options.Target, "-o", "yaml")
	}
	if options.Deployment && strings.HasPrefix(options.Target, "deployment/") {
		collect("target/rollout-status.txt", "rollout", "status", options.Target, "--timeout=30s")
		collect("target/rollout-history.txt", "rollout", "history", options.Target)
	}
	for _, pod := range manifest.Pods {
		collect(filepath.Join("pods", safe(pod), "describe.txt"), "describe", "pod/"+pod)
		collect(filepath.Join("pods", safe(pod), "pod.yaml"), "get", "pod/"+pod, "-o", "yaml")
	}
	for _, item := range options.Items {
		args := []string{"logs", "pod/" + item.Pod, "-c", item.Container, "--ignore-errors=true", "--tail", fmt.Sprint(options.Tail)}
		if options.Since != "" {
			args = append(args, "--since", options.Since)
		}
		collect(filepath.Join("logs", safe(item.Pod), safe(item.Container)+".log"), args...)
		previous := append(append([]string(nil), args...), "--previous")
		collect(filepath.Join("logs", safe(item.Pod), safe(item.Container)+"-previous.log"), previous...)
	}
	manifestContent, _ := json.MarshalIndent(manifest, "", "  ")
	write("manifest.json", string(manifestContent)+"\n")
	readme := fmt.Sprintf("# tailg troubleshooting bundle\n\n- Generated: `%s`\n- Target: `%s`\n- Namespace: `%s`\n- Pods: `%s`\n- Deployment focused: `%t`\n\nOpen `index.html` for navigation.\n", manifest.GeneratedAt, manifest.Target, manifest.Namespace, strings.Join(manifest.Pods, ", "), manifest.DeploymentFocused)
	write("README.md", readme)
	var links []string
	for _, file := range manifest.Files {
		links = append(links, fmt.Sprintf("<li><a href=\"%s\">%s</a></li>", html.EscapeString(file), html.EscapeString(file)))
	}
	write("index.html", "<!doctype html><meta charset=\"utf-8\"><title>tailg bundle</title><style>body{font:16px system-ui;max-width:1000px;margin:3rem auto;padding:0 1rem}code{background:#eee;padding:.15rem .3rem}</style><h1>tailg troubleshooting bundle</h1><p>Target <code>"+html.EscapeString(manifest.Target)+"</code> in <code>"+html.EscapeString(manifest.Namespace)+"</code></p><ul>"+strings.Join(links, "")+"</ul>")
	return absolute, nil
}

var unsafePath = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func safe(value string) string {
	value = strings.Trim(unsafePath.ReplaceAllString(value, "-"), "-")
	if value == "" {
		return "target"
	}
	return value
}
func valueOr(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
