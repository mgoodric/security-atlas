//go:build integration

// #2145 — first-visit onboarding hints are stored in, and cleared from, the
// caller's preferences (GET/PATCH /v1/me/preferences), keyed per user.

package me_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api"
	"github.com/mgoodric/security-atlas/internal/api/testjwt"
	"github.com/mgoodric/security-atlas/internal/dbtest"
)

// hintEnv serves the router as a signed-in person whose JWT subject carries
// the "user:" prefix (#2373), the shape a real login mints.
func hintEnv(t *testing.T, app *pgxpool.Pool, tenantID, userID string) testEnv {
	t.Helper()
	srv := api.New(api.Config{})
	srv.AttachDB(app)
	claims := testjwt.ViewerFor(uuid.MustParse(tenantID))
	claims.Subject = "user:" + userID
	// Issue before building the handler: the handler captures the signing key.
	bearer := srv.IssueTestJWT(t, claims)
	ts := httptest.NewServer(srv.HTTPHandlerForTests())
	t.Cleanup(ts.Close)
	return testEnv{server: ts, bearer: bearer}
}

func dismissedHints(t *testing.T, body map[string]any) []string {
	t.Helper()
	raw, ok := body["dismissed_hints"].([]any)
	if !ok {
		t.Fatalf("dismissed_hints not a list: %v", body)
	}
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		out = append(out, v.(string))
	}
	return out
}

func storedDismissals(t *testing.T, admin *pgxpool.Pool, tenantID, userID string) int {
	t.Helper()
	var n int
	if err := admin.QueryRow(context.Background(),
		`SELECT count(*) FROM user_onboarding_hint_dismissals WHERE tenant_id = $1 AND user_id = $2`,
		tenantID, userID).Scan(&n); err != nil {
		t.Fatalf("count dismissals: %v", err)
	}
	return n
}

func hintAuditRows(t *testing.T, admin *pgxpool.Pool, tenantID string) int {
	t.Helper()
	var n int
	if err := admin.QueryRow(context.Background(),
		`SELECT count(*) FROM me_audit_log WHERE tenant_id = $1 AND action = 'preferences.update'`,
		tenantID).Scan(&n); err != nil {
		t.Fatalf("count audit: %v", err)
	}
	return n
}

func TestHints_DismissStoredThenResetClears(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenantID, userID := seedTenantAndUser(t, admin, "hana@example.com", "Hana")
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), `DELETE FROM user_onboarding_hint_dismissals WHERE tenant_id = $1`, tenantID)
	})
	env := hintEnv(t, app, tenantID, userID)

	resp, body := do(t, env, http.MethodGet, "/v1/me/preferences", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET: status %d; body=%v", resp.StatusCode, body)
	}
	if got := dismissedHints(t, body); len(got) != 0 {
		t.Fatalf("fresh user dismissed_hints = %v; want []", got)
	}

	resp, body = do(t, env, http.MethodPatch, "/v1/me/preferences", map[string]any{
		"dismissed_hints": map[string]bool{"controls": true},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PATCH dismiss: status %d; body=%v", resp.StatusCode, body)
	}
	if got := dismissedHints(t, body); len(got) != 1 || got[0] != "controls" {
		t.Fatalf("after dismiss dismissed_hints = %v; want [controls]", got)
	}
	if n := storedDismissals(t, admin, tenantID, userID); n != 1 {
		t.Fatalf("stored rows after dismiss = %d; want 1", n)
	}
	// Dismissing again is a no-op and writes no second audit row.
	do(t, env, http.MethodPatch, "/v1/me/preferences", map[string]any{
		"dismissed_hints": map[string]bool{"controls": true},
	})
	if n := hintAuditRows(t, admin, tenantID); n != 1 {
		t.Fatalf("audit rows after a repeat dismiss = %d; want 1", n)
	}

	// The notification matrix is untouched by a hint-only PATCH.
	_, body = do(t, env, http.MethodGet, "/v1/me/preferences", nil)
	prefs, _ := body["preferences"].(map[string]any)
	if row, _ := prefs["control_drift"].(map[string]any); row["email"] != true {
		t.Errorf("control_drift/email = %v; want true", row["email"])
	}

	resp, body = do(t, env, http.MethodPatch, "/v1/me/preferences", map[string]any{"reset_hints": true})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PATCH reset: status %d; body=%v", resp.StatusCode, body)
	}
	if got := dismissedHints(t, body); len(got) != 0 {
		t.Fatalf("after reset dismissed_hints = %v; want []", got)
	}
	if n := storedDismissals(t, admin, tenantID, userID); n != 0 {
		t.Fatalf("stored rows after reset = %d; want 0", n)
	}
	if n := hintAuditRows(t, admin, tenantID); n != 2 {
		t.Fatalf("audit rows after reset = %d; want 2", n)
	}
}

func TestHints_UnknownModule400WritesNothing(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenantID, userID := seedTenantAndUser(t, admin, "ivo@example.com", "Ivo")
	env := hintEnv(t, app, tenantID, userID)

	resp, _ := do(t, env, http.MethodPatch, "/v1/me/preferences", map[string]any{
		"dismissed_hints": map[string]bool{"controls": true, "not_a_module": true},
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("PATCH unknown module: status %d; want 400", resp.StatusCode)
	}
	if n := storedDismissals(t, admin, tenantID, userID); n != 0 {
		t.Fatalf("stored rows after a rejected PATCH = %d; want 0", n)
	}
}

func TestHints_PerUser(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenantID, alice := seedTenantAndUser(t, admin, "jo@example.com", "Jo")
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), `DELETE FROM user_onboarding_hint_dismissals WHERE tenant_id = $1`, tenantID)
	})
	bob := uuid.NewString()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO users (id, tenant_id, email, display_name, status, time_zone)
		VALUES ($1, $2, 'kai@example.com', 'Kai', 'active', '')`, bob, tenantID); err != nil {
		t.Fatalf("seed second user: %v", err)
	}

	do(t, hintEnv(t, app, tenantID, alice), http.MethodPatch, "/v1/me/preferences", map[string]any{
		"dismissed_hints": map[string]bool{"controls": true},
	})
	_, body := do(t, hintEnv(t, app, tenantID, bob), http.MethodGet, "/v1/me/preferences", nil)
	if got := dismissedHints(t, body); len(got) != 0 {
		t.Fatalf("another user in the same tenant sees dismissed_hints = %v; want []", got)
	}
}
