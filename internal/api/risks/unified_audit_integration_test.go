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
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api"
	"github.com/mgoodric/security-atlas/internal/api/risks"
	"github.com/mgoodric/security-atlas/internal/api/testjwt"
	"github.com/mgoodric/security-atlas/internal/auth/jwt"
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
		if _, leaked := payload["before_state"]; leaked {
			t.Errorf("unified entry payload carries before_state: %v", payload)
		}
		if _, leaked := payload["after_state"]; leaked {
			t.Errorf("unified entry payload carries after_state: %v", payload)
		}
		changed := fmt.Sprint(payload["changed_fields"])
		switch e["action"] {
		case "placement_changed":
			if changed != "[level]" {
				t.Errorf("placement_changed changed_fields = %s, want [level]", changed)
			}
		case "fields_changed":
			if changed != "[title]" {
				t.Errorf("fields_changed changed_fields = %s, want [title]", changed)
			}
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

// risk_audit_log has no FK to risks and the unified branch joins nothing,
// so a deleted risk's rows stay in the unified log with their envelope.
func TestUnifiedAuditLog_DeletedRiskKeepsRows_1954(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshPlacementTenant(t, admin)
	if _, err := admin.Exec(context.Background(),
		`INSERT INTO tenants (id, name) VALUES ($1, $2)`, tenant, "risk-deleted-1954-"+tenant[:8]); err != nil {
		t.Fatalf("seed tenants row: %v", err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), `DELETE FROM tenants WHERE id = $1`, tenant)
	})
	env := adminServer(t, app, tenant)

	_, created := createPlaced(t, env, nil)
	id := riskField(t, created, "id").(string)
	if resp, body := patch(t, env, "/v1/risks/"+id, `{"level":"org"}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("PATCH: status %d (%v)", resp.StatusCode, body)
	}
	req, _ := http.NewRequest(http.MethodDelete, env.server.URL+"/v1/risks/"+id, nil)
	req.Header.Set("Authorization", "Bearer "+env.bearer)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE: status %d, want 204", resp.StatusCode)
	}

	now := time.Now().UTC()
	q := url.Values{
		"from": {now.Add(-time.Hour).Format(time.RFC3339)},
		"to":   {now.Add(time.Hour).Format(time.RFC3339)},
		"kind": {"risk"},
	}
	r, unified := get(t, env, "/v1/admin/audit-log/unified?"+q.Encode())
	if r.StatusCode != http.StatusOK {
		t.Fatalf("unified audit log: status %d (%v)", r.StatusCode, unified)
	}
	entries, _ := unified["entries"].([]any)
	if len(entries) != 1 {
		t.Fatalf("entries = %v, want the deleted risk's placement row", entries)
	}
	e := entries[0].(map[string]any)
	if e["target_id"] != id || e["action"] != "placement_changed" || e["actor_id"] == "" {
		t.Fatalf("entry = %v", e)
	}
}

// rawGet issues an authenticated GET and returns the status and raw body.
func rawGet(t *testing.T, baseURL, path, bearer string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, baseURL+path, nil)
	req.Header.Set("Authorization", "Bearer "+bearer)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return resp.StatusCode, string(body)
}

// roleUser seeds a user holding role in the tenant and returns claims for
// it. The unified log's role probe reads user_roles, so a role only on the
// token is not enough. The rows are removed when the test ends.
func roleUser(t *testing.T, admin *pgxpool.Pool, tenant uuid.UUID, role string) jwt.AtlasClaims {
	t.Helper()
	ctx := context.Background()
	userID := uuid.New()
	if _, err := admin.Exec(ctx, `
		INSERT INTO users (id, tenant_id, email, display_name, status, time_zone)
		VALUES ($1, $2, $3, $4, 'active', '')`,
		userID, tenant, role+"-"+userID.String()[:8]+"@example.com", role); err != nil {
		t.Fatalf("seed %s user: %v", role, err)
	}
	if _, err := admin.Exec(ctx,
		`INSERT INTO user_roles (tenant_id, user_id, role, granted_by) VALUES ($1, $2, $3, 'test-1954')`,
		tenant, userID, role); err != nil {
		t.Fatalf("seed %s role: %v", role, err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(ctx, `DELETE FROM user_roles WHERE tenant_id = $1 AND user_id = $2`, tenant, userID)
		_, _ = admin.Exec(ctx, `DELETE FROM users WHERE tenant_id = $1 AND id = $2`, tenant, userID)
	})
	claims := testjwt.OwnerFor(tenant, []string{role})
	claims.Subject = userID.String()
	return claims
}

// Auditors and control owners read the unified log, its export and
// Activity, but OPA denies them /v1/risks. A risk row shows them that a
// risk changed and which fields, never the values (#1954 review).
func TestUnifiedAuditLog_RiskRowsCarryNoFieldValues_1954(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshPlacementTenant(t, admin)
	if _, err := admin.Exec(context.Background(),
		`INSERT INTO tenants (id, name) VALUES ($1, $2)`, tenant, "risk-values-1954-"+tenant[:8]); err != nil {
		t.Fatalf("seed tenants row: %v", err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), `DELETE FROM tenants WHERE id = $1`, tenant)
	})

	srv := api.New(api.Config{})
	srv.AttachDB(app)
	tid := uuid.MustParse(tenant)
	adminBearer := srv.IssueTestJWT(t, testjwt.AdminFor(tid))
	auditor := srv.IssueTestJWT(t, roleUser(t, admin, tid, "auditor"))
	controlOwner := srv.IssueTestJWT(t, roleUser(t, admin, tid, "control_owner"))
	ts := httptest.NewServer(srv.HTTPHandlerForTests())
	t.Cleanup(ts.Close)
	env := testEnv{server: ts, bearer: adminBearer}

	// The record values to look for. Each is distinctive enough that a
	// match in a response can only be the leaked value.
	secrets := []string{"secret-title-1954", "secret-description-1954", "secret-owner-1954"}
	_, created := createPlaced(t, env, nil)
	id := riskField(t, created, "id").(string)
	if resp, body := patch(t, env, "/v1/risks/"+id,
		`{"title":"`+secrets[0]+`","description":"`+secrets[1]+`","treatment_owner":"`+secrets[2]+`","level":"org"}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("PATCH: status %d (%v)", resp.StatusCode, body)
	}

	// OPA, which denies these roles /v1/risks in production
	// (policies/authz), is not wired in test servers, so that denial is
	// not asserted here. What is asserted is that the audit surfaces
	// these roles can read never carry the values.

	now := time.Now().UTC()
	window := "from=" + url.QueryEscape(now.Add(-time.Hour).Format(time.RFC3339)) +
		"&to=" + url.QueryEscape(now.Add(time.Hour).Format(time.RFC3339)) + "&kind=risk"
	cases := []struct {
		name, bearer, path string
	}{
		{"auditor unified", auditor, "/v1/admin/audit-log/unified?" + window},
		{"auditor export json", auditor, "/v1/admin/audit-log/export?format=json&" + window},
		{"auditor export csv", auditor, "/v1/admin/audit-log/export?format=csv&" + window},
		{"control_owner activity", controlOwner, "/v1/activity/unified?" + window},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, body := rawGet(t, ts.URL, tc.path, tc.bearer)
			if status != http.StatusOK {
				t.Fatalf("status %d, want 200: %s", status, body)
			}
			// Not vacuous: the risk rows are in the response.
			if !strings.Contains(body, id) || !strings.Contains(body, "fields_changed") {
				t.Fatalf("response has no risk rows for %s: %s", id, body)
			}
			for _, s := range secrets {
				if strings.Contains(body, s) {
					t.Errorf("response carries the risk value %q: %s", s, body)
				}
			}
			for _, key := range []string{"before_state", "after_state"} {
				if strings.Contains(body, key) {
					t.Errorf("response carries %s: %s", key, body)
				}
			}
		})
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
