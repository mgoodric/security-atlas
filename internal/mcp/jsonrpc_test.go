package mcp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mgoodric/security-atlas/internal/mcp"
)

// stubTool implements mcp.Tool for protocol-layer testing without the
// HTTP client (slice 172 AC-12 unit-test layer).
type stubTool struct {
	name        string
	description string
	schema      string
	handler     func(ctx context.Context, args json.RawMessage) (any, error)
}

func (s *stubTool) Definition() mcp.ToolDefinition {
	return mcp.ToolDefinition{
		Name:        s.name,
		Description: s.description,
		InputSchema: json.RawMessage(s.schema),
	}
}

func (s *stubTool) Handle(ctx context.Context, args json.RawMessage) (any, error) {
	return s.handler(ctx, args)
}

// TestInitializeHandshake checks AC-2: the server responds to
// `initialize` with the expected protocol version, tools capability,
// and serverInfo.
func TestInitializeHandshake(t *testing.T) {
	t.Parallel()

	server := mcp.NewServer("atlas-mcp", "v0.0.0-test", nil, nil)

	req := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}` + "\n"
	var out bytes.Buffer
	if err := server.Run(context.Background(), strings.NewReader(req), &out); err != nil {
		t.Fatalf("Run: %v", err)
	}

	var resp struct {
		JSONRPC string `json:"jsonrpc"`
		ID      int    `json:"id"`
		Result  struct {
			ProtocolVersion string         `json:"protocolVersion"`
			Capabilities    map[string]any `json:"capabilities"`
			ServerInfo      map[string]any `json:"serverInfo"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v body=%q", err, out.String())
	}
	if resp.JSONRPC != "2.0" {
		t.Errorf("jsonrpc = %q, want %q", resp.JSONRPC, "2.0")
	}
	if resp.ID != 1 {
		t.Errorf("id = %d, want 1", resp.ID)
	}
	if resp.Result.ProtocolVersion != mcp.ProtocolVersion {
		t.Errorf("protocolVersion = %q, want %q", resp.Result.ProtocolVersion, mcp.ProtocolVersion)
	}
	if _, ok := resp.Result.Capabilities["tools"]; !ok {
		t.Errorf("capabilities missing tools: %+v", resp.Result.Capabilities)
	}
	if resp.Result.ServerInfo["name"] != "atlas-mcp" {
		t.Errorf("serverInfo.name = %v, want atlas-mcp", resp.Result.ServerInfo["name"])
	}
}

