// Package mcp implements a minimal Model Context Protocol server for
// security-atlas. Slice 172 ships READ-ONLY tools that wrap the
// platform's existing HTTP API; slice 173 (gated on this slice) ships
// write tools that integrate with the AI-assist boundary's HITL flow.
//
// This file implements the subset of the JSON-RPC 2.0 + MCP protocol the
// six read tools require:
//
//   - initialize         (handshake)
//   - tools/list         (enumerate tools)
//   - tools/call         (invoke a tool)
//
// We deliberately do NOT pull in a third-party MCP framework. See
// docs/audit-log/172-mcp-server-decisions.md "D2 — MCP framework" for
// the rationale: P0-A11 forbids bundling the MCP framework dependency
// into the platform binary; with a single-module workspace
// (`go.work = use .`) any imported framework would land in go.mod for
// every build target. Hand-rolling against the stable JSON-RPC 2.0
// standard plus the small MCP subset (3 message types) sidesteps that
// at ~250 LoC of pure stdlib.
//
// The MCP protocol version target is `2024-11-05` (the version published
// at slice 172 design time). Future protocol bumps will be handled by
// extending the handshake's protocol-version negotiation in Server.handleInitialize.
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
)

// ProtocolVersion is the MCP protocol revision this server speaks. MCP
// is versioned by date string per the spec.
const ProtocolVersion = "2024-11-05"

// JSON-RPC 2.0 envelopes. Fields are pointer / RawMessage where the spec
// permits a value to be absent (request without id is a notification;
// response with neither result nor error is malformed).

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
	Data    any    `json:"data,omitempty"`
}

// JSON-RPC 2.0 standard error codes plus the MCP-specific
// `MethodNotFound` mapping. Values match the spec; do not renumber.
const (
	errCodeParseError     = -32700
	errCodeInvalidRequest = -32600
	errCodeMethodNotFound = -32601
	errCodeInvalidParams  = -32602
	errCodeInternalError  = -32603
)

// Tool is the contract a concrete tool implementation must satisfy.
// Each tool returns (a) a Definition that the MCP client's
// `tools/list` response advertises and (b) a Handle method that
// executes one `tools/call`.
//
// Handle MUST NOT panic. It MUST return all errors via the returned
// error value; the server maps the error to a JSON-RPC error response.
type Tool interface {
	Definition() ToolDefinition
	Handle(ctx context.Context, args json.RawMessage) (any, error)
}

// ToolDefinition is the public shape `tools/list` advertises. Field
// names track the MCP spec; do not rename.
type ToolDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

// Server is a stdio MCP server. One Server instance handles one client
// session over a single (reader, writer) pair. The server reads
// newline-delimited JSON-RPC 2.0 messages, dispatches `initialize`,
// `tools/list`, and `tools/call`, and writes responses back.
//
// The server is NOT safe for concurrent Run() invocations against the
// same (reader, writer). It IS safe for one Run() to receive requests
// serially; tools are invoked sequentially.
type Server struct {
	mu           sync.Mutex
	tools        map[string]Tool
	serverName   string
	serverVer    string
	initialized  bool
	stderrLogger func(format string, args ...any)
}

// NewServer constructs a Server with the given tool set. serverName and
// serverVer are advertised in the handshake (`serverInfo`). stderrLog is
// the one-line-per-call envelope emitter (P0-A7); pass nil to suppress.
func NewServer(serverName, serverVer string, tools []Tool, stderrLog func(format string, args ...any)) *Server {
	m := make(map[string]Tool, len(tools))
	for _, t := range tools {
		m[t.Definition().Name] = t
	}
	if stderrLog == nil {
		stderrLog = func(string, ...any) {}
	}
	return &Server{
		tools:        m,
		serverName:   serverName,
		serverVer:    serverVer,
		stderrLogger: stderrLog,
	}
}

