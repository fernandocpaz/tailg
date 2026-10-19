package app

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func versionComparisonKubectl(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses a POSIX shell script")
	}
	dir := t.TempDir()
	argsPath := filepath.Join(dir, "args.txt")
	t.Setenv("TAILG_VERSION_ARGS", argsPath)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	script := `#!/bin/sh
printf '%s\n' "$*" >> "$TAILG_VERSION_ARGS"
case "$*" in
  "config view --minify -o json")
    printf '%s\n' '{"current-context":"tkgs-qa","contexts":[{"name":"tkgs-qa","context":{"namespace":"default"}}]}'
    ;;
  "--context tkgs-qa -n apollo get pods -o json")
    printf '%s\n' '{"items":[{"metadata":{"name":"api-111","labels":{"app.kubernetes.io/name":"api"}},"spec":{"containers":[{"name":"main","image":"example/api:10"}]},"status":{"containerStatuses":[{"name":"main","imageID":"example/api@sha256:old"}]}}]}'
    ;;
  "--context tkgs-qa -n emr2train get pods -o json")
    printf '%s\n' '{"items":[{"metadata":{"name":"api-222","labels":{"app.kubernetes.io/name":"api"}},"spec":{"containers":[{"name":"main","image":"example/api:11"}]},"status":{"containerStatuses":[{"name":"main","imageID":"example/api@sha256:new"}]}}]}'
    ;;
  "--context tkgs-qa -n unavailable get pods -o json")
    echo "Forbidden: cannot list pods" >&2
    exit 1
    ;;
  *)
    echo "unexpected kubectl args: $*" >&2
    exit 1
    ;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "kubectl"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	return argsPath
}

func executeVersionArgs(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var output bytes.Buffer
	command := NewCommand(context.Background(), strings.NewReader(""), &output, &output)
	command.SetArgs(args)
	err := command.Execute()
	return output.String(), err
}

func TestRepeatedNamespacesUseCurrentContextForVersionComparison(t *testing.T) {
	argsPath := versionComparisonKubectl(t)
	output, err := executeVersionArgs(t, "--versions", "--namespace", "apollo", "--namespace", "emr2train")
	if err != nil {
		t.Fatalf("version compare failed: %v\n%s", err, output)
	}
	for _, want := range []string{"IMAGE VERSION COMPARISON", "context=tkgs-qa", "namespaces=apollo,emr2train",
		"comparable=1", "differing=1", "api", "IMAGE ID", "sha256:old", "sha256:new", "DIFF"} {
		if !strings.Contains(output, want) {
			t.Fatalf("missing %q:\n%s", want, output)
		}
	}
	args, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"config view --minify -o json",
		"--context tkgs-qa -n apollo get pods -o json",
		"--context tkgs-qa -n emr2train get pods -o json",
	} {
		if !strings.Contains(string(args), want) {
			t.Fatalf("missing scoped kubectl request %q:\n%s", want, args)
		}
	}
}

func TestRepeatedNamespacesRespectExplicitContextAndShortFlags(t *testing.T) {
	argsPath := versionComparisonKubectl(t)
	output, err := executeVersionArgs(t, "--versions", "--context", "tkgs-qa", "-n", "apollo", "-n", "emr2train")
	if err != nil || !strings.Contains(output, "differing=1") {
		t.Fatalf("unexpected result: err=%v output=%s", err, output)
	}
	args, _ := os.ReadFile(argsPath)
	if strings.Contains(string(args), "config view") {
		t.Fatalf("explicit context should not resolve kubeconfig again: %s", args)
	}
}

func TestSingleNamespaceKeepsExistingVersionListing(t *testing.T) {
	versionComparisonKubectl(t)
	output, err := executeVersionArgs(t, "--versions", "--context", "tkgs-qa", "--namespace", "apollo")
	if err != nil || !strings.Contains(output, "IMAGE VERSIONS | namespace=apollo") || !strings.Contains(output, "api-111") {
		t.Fatalf("single namespace behavior regressed: err=%v output=%q", err, output)
	}
}

func TestRepeatedNamespaceRejectedWithoutVersions(t *testing.T) {
	output, err := executeVersionArgs(t, "--namespace", "apollo", "--namespace", "emr2train")
	if err == nil || !strings.Contains(err.Error(), "--namespace may be repeated only with --versions") {
		t.Fatalf("expected helpful error: err=%v output=%s", err, output)
	}
}

func TestRejectTwoContextsAndTwoNamespaces(t *testing.T) {
	output, err := executeVersionArgs(t, "--versions", "--context", "tkgs-qa", "--context", "tkgs-dev", "--namespace", "apollo", "--namespace", "emr2train")
	if err == nil || !strings.Contains(err.Error(), "do not repeat both flags") {
		t.Fatalf("expected comparison scope validation: err=%v output=%s", err, output)
	}
}

func TestNamespaceComparisonFailsClosedWhenOneNamespaceCannotBeRead(t *testing.T) {
	versionComparisonKubectl(t)
	output, err := executeVersionArgs(t, "--versions", "--context", "tkgs-qa", "--namespace", "apollo", "--namespace", "unavailable")
	if err == nil || !strings.Contains(output, "context tkgs-qa / namespace unavailable") ||
		!strings.Contains(output, "Forbidden") || strings.Contains(output, "IMAGE VERSION COMPARISON") {
		t.Fatalf("partial comparison must not be printed: err=%v output=%s", err, output)
	}
}

func TestRepeatedContextsPreserveExistingVersionComparison(t *testing.T) {
	output, err := executeVersionArgs(t, "--context", "tkgs-qa", "--context", "tkgs-dev")
	if err == nil || !strings.Contains(err.Error(), "--context may be repeated only with --versions") {
		t.Fatalf("existing context validation regressed: err=%v output=%s", err, output)
	}
}
