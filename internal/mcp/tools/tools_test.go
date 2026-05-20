package tools_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mgoodric/security-atlas/internal/mcp"
	"github.com/mgoodric/security-atlas/internal/mcp/tools"
)

// newTestClient spins up an httptest.Server with the given handler and
// returns a wired *mcp.Client + the server (caller closes).
func newTestClient(t *testing.T, h http.HandlerFunc) (*mcp.Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(h)
	c, err := mcp.NewClient(srv.URL, "test-bearer", "v0.0.0-test")
	if err != nil {
		srv.Close()
		t.Fatalf("NewClient: %v", err)
	}
	return c, srv
}

// ===== list_controls =====

// TestListControls_HappyPath verifies AC-6: the tool dispatches to
// /v1/controls and returns the canonical row shape with limit/capped
// metadata.
func TestListControls_HappyPath(t *testing.T) {
	t.Parallel()

	client, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/controls" {
			t.Errorf("path = %q, want /v1/controls", r.URL.Path)
		}
		_, _ = fmt.Fprint(w, `{"controls":[{"id":"00000000-0000-0000-0000-000000000001","title":"T","control_family":"IAC","scf_id":"IAC-06","lifecycle_state":"active","bundle_id":"b1"}],"count":1}`)
	})
	defer srv.Close()

	tool := tools.NewListControls(client)
	out, err := tool.Handle(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	m := out.(map[string]any)
	if m["count"].(int) != 1 {
		t.Errorf("count = %v, want 1", m["count"])
	}
	if m["limit"].(int) != tools.DefaultLimit {
		t.Errorf("limit = %v, want %d", m["limit"], tools.DefaultLimit)
	}
}

// TestListControls_RejectsUnknownArgs verifies AC-12 — DisallowUnknownFields
// rejects typos rather than silently ignoring (P0-A3).
func TestListControls_RejectsUnknownArgs(t *testing.T) {
	t.Parallel()

	client, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("HTTP should not be hit: %s", r.URL.Path)
	})
	defer srv.Close()

	tool := tools.NewListControls(client)
	_, err := tool.Handle(context.Background(), json.RawMessage(`{"framework_ide":"x"}`))
	if err == nil || !strings.Contains(err.Error(), "invalid arguments") {
		t.Errorf("expected invalid-arguments error, got: %v", err)
	}
}

// TestListControls_LimitOverCap verifies P0-A9: a limit > 500 errors
// rather than silently truncating.
func TestListControls_LimitOverCap(t *testing.T) {
	t.Parallel()

	client, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("HTTP should not be hit on cap rejection")
	})
	defer srv.Close()

	tool := tools.NewListControls(client)
	_, err := tool.Handle(context.Background(), json.RawMessage(`{"limit":501}`))
	if err == nil || !strings.Contains(err.Error(), "exceeds max 500") {
		t.Errorf("expected cap-exceeded error, got: %v", err)
	}
}

// TestListControls_UnsupportedFiltersRejected verifies framework_id /
// scope filter rejection (the platform endpoint doesn't accept them
// yet). P0-A3 — don't relax validators.
func TestListControls_UnsupportedFiltersRejected(t *testing.T) {
	t.Parallel()

	client, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("HTTP should not be hit on rejected filter")
	})
	defer srv.Close()

	tool := tools.NewListControls(client)
	for _, body := range []string{
		`{"framework_id":"soc2"}`,
		`{"scope":"prod"}`,
	} {
		_, err := tool.Handle(context.Background(), json.RawMessage(body))
		if err == nil || !strings.Contains(err.Error(), "not yet supported") {
			t.Errorf("body %s: expected not-yet-supported error, got: %v", body, err)
		}
	}
}

// ===== get_control =====

func TestGetControl_RequiresID(t *testing.T) {
	t.Parallel()

	client, srv := newTestClient(t, func(http.ResponseWriter, *http.Request) {})
	defer srv.Close()

	tool := tools.NewGetControl(client)
	_, err := tool.Handle(context.Background(), json.RawMessage(`{}`))
	if err == nil || !strings.Contains(err.Error(), "required") {
		t.Errorf("expected required error, got: %v", err)
	}
}

func TestGetControl_UUIDPath(t *testing.T) {
	t.Parallel()

	wantID := "00000000-0000-0000-0000-000000000001"
	client, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/controls" {
			t.Errorf("path = %q, want /v1/controls", r.URL.Path)
		}
		_, _ = fmt.Fprintf(w, `{"controls":[{"id":%q,"title":"T","control_family":"IAC","scf_id":"IAC-06","lifecycle_state":"active","bundle_id":"b1"}],"count":1}`, wantID)
	})
	defer srv.Close()

	tool := tools.NewGetControl(client)
	out, err := tool.Handle(context.Background(), json.RawMessage(`{"anchor_id":"`+wantID+`"}`))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	m := out.(map[string]any)
	if _, ok := m["control"]; !ok {
		t.Errorf("expected control field, got %+v", m)
	}
}

func TestGetControl_ShortCodePath(t *testing.T) {
	t.Parallel()

	client, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/v1/anchors/") {
			t.Errorf("path = %q, want /v1/anchors/ prefix", r.URL.Path)
		}
		_, _ = fmt.Fprint(w, `{"anchor":{"id":"a-uuid","short_code":"IAC-06","title":"T","family":"IAC"}}`)
	})
	defer srv.Close()

	tool := tools.NewGetControl(client)
	out, err := tool.Handle(context.Background(), json.RawMessage(`{"anchor_id":"IAC-06"}`))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	m := out.(map[string]any)
	if _, ok := m["anchor"]; !ok {
		t.Errorf("expected anchor field, got %+v", m)
	}
}

// ===== list_risks =====