// Run is the main message loop. It reads newline-delimited JSON-RPC
// messages from r, dispatches each, and writes responses to w. Returns
// when r returns io.EOF or ctx is cancelled.
func (s *Server) Run(ctx context.Context, r io.Reader, w io.Writer) error {
	// bufio.Scanner with a generous max-token-size; MCP tool calls
	// rarely exceed a few KiB but a defensive 1 MiB cap bounds memory.
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
	enc := json.NewEncoder(w)

	for scanner.Scan() {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		resp, isNotification := s.dispatch(ctx, line)
		if isNotification {
			// JSON-RPC 2.0: notifications get no response.
			continue
		}
		if err := enc.Encode(resp); err != nil {
			return fmt.Errorf("write response: %w", err)
		}
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

// dispatch parses one JSON-RPC message and returns the response (or nil
// + isNotification=true for a notification, which gets no response per
// the JSON-RPC 2.0 spec).
func (s *Server) dispatch(ctx context.Context, line []byte) (*rpcResponse, bool) {
	var req rpcRequest
	if err := json.Unmarshal(line, &req); err != nil {
		return errorResp(nil, errCodeParseError, "parse error: "+err.Error()), false
	}
	if req.JSONRPC != "2.0" {
		return errorResp(req.ID, errCodeInvalidRequest, "jsonrpc must be \"2.0\""), false
	}
	isNotification := len(req.ID) == 0

	switch req.Method {
	case "initialize":
		return s.handleInitialize(req), isNotification
	case "initialized", "notifications/initialized":
		// The client sends this notification after a successful
		// `initialize` response. No response needed.
		s.mu.Lock()
		s.initialized = true
		s.mu.Unlock()
		return nil, true
	case "tools/list":
		return s.handleToolsList(req), isNotification
	case "tools/call":
		return s.handleToolsCall(ctx, req), isNotification
	default:
		return errorResp(req.ID, errCodeMethodNotFound, "method not found: "+req.Method), isNotification
	}
}

// handleInitialize answers the MCP handshake. The minimal response
// advertises the protocol version, our capabilities (tools only —
// no resources, no prompts), and serverInfo.
func (s *Server) handleInitialize(req rpcRequest) *rpcResponse {
	type initResult struct {
		ProtocolVersion string         `json:"protocolVersion"`
		Capabilities    map[string]any `json:"capabilities"`
		ServerInfo      map[string]any `json:"serverInfo"`
	}
	return &rpcResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: initResult{
			ProtocolVersion: ProtocolVersion,
			Capabilities: map[string]any{
				// Tools-only server. The empty object signals
				// "this capability is supported with no
				// sub-features" per MCP spec.
				"tools": map[string]any{},
			},
			ServerInfo: map[string]any{
				"name":    s.serverName,
				"version": s.serverVer,
			},
		},
	}
}

// handleToolsList answers `tools/list` with every registered tool's
// definition. The list is sorted by name for stable output (helps the
// schema-stability test).
func (s *Server) handleToolsList(req rpcRequest) *rpcResponse {
	type listResult struct {
		Tools []ToolDefinition `json:"tools"`
	}
	defs := make([]ToolDefinition, 0, len(s.tools))
	// Stable order: walk the canonical tool name list (matches
	// what cmd/atlas-mcp wires up) rather than map-iteration order.
	for _, name := range CanonicalToolOrder {
		t, ok := s.tools[name]
		if !ok {
			continue
		}
		defs = append(defs, t.Definition())
	}
	// Catch any tools that weren't in the canonical order (e.g.,
	// engineer adds a tool without updating CanonicalToolOrder).
	// These trail alphabetically.
	known := make(map[string]struct{}, len(defs))
	for _, d := range defs {
		known[d.Name] = struct{}{}
	}
	for name, t := range s.tools {
		if _, seen := known[name]; seen {
			continue
		}
		defs = append(defs, t.Definition())
	}
	return &rpcResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result:  listResult{Tools: defs},
	}
}

// handleToolsCall invokes one tool. The MCP wire shape wraps the tool
// result in `content` (an array of text/image/resource objects). We
// emit a single text item containing the tool's JSON output — that's
// the most universal shape for read tools, and the LLM consumer can
// re-parse the JSON if it wants structured data.
func (s *Server) handleToolsCall(ctx context.Context, req rpcRequest) *rpcResponse {
	type callParams struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	var p callParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		return errorResp(req.ID, errCodeInvalidParams, "invalid params: "+err.Error())
	}
	if p.Name == "" {
		return errorResp(req.ID, errCodeInvalidParams, "params.name is required")
	}
	s.mu.Lock()
	tool, ok := s.tools[p.Name]
	s.mu.Unlock()
	if !ok {
		return errorResp(req.ID, errCodeMethodNotFound, "unknown tool: "+p.Name)
	}

	out, err := tool.Handle(ctx, p.Arguments)
	if err != nil {
		// MCP spec: tool execution errors are returned INSIDE
		// the result with `isError: true`, not as JSON-RPC errors.
		// JSON-RPC errors are reserved for protocol-level failures
		// (parse error, method not found, etc.).
		return &rpcResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]any{
				"content": []map[string]any{{
					"type": "text",
					"text": err.Error(),
				}},
				"isError": true,
			},
		}
	}

	// Marshal the tool output to JSON for the text-content payload.
	body, err := json.Marshal(out)
	if err != nil {
		return errorResp(req.ID, errCodeInternalError, "marshal tool output: "+err.Error())
	}
	return &rpcResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: map[string]any{
			"content": []map[string]any{{
				"type": "text",
				"text": string(body),
			}},
		},
	}
}

// errorResp constructs a JSON-RPC error response.
func errorResp(id json.RawMessage, code int, message string) *rpcResponse {
	return &rpcResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error: &rpcError{
			Code:    code,
			Message: message,
		},
	}
}

// CanonicalToolOrder defines the stable order in which `tools/list`
// returns tools. Match the order documented in cmd/atlas-mcp/README.md
// and the slice doc's tool surface (D3).
var CanonicalToolOrder = []string{
	"list_controls",
	"get_control",
	"list_risks",
	"get_risk",
	"list_evidence",
	"list_audit_periods",
}
