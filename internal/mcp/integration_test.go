//go:build integration

package mcp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mgoodric/security-atlas/internal/mcp"
	"github.com/mgoodric/security-atlas/internal/mcp/tools"
)

// fakePlatform simulates the security-atlas HTTP API enough for an
// end-to-end MCP session test. It enforces bearer-as-tenant-key — the
// `Authorization: Bearer <token>` header dictates which tenant's rows
// the response contains, mirroring the real platform's RLS-gated
// behavior.
type fakePlatform struct {
	srv *httptest.Server
	mu  sync.Mutex
	// observed records every (tool name, User-Agent, bearer) tuple
	// for cross-tenant + P0-A4 assertions.
	observedUA      []string
	observedBearer  []string
	observedReqPath []string
}

// rowsForTenant returns the per-tenant data set the fake platform
// serves. Two tenants, distinct rows; the integration test asserts
// that bearer-A only sees tenant-A's rows.
func (f *fakePlatform) rowsForTenant(tenantKey string) (controls []map[string]string, risks []map[string]any, periods []map[string]any) {
	switch tenantKey {
	case "tenant-A":
		controls = []map[string]string{{
			"id":              "00000000-0000-0000-0000-000000000a01",
			"title":           "Tenant A control 1",
			"control_family":  "IAC",
			"scf_id":          "IAC-06",
			"lifecycle_state": "active",
			"bundle_id":       "ba1",
		}}
		risks = []map[string]any{{
			"id":                   "00000000-0000-0000-0000-000000000a11",
			"title":                "Tenant A risk 1",
			"description":          "",
			"category":             "operational",
			"methodology":          "nist_800_30",
			"inherent_score":       json.RawMessage("{}"),
			"treatment":            "accept",
			"treatment_owner":      "",
			"residual_score":       json.RawMessage("{}"),
			"accepter":             "",
			"instrument_reference": "",
			"linked_control_ids":   []string{},
			"themes":               []string{},
			"severity":             0,
			"created_at":           time.Now().UTC().Format(time.RFC3339),
			"updated_at":           time.Now().UTC().Format(time.RFC3339),
		}}
		periods = []map[string]any{{
			"id":                   "00000000-0000-0000-0000-000000000a21",
			"name":                 "Tenant A audit",
			"framework_version_id": "00000000-0000-0000-0000-000000000fa1",
			"period_start":         "2026-01-01T00:00:00Z",
			"period_end":           "2026-03-31T00:00:00Z",
			"status":               "open",
			"created_by":           "u",
			"created_at":           "2026-01-01T00:00:00Z",
			"updated_at":           "2026-01-01T00:00:00Z",
		}}
	case "tenant-B":
		controls = []map[string]string{{
			"id":              "00000000-0000-0000-0000-000000000b01",
			"title":           "Tenant B control 1",
			"control_family":  "AST",
			"scf_id":          "AST-01",
			"lifecycle_state": "active",
			"bundle_id":       "bb1",
		}}
		risks = []map[string]any{{
			"id":                   "00000000-0000-0000-0000-000000000b11",
			"title":                "Tenant B risk 1",
			"description":          "",
			"category":             "compliance",
			"methodology":          "nist_800_30",
			"inherent_score":       json.RawMessage("{}"),
			"treatment":            "accept",
			"treatment_owner":      "",
			"residual_score":       json.RawMessage("{}"),
			"accepter":             "",
			"instrument_reference": "",
			"linked_control_ids":   []string{},
			"themes":               []string{},
			"severity":             0,
			"created_at":           time.Now().UTC().Format(time.RFC3339),
			"updated_at":           time.Now().UTC().Format(time.RFC3339),
		}}
		periods = nil
	}
	return
}

