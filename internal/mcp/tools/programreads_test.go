package tools_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/mgoodric/security-atlas/internal/mcp/tools"
)

// The four program-record read tools share one generic passthrough
// implementation, so these tests exercise the shared behavior through
// list_policies and assert the other three dispatch to the right
// endpoint + list key.

func TestListPolicies_PassthroughAndEnvelope(t *testing.T) {
	t.Parallel()

	client, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/policies" {
			t.Errorf("path = %q, want /v1/policies", r.URL.Path)
		}
		_, _ = fmt.Fprint(w, `{"policies":[{"id":"p1","title":"Access Control Policy","state":"published"}],"count":1}`)
	})
	defer srv.Close()

	out, err := tools.NewListPolicies(client).Handle(context.Background(), json.RawMessage(`{}`))
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
	if m["capped"].(bool) {
		t.Errorf("capped = true, want false for a single row")
	}
	// The row is passed through verbatim (raw platform JSON), so the
	// source field survives without a typed row dropping it.
	rows := m["policies"].([]json.RawMessage)
	if len(rows) != 1 || !strings.Contains(string(rows[0]), `"Access Control Policy"`) {
		t.Errorf("passthrough row missing: %v", rows)
	}
}

func TestProgramList_CapsResults(t *testing.T) {
	t.Parallel()

	client, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `{"vendors":[{"id":"v1"},{"id":"v2"},{"id":"v3"}]}`)
	})
	defer srv.Close()

	out, err := tools.NewListVendors(client).Handle(context.Background(), json.RawMessage(`{"limit":2}`))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	m := out.(map[string]any)
	if m["count"].(int) != 2 {
		t.Errorf("count = %v, want 2 (capped)", m["count"])
	}
	if !m["capped"].(bool) {
		t.Errorf("capped = false, want true when rows exceed limit")
	}
}

func TestProgramList_RejectsUnknownArgs(t *testing.T) {
	t.Parallel()

	client, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("HTTP should not be hit on a bad-args request: %s", r.URL.Path)
	})
	defer srv.Close()

	_, err := tools.NewListExceptions(client).Handle(context.Background(), json.RawMessage(`{"statuss":"active"}`))
	if err == nil || !strings.Contains(err.Error(), "invalid arguments") {
		t.Errorf("expected invalid-arguments error, got: %v", err)
	}
}

func TestListActionPlans_DispatchesToEndpoint(t *testing.T) {
	t.Parallel()

	hit := false
	client, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		hit = true
		if r.URL.Path != "/v1/action-plans" {
			t.Errorf("path = %q, want /v1/action-plans", r.URL.Path)
		}
		_, _ = fmt.Fprint(w, `{"action_plans":[]}`)
	})
	defer srv.Close()

	if _, err := tools.NewListActionPlans(client).Handle(context.Background(), json.RawMessage(`{}`)); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if !hit {
		t.Error("action-plans endpoint was not called")
	}
}
