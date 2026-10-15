package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/fernandocpaz/tailg/internal/agent"
)

// Native process fixture also runs in Windows CI. Other helper subprocesses in
// this package continue through m.Run unless this specific flag is enabled.
func TestMain(m *testing.M) {
	if os.Getenv("TAILG_AI_NETWORK_HELPER") == "1" {
		dir := os.Getenv("TAILG_AI_NETWORK_DIR")
		args := os.Args[1:]
		for i, arg := range args {
			if arg == "config" {
				fmt.Print(`{"current-context":"qa","contexts":[{"name":"qa","context":{"namespace":"apollo"}}]}`)
				os.Exit(0)
			}
			if arg == "logs" {
				os.WriteFile(filepath.Join(dir, "logs.called"), []byte("yes"), 0600)
				fmt.Println(time.Now().UTC().Format(time.RFC3339Nano) + " [12:00:00 ERR] HTTP upstream connection refused")
				os.Exit(0)
			}
			if arg == "get" && i+1 < len(args) {
				resource := args[i+1]
				os.WriteFile(filepath.Join(dir, resource+".called"), []byte(strings.Join(args, " ")), 0600)
				if resource == os.Getenv("TAILG_AI_NETWORK_FAIL") {
					fmt.Fprintln(os.Stderr, "Forbidden password=sentinel-secret")
					os.Exit(1)
				}
				if resource == "pods" && os.Getenv("TAILG_AI_NETWORK_EMPTY") == "1" {
					fmt.Print(`{"items":[]}`)
					os.Exit(0)
				}
				data, err := os.ReadFile(filepath.Join(dir, resource+".json"))
				if err != nil {
					fmt.Fprintln(os.Stderr, err)
					os.Exit(2)
				}
				os.Stdout.Write(data)
				os.Exit(0)
			}
		}
		os.Exit(4)
	}
	os.Exit(m.Run())
}

func aiNetworkKubectl(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	name := "kubectl"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	input, err := os.Open(exe)
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	output, err := os.OpenFile(filepath.Join(dir, name), os.O_CREATE|os.O_WRONLY, 0700)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = io.Copy(output, input); err != nil {
		t.Fatal(err)
	}
	if err = output.Close(); err != nil {
		t.Fatal(err)
	}
	files, err := os.ReadDir(filepath.Join("..", "kube", "testdata", "network"))
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		data, err := os.ReadFile(filepath.Join("..", "kube", "testdata", "network", file.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(dir, file.Name()), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("TAILG_AI_NETWORK_HELPER", "1")
	t.Setenv("TAILG_AI_NETWORK_DIR", dir)
	t.Setenv("GORACE", os.Getenv("GORACE")+" atexit_sleep_ms=0")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return dir
}

func TestStructuredNetworkCLIAndMCPUseSameSchemaWithoutLogs(t *testing.T) {
	dir := aiNetworkKubectl(t)
	for _, format := range []string{"json", "ndjson"} {
		var out, errOut strings.Builder
		cmd := NewCommand(context.Background(), strings.NewReader(""), &out, &errOut)
		cmd.SetArgs([]string{"--network", "--context", "qa", "-n", "apollo", "--output", format, "--max-bytes", "4096"})
		err := cmd.Execute()
		if err != nil && !IsExitError(err) {
			t.Fatal(err)
		}
		var report agent.NetworkReport
		if err = json.Unmarshal([]byte(out.String()), &report); err != nil {
			t.Fatalf("%v: %s", err, out.String())
		}
		if report.SchemaVersion != agent.NetworkSchemaVersion || out.Len() > 4096 || report.Scope.Namespace != "apollo" || report.Summary.Resources != 5 {
			t.Fatalf("CLI schema/scope/bound changed: %+v", report.Summary)
		}
	}
	input := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"tailg_network_diagnose","arguments":{"namespace":"apollo","context":"qa","maxBytes":4096}}}` + "\n"
	var out, errOut strings.Builder
	cmd := NewCommand(context.Background(), strings.NewReader(input), &out, &errOut)
	cmd.SetArgs([]string{"mcp"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var response struct {
		Result struct {
			IsError    bool                `json:"isError"`
			Structured agent.NetworkReport `json:"structuredContent"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(out.String()), &response); err != nil {
		t.Fatal(err)
	}
	if response.Result.IsError || response.Result.Structured.SchemaVersion != agent.NetworkSchemaVersion || response.Result.Structured.Scope.Namespace != "apollo" {
		t.Fatalf("MCP schema: %s", out.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "logs.called")); !os.IsNotExist(err) {
		t.Fatal("standalone network tool read application logs")
	}
	t.Setenv("TAILG_AI_NETWORK_FAIL", "httpproxies.projectcontour.io")
	out.Reset()
	cmd = NewCommand(context.Background(), strings.NewReader(input), &out, &errOut)
	cmd.SetArgs([]string{"mcp"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"status":"partial"`) || strings.Contains(out.String(), "sentinel-secret") {
		t.Fatalf("MCP hid RBAC gap or exposed credentials: %s", out.String())
	}
}

func TestDiagnoseAutomaticallyAddsNetworkAndSupportsOptOut(t *testing.T) {
	aiNetworkKubectl(t)
	options := defaultAgentOptions()
	options.Context = "qa"
	options.Namespace = "apollo"
	options.Network = true
	report, err := collectAgentReport(context.Background(), options, agent.ModeDiagnose)
	if err != nil {
		t.Fatal(err)
	}
	if report.Network == nil || len(report.Issues) == 0 || len(report.Network.Correlations) == 0 {
		t.Fatalf("missing network/log correlation: network=%v issues=%d", report.Network != nil, len(report.Issues))
	}
	for _, c := range report.Network.Correlations {
		if len(c.Pods) != 1 || c.Pods[0] != "api-abc" || c.IssueID != report.Issues[0].ID {
			t.Fatal("wrong shared-pod association")
		}
	}
	options.Network = false
	report, err = collectAgentReport(context.Background(), options, agent.ModeDiagnose)
	if err != nil || report.Network != nil {
		t.Fatal("--network=false did not preserve log-only diagnostics")
	}
	options.Network = true
	t.Setenv("TAILG_AI_NETWORK_EMPTY", "1")
	report, err = collectAgentReport(context.Background(), options, agent.ModeDiagnose)
	if err != nil || report.Network == nil || report.Coverage.Status == "complete" {
		t.Fatalf("empty namespace prevented network diagnostics: %v", err)
	}
}

func TestDiagnoseNetworkFlagDefaultsAndRootOutputValidation(t *testing.T) {
	var out strings.Builder
	cmd := NewCommand(context.Background(), strings.NewReader(""), &out, &out)
	diagnose, _, err := cmd.Find([]string{"diagnose"})
	if err != nil || diagnose.Flags().Lookup("network").DefValue != "true" {
		t.Fatal("diagnose must include network by default")
	}
	cmd.SetArgs([]string{"--output", "json"})
	if err = cmd.Execute(); err == nil || !strings.Contains(err.Error(), "require --network") {
		t.Fatal("root output flag ignored outside network mode")
	}
}