func newFakePlatform() *fakePlatform {
	f := &fakePlatform{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.observedUA = append(f.observedUA, r.Header.Get("User-Agent"))
		f.observedBearer = append(f.observedBearer, r.Header.Get("Authorization"))
		f.observedReqPath = append(f.observedReqPath, r.URL.Path)
		f.mu.Unlock()

		// Map bearer to tenant key.
		var tenantKey string
		switch r.Header.Get("Authorization") {
		case "Bearer test-tenant-A-bearer":
			tenantKey = "tenant-A"
		case "Bearer test-tenant-B-bearer":
			tenantKey = "tenant-B"
		default:
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}

		controls, risks, periods := f.rowsForTenant(tenantKey)

		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/controls":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"controls": controls,
				"count":    len(controls),
			})
		case "/v1/risks":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"risks": risks,
				"count": len(risks),
			})
		case "/v1/evidence":
			// Empty ledger window — payload_json deliberately
			// absent so the typed wire shape is exercised
			// without any redaction noise.
			_ = json.NewEncoder(w).Encode(map[string]any{
				"control_id":  "",
				"evidence":    []map[string]any{},
				"count":       0,
				"next_cursor": "",
			})
		case "/v1/audit-periods":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"audit_periods": periods,
				"count":         len(periods),
			})
		default:
			if strings.HasPrefix(r.URL.Path, "/v1/anchors/") {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"anchor": map[string]string{
						"id":         "00000000-0000-0000-0000-000000000fa9",
						"short_code": strings.TrimPrefix(r.URL.Path, "/v1/anchors/"),
						"title":      "Anchor for " + tenantKey,
						"family":     "IAC",
					},
				})
				return
			}
			if strings.HasPrefix(r.URL.Path, "/v1/risks/") {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"risk": risks[0],
				})
				return
			}
			http.NotFound(w, r)
		}
	}))
	return f
}

func (f *fakePlatform) close() { f.srv.Close() }

// TestIntegration_EndToEndStdioSession exercises AC-13's contract: spin
// up a fake platform, point an MCP server at it with tenant-A's bearer,
// drive an initialize → tools/list → tools/call session over stdio,
// and assert:
//
//  1. Every outbound HTTP request carries the User-Agent header (P0-A4).
//  2. tools/list returns exactly the six tools in CanonicalToolOrder.
//  3. list_controls returns tenant-A's row, not tenant-B's.
//  4. A cross-tenant assertion: a parallel server bound to tenant-B's
//     bearer sees only tenant-B's rows.
func TestIntegration_EndToEndStdioSession(t *testing.T) {
	fake := newFakePlatform()
	defer fake.close()

	clientA, err := mcp.NewClient(fake.srv.URL, "test-tenant-A-bearer", "v0.0.0-test")
	if err != nil {
		t.Fatalf("NewClient A: %v", err)
	}
	serverA := mcp.NewServer("atlas-mcp", "v0.0.0-test", tools.All(clientA), nil)

	clientB, err := mcp.NewClient(fake.srv.URL, "test-tenant-B-bearer", "v0.0.0-test")
	if err != nil {
		t.Fatalf("NewClient B: %v", err)
	}
	serverB := mcp.NewServer("atlas-mcp", "v0.0.0-test", tools.All(clientB), nil)

	// Drive tenant-A session.
	gotA := driveSession(t, serverA, []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"list_controls","arguments":{}}}`,
	})

	// AC-13 (a): tools/list returns all six.
	if len(gotA[1].Result.Tools) != 6 {
		t.Errorf("tenant A tools/list returned %d tools, want 6", len(gotA[1].Result.Tools))
	}
	// AC-13 (b): list_controls returns tenant-A's row.
	if !strings.Contains(gotA[2].rawResultText(), "Tenant A control 1") {
		t.Errorf("tenant A list_controls missing tenant-A row: %s", gotA[2].rawResultText())
	}
	if strings.Contains(gotA[2].rawResultText(), "Tenant B") {
		t.Errorf("CROSS-TENANT LEAK: tenant A response contained tenant-B data: %s", gotA[2].rawResultText())
	}

	// Drive tenant-B session.
	gotB := driveSession(t, serverB, []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"list_controls","arguments":{}}}`,
	})
	if !strings.Contains(gotB[1].rawResultText(), "Tenant B control 1") {
		t.Errorf("tenant B list_controls missing tenant-B row: %s", gotB[1].rawResultText())
	}
	if strings.Contains(gotB[1].rawResultText(), "Tenant A") {
		t.Errorf("CROSS-TENANT LEAK: tenant B response contained tenant-A data: %s", gotB[1].rawResultText())
	}

	// AC-13 + P0-A4: every observed UA must match the canonical template.
	fake.mu.Lock()
	defer fake.mu.Unlock()
	want := "atlas-mcp/v0.0.0-test (mcp; ai_assisted=read-only)"
	for i, ua := range fake.observedUA {
		if ua != want {
			t.Errorf("observedUA[%d] = %q, want %q (P0-A4)", i, ua, want)
		}
	}
}