func TestListRisks_ForwardsStatusFilter(t *testing.T) {
	t.Parallel()

	client, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("treatment"); got != "accept" {
			t.Errorf("treatment query = %q, want accept", got)
		}
		_, _ = fmt.Fprint(w, `{"risks":[],"count":0}`)
	})
	defer srv.Close()

	tool := tools.NewListRisks(client)
	_, err := tool.Handle(context.Background(), json.RawMessage(`{"status":"accept"}`))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
}

func TestGetRisk_ParsesUUID(t *testing.T) {
	t.Parallel()

	client, srv := newTestClient(t, func(http.ResponseWriter, *http.Request) {
		t.Errorf("HTTP should not be hit on bad UUID")
	})
	defer srv.Close()

	tool := tools.NewGetRisk(client)
	_, err := tool.Handle(context.Background(), json.RawMessage(`{"risk_id":"not-a-uuid"}`))
	if err == nil || !strings.Contains(err.Error(), "must be a UUID") {
		t.Errorf("expected UUID error, got: %v", err)
	}
}

// ===== list_evidence =====

// TestListEvidence_NoPayloadJSON_DefenseInDepth verifies P0-A5 at the
// strict level: even when the platform response includes a payload-like
// field with a different name, the typed evidenceRow struct drops it
// on unmarshal.
func TestListEvidence_NoPayloadJSON_DefenseInDepth(t *testing.T) {
	t.Parallel()

	// Inject a `payload_json` field into the platform response; the
	// typed struct must drop it.
	client, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `{
			"control_id":"",
			"evidence":[{
				"evidence_id":"e1",
				"evidence_kind":"k",
				"observed_at":"2026-05-19T00:00:00Z",
				"source":{},
				"content_hash":"h",
				"scope_cell":null,
				"result":"pass",
				"payload_json":"THIS-SHOULD-NEVER-LEAK"
			}],
			"count":1
		}`)
	})
	defer srv.Close()

	tool := tools.NewListEvidence(client)
	out, err := tool.Handle(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	// Marshal the tool output and assert payload_json is absent.
	body, _ := json.Marshal(out)
	if strings.Contains(string(body), "THIS-SHOULD-NEVER-LEAK") {
		t.Fatalf("P0-A5 violation: payload_json leaked into tool response: %s", string(body))
	}
	if strings.Contains(string(body), "payload_json") {
		t.Fatalf("P0-A5 violation: payload_json field appeared in tool response: %s", string(body))
	}
}

func TestListEvidence_ResultEnumValidation(t *testing.T) {
	t.Parallel()

	client, srv := newTestClient(t, func(http.ResponseWriter, *http.Request) {
		t.Errorf("HTTP should not be hit on bad enum")
	})
	defer srv.Close()

	tool := tools.NewListEvidence(client)
	_, err := tool.Handle(context.Background(), json.RawMessage(`{"result":"unknown"}`))
	if err == nil || !strings.Contains(err.Error(), "must be one of") {
		t.Errorf("expected enum error, got: %v", err)
	}
}

func TestListEvidence_ControlIDMustBeUUID(t *testing.T) {
	t.Parallel()

	client, srv := newTestClient(t, func(http.ResponseWriter, *http.Request) {
		t.Errorf("HTTP should not be hit on bad UUID")
	})
	defer srv.Close()

	tool := tools.NewListEvidence(client)
	_, err := tool.Handle(context.Background(), json.RawMessage(`{"control_id":"not-uuid"}`))
	if err == nil || !strings.Contains(err.Error(), "must be a UUID") {
		t.Errorf("expected UUID error, got: %v", err)
	}
}

// ===== list_audit_periods =====

func TestListAuditPeriods_FilterStatusClientSide(t *testing.T) {
	t.Parallel()

	client, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `{
			"audit_periods":[
				{"id":"a1","name":"Open","status":"open","period_start":"2026-01-01T00:00:00Z","period_end":"2026-03-31T00:00:00Z","framework_version_id":"fv1","created_by":"u","created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z"},
				{"id":"a2","name":"Frozen","status":"frozen","period_start":"2026-01-01T00:00:00Z","period_end":"2026-03-31T00:00:00Z","framework_version_id":"fv1","created_by":"u","created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z"}
			],
			"count":2
		}`)
	})
	defer srv.Close()

	tool := tools.NewListAuditPeriods(client)
	out, err := tool.Handle(context.Background(), json.RawMessage(`{"status":"frozen"}`))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	m := out.(map[string]any)
	if m["count"].(int) != 1 {
		t.Errorf("count = %v, want 1 (status filter)", m["count"])
	}
}

func TestListAuditPeriods_StatusEnumValidation(t *testing.T) {
	t.Parallel()

	client, srv := newTestClient(t, func(http.ResponseWriter, *http.Request) {
		t.Errorf("HTTP should not be hit on bad enum")
	})
	defer srv.Close()

	tool := tools.NewListAuditPeriods(client)
	_, err := tool.Handle(context.Background(), json.RawMessage(`{"status":"in-flight"}`))
	if err == nil || !strings.Contains(err.Error(), "must be one of") {
		t.Errorf("expected enum error, got: %v", err)
	}
}

// ===== All() wires correctly =====

func TestAll_WiresSixTools(t *testing.T) {
	t.Parallel()

	client, _ := mcp.NewClient("http://localhost:8080", "test-bearer", "v0.0.0-test")
	all := tools.All(client)
	if len(all) != 6 {
		t.Fatalf("All() = %d tools, want 6 (P0-A10)", len(all))
	}
	// Names must match CanonicalToolOrder exactly.
	for i, want := range mcp.CanonicalToolOrder {
		if got := all[i].Definition().Name; got != want {
			t.Errorf("all[%d] = %q, want %q (CanonicalToolOrder)", i, got, want)
		}
	}
}
