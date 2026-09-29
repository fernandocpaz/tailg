package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestMCPSupportsModernDiscoveryAndTools(t *testing.T) {
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"server/discover","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"tailg_list_issues","arguments":{"namespace":"default"}}}`,
	}, "\n") + "\n"
	var output strings.Builder
	handler := func(_ context.Context, name string, arguments ToolArguments) (Report, error) {
		if name != "tailg_list_issues" || arguments.Namespace != "default" {
			t.Fatalf("unexpected call: %s %+v", name, arguments)
		}
		return Report{SchemaVersion: SchemaVersion, Kind: "DiagnosticReport", Issues: []Issue{}, Pods: []Pod{}, KubernetesEvents: []KubernetesEvent{}, CollectionErrors: []CollectionError{}}, nil
	}
	if err := ServeMCP(context.Background(), strings.NewReader(input), &output, handler); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("got %d responses: %s", len(lines), output.String())
	}
	var discover map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &discover); err != nil {
		t.Fatal(err)
	}
	result := discover["result"].(map[string]any)
	if result["resultType"] != "complete" {
		t.Fatalf("unexpected discovery: %v", result)
	}
	var list map[string]any
	if err := json.Unmarshal([]byte(lines[1]), &list); err != nil {
		t.Fatal(err)
	}
	tools := list["result"].(map[string]any)["tools"].([]any)
	if len(tools) != 9 {
		t.Fatalf("got %d tools, want three diagnostic, three monitor, two evidence, and one work-item tool", len(tools))
	}
	firstSchema := tools[0].(map[string]any)["inputSchema"].(map[string]any)
	if _, present := firstSchema["required"]; present {
		t.Fatalf("optional schema must not contain required: %v", firstSchema)
	}
}

func TestMCPInstructionsSayUnknownCoverageIsNotHealthy(t *testing.T) {
	input := `{"jsonrpc":"2.0","id":1,"method":"server/discover","params":{}}` + "\n"
	var output strings.Builder
	if err := ServeMCP(context.Background(), strings.NewReader(input), &output, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "do not report the environment healthy") {
		t.Fatalf("MCP instructions do not clarify unknown coverage: %s", output.String())
	}
}

func TestMCPSupportsLegacyInitialize(t *testing.T) {
	input := `{"jsonrpc":"2.0","id":"init","method":"initialize","params":{"protocolVersion":"2025-11-25"}}` + "\n"
	var output strings.Builder
	if err := ServeMCP(context.Background(), strings.NewReader(input), &output, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), `"protocolVersion":"2025-11-25"`) {
		t.Fatalf("unexpected response: %s", output.String())
	}
}

func TestMCPAnyHandlerReturnsStructuredMonitorResult(t *testing.T) {
	input := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"tailg_list_incidents","arguments":{}}}` + "\n"
	var output strings.Builder
	handler := func(_ context.Context, name string, _ ToolArguments) (any, error) {
		if name != "tailg_list_incidents" {
			t.Fatalf("unexpected tool %q", name)
		}
		return map[string]any{"status": "healthy", "incidents": []any{}}, nil
	}
	if err := ServeMCPAny(context.Background(), strings.NewReader(input), &output, handler); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), `"structuredContent":{"incidents":[],"status":"healthy"}`) {
		t.Fatalf("custom result was not returned as structured content: %s", output.String())
	}
}
