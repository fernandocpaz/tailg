package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
)

const MCPProtocolVersion = "2026-07-28"

type ToolArguments struct {
	Target       string `json:"target,omitempty"`
	Namespace    string `json:"namespace,omitempty"`
	Context      string `json:"context,omitempty"`
	Selector     string `json:"selector,omitempty"`
	Container    string `json:"container,omitempty"`
	Since        string `json:"since,omitempty"`
	Tail         *int   `json:"tail,omitempty"`
	MaxLines     *int   `json:"maxLines,omitempty"`
	MaxIssues    *int   `json:"maxIssues,omitempty"`
	ContextLines *int   `json:"contextLines,omitempty"`
	MaxBytes     *int   `json:"maxBytes,omitempty"`
	IssueID      string `json:"issueId,omitempty"`
}

type ToolHandler func(context.Context, string, ToolArguments) (Report, error)

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func ServeMCP(ctx context.Context, input io.Reader, output io.Writer, handler ToolHandler) error {
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	encoder := json.NewEncoder(output)
	encoder.SetEscapeHTML(false)
	for scanner.Scan() {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var request rpcRequest
		if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
			if writeErr := encoder.Encode(rpcResponse{JSONRPC: "2.0", Error: &rpcError{Code: -32700, Message: "Parse error"}}); writeErr != nil {
				return writeErr
			}
			continue
		}
		if len(request.ID) == 0 {
			continue
		}
		response := rpcResponse{JSONRPC: "2.0", ID: request.ID}
		switch request.Method {
		case "server/discover":
			response.Result = map[string]any{
				"resultType": "complete", "supportedVersions": []string{MCPProtocolVersion},
				"capabilities": map[string]any{"tools": map[string]any{}},
				"_meta":        map[string]any{"io.modelcontextprotocol/serverInfo": map[string]string{"name": "tailg", "version": "1"}},
				"instructions": "Read-only Kubernetes diagnostics. Start with tailg_list_issues; call tailg_get_issue_context for a selected issue or tailg_diagnose for pod health and warning events.",
				"ttlMs":        3600000, "cacheScope": "public",
			}
		case "initialize":
			var params struct {
				ProtocolVersion string `json:"protocolVersion"`
			}
			_ = json.Unmarshal(request.Params, &params)
			if params.ProtocolVersion == "" {
				params.ProtocolVersion = "2025-11-25"
			}
			response.Result = map[string]any{
				"protocolVersion": params.ProtocolVersion, "capabilities": map[string]any{"tools": map[string]any{"listChanged": false}},
				"serverInfo":   map[string]string{"name": "tailg", "version": "1"},
				"instructions": "Read-only, bounded Kubernetes issue diagnostics.",
			}
		case "ping":
			response.Result = map[string]any{"resultType": "complete"}
		case "tools/list":
			response.Result = map[string]any{"resultType": "complete", "tools": mcpTools(), "ttlMs": 3600000, "cacheScope": "public"}
		case "tools/call":
			var params struct {
				Name      string          `json:"name"`
				Arguments json.RawMessage `json:"arguments"`
			}
			if err := json.Unmarshal(request.Params, &params); err != nil {
				response.Error = &rpcError{Code: -32602, Message: "Invalid tool parameters"}
				break
			}
			var arguments ToolArguments
			if len(params.Arguments) > 0 {
				if err := json.Unmarshal(params.Arguments, &arguments); err != nil {
					response.Error = &rpcError{Code: -32602, Message: "Invalid tool arguments"}
					break
				}
			}
			if handler == nil {
				response.Error = &rpcError{Code: -32603, Message: "Tool handler is not configured"}
				break
			}
			report, err := handler(ctx, params.Name, arguments)
			if err != nil {
				response.Result = map[string]any{"resultType": "complete", "isError": true, "content": []any{map[string]string{"type": "text", "text": Redact(err.Error())}}}
				break
			}
			encoded, _ := json.Marshal(report)
			response.Result = map[string]any{
				"resultType": "complete", "isError": false,
				"content":           []any{map[string]string{"type": "text", "text": string(encoded)}},
				"structuredContent": report,
			}
		default:
			response.Error = &rpcError{Code: -32601, Message: "Method not found"}
		}
		if err := encoder.Encode(response); err != nil {
			return err
		}
	}
	return scanner.Err()
}

func mcpTools() []map[string]any {
	baseProperties := map[string]any{
		"target":    map[string]any{"type": "string", "description": "Kubernetes resource, app name, wildcard, or comma-separated apps. Omit to scan a namespace."},
		"namespace": map[string]any{"type": "string"}, "context": map[string]any{"type": "string"},
		"selector": map[string]any{"type": "string"}, "container": map[string]any{"type": "string", "description": "Container-name regular expression."},
		"since":        map[string]any{"type": "string", "description": "Bounded relative window such as 30m or 2h."},
		"tail":         map[string]any{"type": "integer", "minimum": 1, "maximum": 50000},
		"maxLines":     map[string]any{"type": "integer", "minimum": 1, "maximum": 50000},
		"maxIssues":    map[string]any{"type": "integer", "minimum": 1, "maximum": 1000},
		"contextLines": map[string]any{"type": "integer", "minimum": 0, "maximum": 50},
		"maxBytes":     map[string]any{"type": "integer", "minimum": 4096, "maximum": 16777216},
	}
	schema := func(extra map[string]any, required []string) map[string]any {
		properties := map[string]any{}
		for key, value := range baseProperties {
			properties[key] = value
		}
		for key, value := range extra {
			properties[key] = value
		}
		result := map[string]any{"type": "object", "properties": properties, "additionalProperties": false}
		if len(required) > 0 {
			result["required"] = required
		}
		return result
	}
	return []map[string]any{
		{"name": "tailg_list_issues", "description": "List grouped active Kubernetes log issues with stable IDs and bounded context. Read-only.", "inputSchema": schema(nil, nil), "annotations": map[string]any{"readOnlyHint": true, "destructiveHint": false}},
		{"name": "tailg_diagnose", "description": "Diagnose log issues, selected pod health, and related Kubernetes warning events. Read-only.", "inputSchema": schema(nil, nil), "annotations": map[string]any{"readOnlyHint": true, "destructiveHint": false}},
		{"name": "tailg_get_issue_context", "description": "Return bounded same-container context for one stable issue ID. Read-only.", "inputSchema": schema(map[string]any{"issueId": map[string]any{"type": "string", "pattern": "^[0-9a-f]{16}$"}}, []string{"issueId"}), "annotations": map[string]any{"readOnlyHint": true, "destructiveHint": false}},
	}
}

func UnknownTool(name string) error { return fmt.Errorf("unknown tool %q", name) }