// TestToolsList verifies AC-2 / AC-12 — `tools/list` returns each
// registered tool with its name + schema, in CanonicalToolOrder.
func TestToolsList(t *testing.T) {
	t.Parallel()

	tools := []mcp.Tool{
		&stubTool{name: "get_control", description: "X", schema: `{"type":"object"}`,
			handler: func(context.Context, json.RawMessage) (any, error) { return nil, nil }},
		&stubTool{name: "list_controls", description: "Y", schema: `{"type":"object"}`,
			handler: func(context.Context, json.RawMessage) (any, error) { return nil, nil }},
	}
	server := mcp.NewServer("atlas-mcp", "v0.0.0-test", tools, nil)

	req := `{"jsonrpc":"2.0","id":2,"method":"tools/list"}` + "\n"
	var out bytes.Buffer
	if err := server.Run(context.Background(), strings.NewReader(req), &out); err != nil {
		t.Fatalf("Run: %v", err)
	}
	var resp struct {
		Result struct {
			Tools []mcp.ToolDefinition `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Result.Tools) != 2 {
		t.Fatalf("tools len = %d, want 2", len(resp.Result.Tools))
	}
	// CanonicalToolOrder lists list_controls BEFORE get_control;
	// the server should reorder to match (regardless of registration order).
	if resp.Result.Tools[0].Name != "list_controls" {
		t.Errorf("tools[0] = %q, want list_controls", resp.Result.Tools[0].Name)
	}
	if resp.Result.Tools[1].Name != "get_control" {
		t.Errorf("tools[1] = %q, want get_control", resp.Result.Tools[1].Name)
	}
}

// TestToolsCallSuccess verifies AC-12 happy path — tool invocation
// flows through and the result is wrapped in MCP content shape.
func TestToolsCallSuccess(t *testing.T) {
	t.Parallel()

	called := false
	tools := []mcp.Tool{
		&stubTool{name: "list_controls", description: "X", schema: `{"type":"object"}`,
			handler: func(_ context.Context, args json.RawMessage) (any, error) {
				called = true
				return map[string]any{"controls": []any{}, "count": 0}, nil
			}},
	}
	server := mcp.NewServer("atlas-mcp", "v0.0.0-test", tools, nil)

	req := `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"list_controls","arguments":{}}}` + "\n"
	var out bytes.Buffer
	if err := server.Run(context.Background(), strings.NewReader(req), &out); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !called {
		t.Fatal("handler not invoked")
	}
	var resp struct {
		Result map[string]any `json:"result"`
	}
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v body=%q", err, out.String())
	}
	content, ok := resp.Result["content"].([]any)
	if !ok || len(content) != 1 {
		t.Fatalf("expected content array of length 1, got %+v", resp.Result)
	}
	item, _ := content[0].(map[string]any)
	if item["type"] != "text" {
		t.Errorf("content[0].type = %v, want text", item["type"])
	}
	// The text field is the JSON-encoded tool output.
	if !strings.Contains(item["text"].(string), `"count":0`) {
		t.Errorf("text missing tool output: %v", item["text"])
	}
}

// TestToolsCallErrorReturnsIsError verifies AC-12 sad path — a tool
// returning an error surfaces to the client as result.isError=true
// (NOT as a JSON-RPC error; protocol errors are reserved for the
// transport layer per the MCP spec).
func TestToolsCallErrorReturnsIsError(t *testing.T) {
	t.Parallel()

	tools := []mcp.Tool{
		&stubTool{name: "list_controls", description: "X", schema: `{"type":"object"}`,
			handler: func(context.Context, json.RawMessage) (any, error) {
				return nil, errBadInput
			}},
	}
	server := mcp.NewServer("atlas-mcp", "v0.0.0-test", tools, nil)

	req := `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"list_controls","arguments":{}}}` + "\n"
	var out bytes.Buffer
	if err := server.Run(context.Background(), strings.NewReader(req), &out); err != nil {
		t.Fatalf("Run: %v", err)
	}
	var resp struct {
		Result map[string]any `json:"result"`
		Error  any            `json:"error"`
	}
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Error != nil {
		t.Errorf("expected result.isError, got JSON-RPC error: %v", resp.Error)
	}
	if resp.Result["isError"] != true {
		t.Errorf("expected isError=true, got %v", resp.Result)
	}
}

// TestUnknownMethod verifies an unknown JSON-RPC method returns
// `MethodNotFound` (code -32601).
func TestUnknownMethod(t *testing.T) {
	t.Parallel()

	server := mcp.NewServer("atlas-mcp", "v0.0.0-test", nil, nil)
	req := `{"jsonrpc":"2.0","id":5,"method":"nonexistent/method"}` + "\n"
	var out bytes.Buffer
	if err := server.Run(context.Background(), strings.NewReader(req), &out); err != nil {
		t.Fatalf("Run: %v", err)
	}
	var resp struct {
		Error struct {
			Code int `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Error.Code != -32601 {
		t.Errorf("error.code = %d, want -32601", resp.Error.Code)
	}
}

// TestUnknownTool verifies tools/call with an unknown name returns
// MethodNotFound (the same code as for unknown JSON-RPC methods).
func TestUnknownTool(t *testing.T) {
	t.Parallel()

	server := mcp.NewServer("atlas-mcp", "v0.0.0-test", nil, nil)
	req := `{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"does_not_exist","arguments":{}}}` + "\n"
	var out bytes.Buffer
	if err := server.Run(context.Background(), strings.NewReader(req), &out); err != nil {
		t.Fatalf("Run: %v", err)
	}
	var resp struct {
		Error struct {
			Code int `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Error.Code != -32601 {
		t.Errorf("error.code = %d, want -32601", resp.Error.Code)
	}
}

// TestParseError verifies malformed JSON produces a -32700 parse error
// response (JSON-RPC 2.0 standard code).
func TestParseError(t *testing.T) {
	t.Parallel()

	server := mcp.NewServer("atlas-mcp", "v0.0.0-test", nil, nil)
	req := `{not valid json` + "\n"
	var out bytes.Buffer
	if err := server.Run(context.Background(), strings.NewReader(req), &out); err != nil {
		t.Fatalf("Run: %v", err)
	}
	var resp struct {
		Error struct {
			Code int `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Error.Code != -32700 {
		t.Errorf("error.code = %d, want -32700", resp.Error.Code)
	}
}

// TestNotificationGetsNoResponse verifies the JSON-RPC 2.0 rule that
// notifications (messages without an id) get no response. The MCP
// spec uses this for `notifications/initialized`.
func TestNotificationGetsNoResponse(t *testing.T) {
	t.Parallel()

	server := mcp.NewServer("atlas-mcp", "v0.0.0-test", nil, nil)
	req := `{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\n"
	var out bytes.Buffer
	if err := server.Run(context.Background(), strings.NewReader(req), &out); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.Len() != 0 {
		t.Errorf("notification produced output: %q", out.String())
	}
}

// errBadInput is a simple sentinel for the tool-error test.
type errInput struct{}

func (errInput) Error() string { return "bad input" }

var errBadInput = errInput{}
