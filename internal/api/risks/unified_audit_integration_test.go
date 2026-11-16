//go:build integration

// #1954 — risk_audit_log rows surface in the unified admin audit log as
// kind `risk`, for their own tenant only, and a PATCH with no credential
// in context is refused before anything is written.
//
// Run with:
//
//	go test -tags=integration ./internal/api/risks/ -run 1954

package risks_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/mgoodric/security-atlas/internal/api/risks"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/risk"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

func TestUnifiedAuditLog_ShowsRiskRows_1954(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenantA := freshPlacementTenant(t, admin)
	tenantB := freshPlacementTenant(t, admin)
	// An admin bearer resolves its tenant through a tenants row. Each
	// cleanup is registered after SeedTenant's, so it runs first.
	for _, tenant := range []string{tenantA, tenantB} {
		if _, err := admin.Exec(context.Background(),
			`INSERT INTO tenants (id, name) VALUES ($1, $2)`, tenant, "risk-audit-1954-"+tenant[:8]); err != nil {
			t.Fatalf("seed tenants row: %v", err)
		}
		t.Cleanup(func() {
			_, _ = admin.Exec(context.Background(), `DELETE FROM tenants WHERE id = $1`, tenant)
		})
	}

	envA := adminServer(t, app, tenantA)
	envB := adminServer(t, app, tenantB)

	_, created := createPlaced(t, envA, nil)
	id := riskField(t, created, "id").(string)
	if resp, body := patch(t, envA, "/v1/risks/"+id, `{"level":"org"}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("placement PATCH: status %d (%v)", resp.StatusCode, body)
	}
	if resp, body := patch(t, envA, "/v1/risks/"+id, `{"title":"renamed risk"}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("fields PATCH: status %d (%v)", resp.StatusCode, body)
	}

	now := time.Now().UTC()
	q := url.Values{
		"from": {now.Add(-time.Hour).Format(time.RFC3339)},
		"to":   {now.Add(time.Hour).Format(time.RFC3339)},
		"kind": {"risk"},
	}
	resp, unified := get(t, envA, "/v1/admin/audit-log/unified?"+q.Encode())
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unified audit log: status %d (%v)", resp.StatusCode, unified)
	}
	entries, _ := unified["entries"].([]any)
	actions := map[string]bool{}
	for _, item := range entries {
		e := item.(map[string]any)
		if e["kind"] != "risk" || e["target_type"] != "risk" || e["target_id"] != id {
			t.Errorf("unified entry = %v", e)
		}
		if actor, _ := e["actor_id"].(string); actor == "" || actor == "system" {
			t.Errorf("unified entry actor = %q, want the caller's credential", actor)
		}
		payload, _ := e["payload_json"].(map[string]any)
		if payload["before_state"] == nil || payload["after_state"] == nil {
			t.Errorf("unified entry payload = %v, want before_state and after_state", payload)
		}
		actions[e["action"].(string)] = true
	}
	if len(entries) != 2 || !actions["placement_changed"] || !actions["fields_changed"] {
		t.Fatalf("unified entries = %v, want placement_changed and fields_changed", entries)
	}

	if resp, unifiedB := get(t, envB, "/v1/admin/audit-log/unified?"+q.Encode()); resp.StatusCode != http.StatusOK ||
		len(unifiedB["entries"].([]any)) != 0 {
		t.Fatalf("tenant B: status %d, entries %v; want none of A's risk rows", resp.StatusCode, unifiedB["entries"])
	}
}

// PatchRisk with a tenant but no credential in context answered 200 and
// recorded the actor as "system" before #1954. It is now a 401 and writes
// nothing: the risk keeps its placement and no audit row appears.
func TestPatchRisk_NoCredentialWritesNoRow_1954(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshPlacementTenant(t, admin)
	env := testServer(t, app, tenant)

	_, created := createPlaced(t, env, nil)
	id := riskField(t, created, "id").(string)

	router := chi.NewRouter()
	router.Patch("/v1/risks/{id}", risks.New(risk.NewStore(app)).PatchRisk)
	ctx, err := tenancy.WithTenant(context.Background(), tenant)
	if err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
	req := httptest.NewRequest(http.MethodPatch, "/v1/risks/"+id, bytes.NewBufferString(`{"level":"company"}`)).WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401: %s", rec.Code, rec.Body.String())
	}

	if n := len(placementAudit(t, app, tenant, id)); n != 0 {
		t.Fatalf("audit rows = %d, want 0", n)
	}
	_, got := get(t, env, "/v1/risks/"+id)
	if riskField(t, got, "level") != "team" {
		t.Fatalf("level = %v, want team (unchanged)", riskField(t, got, "level"))
	}
}
