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
	After        string `json:"after,omitempty"`
	Limit        *int   `json:"limit,omitempty"`
	EvidenceID   string `json:"evidenceId,omitempty"`
	Cursor       string `json:"cursor,omitempty"`
	PageBytes    *int   `json:"pageBytes,omitempty"`
	IncidentID   string `json:"incidentId,omitempty"`
	Title        string `json:"title,omitempty"`
	Description  string `json:"description,omitempty"`
}

type ToolHandler func(context.Context, string, ToolArguments) (Report, error)
type AnyToolHandler func(context.Context, string, ToolArguments) (any, error)

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
	var anyHandler AnyToolHandler
	if handler != nil {
		anyHandler = func(ctx context.Context, name string, arguments ToolArguments) (any, error) {
			return handler(ctx, name, arguments)
		}
	}
	return ServeMCPAny(ctx, input, output, anyHandler)
}

func ServeMCPAny(ctx context.Context, input io.Reader, output io.Writer, handler AnyToolHandler) error {
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
				"instructions": "Use tailg_monitor to persist incident changes, tailg_get_changes to resume from a cursor, and tailg_list_incidents for current state. Treat status=unknown or coverage other than complete as insufficient evidence: do not report the environment healthy or resolve incidents; retry or investigate the collection gap. Use tailg_capture_issue_evidence and tailg_read_evidence_page for paginated retained logs. tailg_create_azure_devops_work_item creates or reuses a tracked item for an open Tailg incident only when Azure DevOps is configured.",
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
				"instructions": "Bounded Kubernetes diagnostics with persistent incident state and cursor-based change replay when --state is configured. Status unknown or incomplete coverage means Tailg could not establish health; do not interpret it as healthy.",
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
			result, err := handler(ctx, params.Name, arguments)
			if err != nil {
				response.Result = map[string]any{"resultType": "complete", "isError": true, "content": []any{map[string]string{"type": "text", "text": Redact(err.Error())}}}
				break
			}
			encoded, _ := json.Marshal(result)
			response.Result = map[string]any{
				"resultType": "complete", "isError": false,
				"content":           []any{map[string]string{"type": "text", "text": string(encoded)}},
				"structuredContent": result,
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
		{"name": "tailg_monitor", "description": "Poll the configured Kubernetes scope and persist incident changes. This writes only the local monitor state file.", "inputSchema": schema(nil, nil), "annotations": map[string]any{"readOnlyHint": false, "destructiveHint": false}},
		{"name": "tailg_list_incidents", "description": "Read persistent incidents and current coverage from the configured monitor state.", "inputSchema": schema(nil, nil), "annotations": map[string]any{"readOnlyHint": true, "destructiveHint": false}},
		{"name": "tailg_get_changes", "description": "Read monitor changes after a durable cursor; continue from nextCursor and reset if resetRequired is true.", "inputSchema": schema(map[string]any{"after": map[string]any{"type": "string"}, "limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 1000}}, nil), "annotations": map[string]any{"readOnlyHint": true, "destructiveHint": false}},
		{"name": "tailg_capture_issue_evidence", "description": "Capture and privately save complete retained logs around one issue. Returns an evidence ID for paginated reading.", "inputSchema": schema(map[string]any{"issueId": map[string]any{"type": "string", "pattern": "^[0-9a-f]{16}$"}}, []string{"issueId"}), "annotations": map[string]any{"readOnlyHint": false, "destructiveHint": false}},
		{"name": "tailg_read_evidence_page", "description": "Read one bounded UTF-8 page from a saved issue evidence snapshot. Continue with nextCursor.", "inputSchema": schema(map[string]any{"evidenceId": map[string]any{"type": "string", "pattern": "^[0-9a-f]{64}$"}, "cursor": map[string]any{"type": "string"}, "pageBytes": map[string]any{"type": "integer", "minimum": 1024, "maximum": 262144}}, []string{"evidenceId"}), "annotations": map[string]any{"readOnlyHint": true, "destructiveHint": false}},
		{"name": "tailg_create_azure_devops_work_item", "description": "Create or reuse an Azure DevOps work item for an open Tailg incident. Requires MCP --state, --ado-organization, --ado-project, and TAILG_AZDO_TOKEN. Deduplicates by incident ID; never creates for resolved incidents.", "inputSchema": schema(map[string]any{"incidentId": map[string]any{"type": "string", "pattern": "^[0-9a-f]{32}$"}, "title": map[string]any{"type": "string", "maxLength": 200}, "description": map[string]any{"type": "string", "maxLength": 20000}}, []string{"incidentId"}), "annotations": map[string]any{"readOnlyHint": false, "destructiveHint": false}},
	}
}

func UnknownTool(name string) error { return fmt.Errorf("unknown tool %q", name) }