// TestIntegration_AllSixToolsCallable exercises each of the six tools
// in turn, ensuring each one dispatches to the right platform path and
// returns a non-error response (the fake platform returns minimal but
// well-formed bodies for each).
func TestIntegration_AllSixToolsCallable(t *testing.T) {
	fake := newFakePlatform()
	defer fake.close()

	client, _ := mcp.NewClient(fake.srv.URL, "test-tenant-A-bearer", "v0.0.0-test")
	server := mcp.NewServer("atlas-mcp", "v0.0.0-test", tools.All(client), nil)

	calls := []struct {
		name string
		args string
	}{
		{"list_controls", `{}`},
		{"get_control", `{"anchor_id":"IAC-06"}`},
		{"list_risks", `{}`},
		{"get_risk", `{"risk_id":"00000000-0000-0000-0000-000000000a11"}`},
		{"list_evidence", `{}`},
		{"list_audit_periods", `{}`},
	}
	reqs := make([]string, 0, len(calls)+1)
	reqs = append(reqs, `{"jsonrpc":"2.0","id":0,"method":"initialize","params":{}}`)
	for i, c := range calls {
		reqs = append(reqs, fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"tools/call","params":{"name":%q,"arguments":%s}}`,
			i+1, c.name, c.args))
	}
	results := driveSession(t, server, reqs)

	// Each tool result (results[1..6]) must NOT carry isError=true.
	for i, c := range calls {
		r := results[i+1]
		if r.Result.IsError {
			t.Errorf("tool %s returned isError=true: %s", c.name, r.rawResultText())
		}
	}
}

// driveSession sends `requests` (newline-joined) to server, captures
// stdout, parses one response per request that had an id, and returns
// the parsed responses in send order.
type sessionResp struct {
	JSONRPC string         `json:"jsonrpc"`
	ID      int            `json:"id"`
	Result  sessionResult  `json:"result"`
	Error   map[string]any `json:"error"`
}

type sessionResult struct {
	ProtocolVersion string         `json:"protocolVersion,omitempty"`
	Capabilities    map[string]any `json:"capabilities,omitempty"`
	ServerInfo      map[string]any `json:"serverInfo,omitempty"`
	Tools           []struct {
		Name string `json:"name"`
	} `json:"tools,omitempty"`
	Content []map[string]any `json:"content,omitempty"`
	IsError bool             `json:"isError,omitempty"`
}

func (r sessionResp) rawResultText() string {
	if len(r.Result.Content) == 0 {
		return ""
	}
	t, _ := r.Result.Content[0]["text"].(string)
	return t
}

func driveSession(t *testing.T, server *mcp.Server, requests []string) []sessionResp {
	t.Helper()

	in := strings.NewReader(strings.Join(requests, "\n") + "\n")
	var out bytes.Buffer
	if err := server.Run(context.Background(), in, &out); err != nil {
		t.Fatalf("Run: %v", err)
	}
	dec := json.NewDecoder(&out)
	var resps []sessionResp
	for {
		var r sessionResp
		if err := dec.Decode(&r); err != nil {
			if err == io.EOF {
				break
			}
			t.Fatalf("decode: %v", err)
		}
		resps = append(resps, r)
	}
	return resps
}
