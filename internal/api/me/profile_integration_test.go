//go:build integration

// Slice 108 — integration tests for /v1/me + /v1/me/preferences + /v1/me/sessions.
// Real Postgres + the assembled platform router so the tests exercise the full
// request path (bearer auth, tenancy middleware, RLS).
//
// Run with:
//
//	go test -tags=integration -race ./internal/api/me/...
//
// Requires DATABASE_URL_APP (atlas_app role) and DATABASE_URL (admin role).
//
// Coverage:
//
//	ISC-26..28  GET /v1/me + PATCH /v1/me happy path + bad time_zone
//	ISC-30      PATCH /v1/me/preferences 400 on unknown event key
//	ISC-32..33  GET /v1/me/sessions own-sessions-only; DELETE /sessions/{id} cross-user 404
//	ISC-38..40  Tenant A cannot see Tenant B preferences / sessions (RLS round-trip)

package me_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api"
	"github.com/mgoodric/security-atlas/internal/api/testjwt"
	"github.com/mgoodric/security-atlas/internal/dbtest"
)

// ----- harness -----

// seedTenantAndUser inserts a fresh tenant + users row and returns the
// (tenantID, userID) pair. Test cleanup deletes both.
func seedTenantAndUser(t *testing.T, admin *pgxpool.Pool, email, displayName string) (string, string) {
	t.Helper()
	tenantID := uuid.NewString()
	userID := uuid.NewString()
	ctx := context.Background()
	if _, err := admin.Exec(ctx, `
		INSERT INTO users (id, tenant_id, email, display_name, status, time_zone)
		VALUES ($1, $2, $3, $4, 'active', '')
	`, userID, tenantID, email, displayName); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	t.Cleanup(func() {
		for _, stmt := range []string{
			`DELETE FROM me_audit_log WHERE tenant_id = $1`,
			`DELETE FROM user_notification_preferences WHERE tenant_id = $1`,
			`DELETE FROM sessions WHERE tenant_id = $1`,
			`DELETE FROM users WHERE tenant_id = $1`,
		} {
			if _, err := admin.Exec(ctx, stmt, tenantID); err != nil {
				t.Logf("cleanup %s: %v", stmt, err)
			}
		}
	})
	return tenantID, userID
}

// seedSession inserts a sessions row for (tenantID, userID) and returns its id.
func seedSession(t *testing.T, admin *pgxpool.Pool, tenantID, userID string) string {
	t.Helper()
	id := uuid.NewString() // any opaque string is fine; the prod path uses base64
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO sessions (id, tenant_id, user_id, expires_at)
		VALUES ($1, $2, $3, now() + interval '7 days')
	`, id, tenantID, userID); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	return id
}

// seedSessionWithMetadata inserts a sessions row populating the slice 162
// augmented columns (user_agent, ip_address, geo_country, geo_city). Used by
// the slice-162 wire-shape assertion.
func seedSessionWithMetadata(t *testing.T, admin *pgxpool.Pool, tenantID, userID, ua, ip, geoCountry, geoCity string) string {
	t.Helper()
	id := uuid.NewString()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO sessions (id, tenant_id, user_id, expires_at, user_agent, ip_address, geo_country, geo_city)
		VALUES ($1, $2, $3, now() + interval '7 days', $4, $5, $6, $7)
	`, id, tenantID, userID, ua, ip, geoCountry, geoCity); err != nil {
		t.Fatalf("seed session with metadata: %v", err)
	}
	return id
}

type testEnv struct {
	server *httptest.Server
	bearer string
}

func testServerForUser(t *testing.T, app *pgxpool.Pool, tenantID, userID string, admin bool) testEnv {
	t.Helper()
	srv := api.New(api.Config{})
	srv.AttachDB(app)
	// Slice 197: JWT bearer via slice 190 path. The jwtmw bridge
	// reads claims.Subject into the synthesized credstore.Credential's
	// UserID, so setting Subject on the claim is the JWT analog of
	// the legacy RebindBearerUserIDForTests hook.
	tenantUUID := uuid.MustParse(tenantID)
	var claims = testjwt.ViewerFor(tenantUUID)
	if admin {
		claims = testjwt.AdminFor(tenantUUID)
	}
	claims.Subject = userID
	bearer := srv.IssueTestJWT(t, claims)
	h := srv.HTTPHandlerForTests()
	if h == nil {
		t.Fatal("HTTPHandlerForTests returned nil — DB pool not attached")
	}
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	return testEnv{server: ts, bearer: bearer}
}

func do(t *testing.T, env testEnv, method, path string, body any) (*http.Response, map[string]any) {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req, _ := http.NewRequest(method, env.server.URL+path, &buf)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+env.bearer)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	var out map[string]any
	if resp.StatusCode != http.StatusNoContent && resp.ContentLength != 0 {
		_ = json.NewDecoder(resp.Body).Decode(&out)
	}
	_ = resp.Body.Close()
	return resp, out
}

// ===== ISC-26: GET /v1/me returns the caller's profile =====

func TestGetMe_HappyPath(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenantID, userID := seedTenantAndUser(t, admin, "alice@example.com", "Alice Example")
	env := testServerForUser(t, app, tenantID, userID, false)

	resp, body := do(t, env, http.MethodGet, "/v1/me", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /v1/me: status %d, want 200; body=%v", resp.StatusCode, body)
	}
	if body["user_id"] != userID {
		t.Errorf("user_id = %v; want %s", body["user_id"], userID)
	}
	if body["display_name"] != "Alice Example" {
		t.Errorf("display_name = %v; want Alice Example", body["display_name"])
	}
	if body["email"] != "alice@example.com" {
		t.Errorf("email = %v; want alice@example.com", body["email"])
	}
	if body["tenant_role"] != "user" {
		t.Errorf("tenant_role = %v; want user", body["tenant_role"])
	}
}

// ===== ISC-27: PATCH /v1/me validates time_zone =====

func TestPatchMe_BadTimeZoneRejected(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenantID, userID := seedTenantAndUser(t, admin, "bob@example.com", "Bob")
	env := testServerForUser(t, app, tenantID, userID, false)

	resp, _ := do(t, env, http.MethodPatch, "/v1/me", map[string]any{
		"time_zone": "Not/A/Real/Zone",
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("PATCH /v1/me bad tz: status %d, want 400", resp.StatusCode)
	}
}

// ===== ISC-28: PATCH /v1/me with valid diff updates + audit =====

func TestPatchMe_UpdatesDisplayNameAndTimeZone(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenantID, userID := seedTenantAndUser(t, admin, "carol@example.com", "Carol Old")
	env := testServerForUser(t, app, tenantID, userID, false)

	resp, body := do(t, env, http.MethodPatch, "/v1/me", map[string]any{
		"display_name": "Carol New",
		"time_zone":    "America/Los_Angeles",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PATCH /v1/me: status %d, want 200; body=%v", resp.StatusCode, body)
	}
	if body["display_name"] != "Carol New" {
		t.Errorf("display_name = %v; want Carol New", body["display_name"])
	}
	if body["time_zone"] != "America/Los_Angeles" {
		t.Errorf("time_zone = %v; want America/Los_Angeles", body["time_zone"])
	}
	// Audit row exists.
	var n int
	if err := admin.QueryRow(context.Background(),
		`SELECT count(*) FROM me_audit_log WHERE tenant_id = $1 AND user_id = $2 AND action = 'profile.update'`,
		tenantID, userID).Scan(&n); err != nil {
		t.Fatalf("count me_audit_log: %v", err)
	}
	if n != 1 {
		t.Errorf("me_audit_log rows = %d; want 1", n)
	}
}

// ===== ISC-A5: empty-diff PATCH skips audit-log write =====

func TestPatchMe_EmptyDiffSkipsAudit(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenantID, userID := seedTenantAndUser(t, admin, "dave@example.com", "Dave")
	env := testServerForUser(t, app, tenantID, userID, false)

	// PATCH with the existing values.
	resp, _ := do(t, env, http.MethodPatch, "/v1/me", map[string]any{
		"display_name": "Dave",
		"time_zone":    "",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PATCH /v1/me empty-diff: status %d, want 200", resp.StatusCode)
	}
	var n int
	_ = admin.QueryRow(context.Background(),
		`SELECT count(*) FROM me_audit_log WHERE tenant_id = $1`, tenantID).Scan(&n)
	if n != 0 {
		t.Errorf("audit rows = %d; want 0 on empty-diff PATCH", n)
	}
}

// ===== ISC-4 / ISC-30: preferences default to all-enabled + 400 on unknown event =====

func TestGetPreferences_DefaultsAllEnabled(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenantID, userID := seedTenantAndUser(t, admin, "ed@example.com", "Ed")
	env := testServerForUser(t, app, tenantID, userID, false)

	resp, body := do(t, env, http.MethodGet, "/v1/me/preferences", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET prefs: status %d, want 200; body=%v", resp.StatusCode, body)
	}
	prefs, ok := body["preferences"].(map[string]any)
	if !ok {
		t.Fatalf("preferences not a map: %v", body)
	}
	for _, ev := range []string{"audit_period_assignment", "policy_ack_due", "risk_review_overdue", "control_drift"} {
		row, ok := prefs[ev].(map[string]any)
		if !ok {
			t.Errorf("missing event %q in prefs: %v", ev, prefs)
			continue
		}
		for _, ch := range []string{"in_app", "email"} {
			if row[ch] != true {
				t.Errorf("default prefs[%s][%s] = %v; want true", ev, ch, row[ch])
			}
		}
	}
}

func TestPatchPreferences_UnknownEvent400(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenantID, userID := seedTenantAndUser(t, admin, "fay@example.com", "Fay")
	env := testServerForUser(t, app, tenantID, userID, false)

	resp, _ := do(t, env, http.MethodPatch, "/v1/me/preferences", map[string]any{
		"made_up_event": map[string]bool{"in_app": false},
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("PATCH prefs unknown event: status %d, want 400", resp.StatusCode)
	}
}

func TestPatchPreferences_MergeSemantic(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenantID, userID := seedTenantAndUser(t, admin, "gigi@example.com", "Gigi")
	env := testServerForUser(t, app, tenantID, userID, false)

	// PATCH only the policy_ack_due/email cell off; everything else stays true.
	resp, body := do(t, env, http.MethodPatch, "/v1/me/preferences", map[string]any{
		"policy_ack_due": map[string]bool{"email": false},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PATCH prefs merge: status %d, want 200; body=%v", resp.StatusCode, body)
	}
	prefs, _ := body["preferences"].(map[string]any)
	pad, _ := prefs["policy_ack_due"].(map[string]any)
	if pad["email"] != false {
		t.Errorf("policy_ack_due/email = %v; want false", pad["email"])
	}
	if pad["in_app"] != true {
		t.Errorf("policy_ack_due/in_app = %v; want true (untouched)", pad["in_app"])
	}
	// And another unrelated event stays default.
	other, _ := prefs["control_drift"].(map[string]any)
	if other["email"] != true {
		t.Errorf("control_drift/email = %v; want true", other["email"])
	}
}

// ===== ISC-32: GET /v1/me/sessions returns the caller's sessions =====

func TestListSessions_OwnSessionsOnly(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenantID, userID := seedTenantAndUser(t, admin, "hank@example.com", "Hank")
	env := testServerForUser(t, app, tenantID, userID, false)

	// Two sessions for this user.
	_ = seedSession(t, admin, tenantID, userID)
	_ = seedSession(t, admin, tenantID, userID)

	// One session for ANOTHER user under the SAME tenant — must not appear.
	otherTenantID, otherUserID := seedTenantAndUser(t, admin, "izzy@example.com", "Izzy")
	_ = otherTenantID
	_, _ = admin.Exec(context.Background(), `
		INSERT INTO users (id, tenant_id, email, display_name, status, time_zone)
		VALUES (gen_random_uuid(), $1, 'jay@example.com', 'Jay', 'active', '')
	`, tenantID)
	// otherUser in same tenant just to have a sibling session
	siblingUserID := uuid.NewString()
	_, _ = admin.Exec(context.Background(), `
		INSERT INTO users (id, tenant_id, email, display_name, status, time_zone)
		VALUES ($1, $2, 'sibling@example.com', 'Sibling', 'active', '')
	`, siblingUserID, tenantID)
	_ = seedSession(t, admin, tenantID, siblingUserID)
	_ = otherUserID // referenced to silence linter; cleanup handles it

	resp, body := do(t, env, http.MethodGet, "/v1/me/sessions", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET sessions: status %d, want 200; body=%v", resp.StatusCode, body)
	}
	rows, _ := body["sessions"].([]any)
	if len(rows) != 2 {
		t.Errorf("expected 2 sessions for caller; got %d", len(rows))
	}
}

// ===== ISC-33: DELETE /v1/me/sessions/{id} cross-user => 404 =====

func TestRevokeSession_CrossUser404(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenantID, userID := seedTenantAndUser(t, admin, "kara@example.com", "Kara")
	env := testServerForUser(t, app, tenantID, userID, false)

	// Sibling user in the SAME tenant with their own session.
	siblingUserID := uuid.NewString()
	_, _ = admin.Exec(context.Background(), `
		INSERT INTO users (id, tenant_id, email, display_name, status, time_zone)
		VALUES ($1, $2, 'sib@example.com', 'Sib', 'active', '')
	`, siblingUserID, tenantID)
	siblingSession := seedSession(t, admin, tenantID, siblingUserID)

	resp, _ := do(t, env, http.MethodDelete, "/v1/me/sessions/"+siblingSession, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("DELETE cross-user session: status %d, want 404 (existence oracle guard)", resp.StatusCode)
	}
	// Verify the sibling's session is STILL valid.
	var revokedAt *time.Time
	if err := admin.QueryRow(context.Background(),
		`SELECT revoked_at FROM sessions WHERE id = $1`, siblingSession).Scan(&revokedAt); err != nil {
		t.Fatalf("scan sibling session: %v", err)
	}
	if revokedAt != nil {
		t.Error("sibling session was revoked across user boundary — cross-user revoke leaked")
	}
}

// ===== ISC-38 / ISC-39: RLS cross-tenant isolation =====

func TestRLS_TenantACannotSeeTenantBPreferences(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)

	tenantA, userA := seedTenantAndUser(t, admin, "tenantA-user@example.com", "TA User")
	envA := testServerForUser(t, app, tenantA, userA, false)

	tenantB, userB := seedTenantAndUser(t, admin, "tenantB-user@example.com", "TB User")
	envB := testServerForUser(t, app, tenantB, userB, false)

	// Tenant B sets a non-default preference.
	resp, _ := do(t, envB, http.MethodPatch, "/v1/me/preferences", map[string]any{
		"audit_period_assignment": map[string]bool{"email": false},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("envB PATCH: status %d", resp.StatusCode)
	}

	// Tenant A reads — must see all-defaults (no leakage of B's setting).
	resp, body := do(t, envA, http.MethodGet, "/v1/me/preferences", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("envA GET: status %d", resp.StatusCode)
	}
	prefs, _ := body["preferences"].(map[string]any)
	apa, _ := prefs["audit_period_assignment"].(map[string]any)
	if apa["email"] != true {
		t.Errorf("RLS leak: tenant A sees email=%v for audit_period_assignment; want true (default)", apa["email"])
	}
}

func TestRLS_TenantACannotListTenantBSessions(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)

	tenantA, userA := seedTenantAndUser(t, admin, "rls-a@example.com", "A")
	envA := testServerForUser(t, app, tenantA, userA, false)

	tenantB, userB := seedTenantAndUser(t, admin, "rls-b@example.com", "B")
	_ = seedSession(t, admin, tenantB, userB)

	// Tenant A under their own tenant calls /v1/me/sessions — should see 0 rows
	// (no sessions exist for A's user).
	resp, body := do(t, envA, http.MethodGet, "/v1/me/sessions", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("envA GET sessions: status %d", resp.StatusCode)
	}
	rows, _ := body["sessions"].([]any)
	if len(rows) != 0 {
		t.Errorf("RLS leak: tenant A sees %d sessions; want 0", len(rows))
	}
}

// ===== Slice 130 — `roles` field on GET /v1/me =====
//
// AC-2: the handler returns the caller's user_roles list under
// `tenancy.ApplyTenant`. AC-6: cross-tenant isolation — Tenant A's caller
// cannot see Tenant B's roles. P0-A1: roles come from user_roles (not
// fabricated from cred.IsAdmin or cred.OwnerRoles). P0-A2: existing
// `is_admin` field continues to render.

// seedUserRoles is the slice-130 helper that inserts one or more roles
// for (tenantID, userID) under the tenant GUC. Matches the slice-027 +
// slice-035 INSERT shape exactly (TEXT user_id, granted_by NOT NULL).
func seedUserRoles(t *testing.T, admin *pgxpool.Pool, tenantID, userID string, roles ...string) {
	t.Helper()
	ctx := context.Background()
	for _, r := range roles {
		if _, err := admin.Exec(ctx,
			`INSERT INTO user_roles (tenant_id, user_id, role, granted_by)
			 VALUES ($1, $2, $3, 'slice-130-test')
			 ON CONFLICT DO NOTHING`,
			tenantID, userID, r,
		); err != nil {
			t.Fatalf("seedUserRoles %q for %s: %v", r, userID, err)
		}
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(ctx, `DELETE FROM user_roles WHERE tenant_id = $1`, tenantID)
	})
}

// TestGetMe_ReturnsRolesList — AC-2 happy path. The caller holds
// auditor + grc_engineer; both roles flow through to the wire response.
// The existing `is_admin` field continues to render (P0-A2 additive).
func TestGetMe_ReturnsRolesList(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenantID, userID := seedTenantAndUser(t, admin, "auditor130@example.com", "Auditor 130")
	seedUserRoles(t, admin, tenantID, userID, "auditor", "grc_engineer")

	// Non-admin caller — `is_admin` should be false, `roles` should carry both grants.
	env := testServerForUser(t, app, tenantID, userID, false)
	resp, body := do(t, env, http.MethodGet, "/v1/me", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /v1/me: status %d, want 200; body=%v", resp.StatusCode, body)
	}
	// is_admin still renders (P0-A2 additive).
	if v, ok := body["is_admin"].(bool); !ok || v {
		t.Errorf("is_admin = %v (%T); want false (boolean)", body["is_admin"], body["is_admin"])
	}
	rolesRaw, ok := body["roles"].([]any)
	if !ok {
		t.Fatalf("roles field missing or wrong type: %T %v", body["roles"], body["roles"])
	}
	got := map[string]bool{}
	for _, r := range rolesRaw {
		s, _ := r.(string)
		got[s] = true
	}
	if !got["auditor"] || !got["grc_engineer"] {
		t.Errorf("roles=%v; want both auditor and grc_engineer", rolesRaw)
	}
}

// TestGetMe_EmptyRolesForNoUserRolesRow — caller with no user_roles row
// sees an empty `roles: []` (not null, not missing). Fail-closed posture
// (P0-A3): the BFF + frontend can rely on the field always being present.
func TestGetMe_EmptyRolesForNoUserRolesRow(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenantID, userID := seedTenantAndUser(t, admin, "no-roles130@example.com", "No Roles")
	// Deliberately NO seedUserRoles call.
	env := testServerForUser(t, app, tenantID, userID, false)

	resp, body := do(t, env, http.MethodGet, "/v1/me", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /v1/me: status %d, want 200; body=%v", resp.StatusCode, body)
	}
	rolesRaw, ok := body["roles"].([]any)
	if !ok {
		t.Fatalf("roles field missing or wrong type (must be present, even when empty): %T %v",
			body["roles"], body["roles"])
	}
	if len(rolesRaw) != 0 {
		t.Errorf("roles=%v; want []", rolesRaw)
	}
}

// TestGetMe_RolesCrossTenantIsolation — AC-6. Tenant A's caller cannot see
// Tenant B's roles. Both tenants are seeded with the same role name
// ("auditor") under DIFFERENT user_ids so the test catches both
// (i) bleed via the tenant GUC and (ii) bleed via shared user_id collision.
func TestGetMe_RolesCrossTenantIsolation(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)

	tenantA, userA := seedTenantAndUser(t, admin, "tenA130@example.com", "TA")
	seedUserRoles(t, admin, tenantA, userA, "auditor")

	tenantB, userB := seedTenantAndUser(t, admin, "tenB130@example.com", "TB")
	seedUserRoles(t, admin, tenantB, userB, "grc_engineer", "control_owner")

	envA := testServerForUser(t, app, tenantA, userA, false)
	envB := testServerForUser(t, app, tenantB, userB, false)

	// Tenant A: must see ONLY "auditor".
	respA, bodyA := do(t, envA, http.MethodGet, "/v1/me", nil)
	if respA.StatusCode != http.StatusOK {
		t.Fatalf("envA GET: status %d", respA.StatusCode)
	}
	rolesA, _ := bodyA["roles"].([]any)
	if len(rolesA) != 1 || rolesA[0] != "auditor" {
		t.Errorf("tenant A roles=%v; want exactly [auditor]", rolesA)
	}

	// Tenant B: must see ONLY grc_engineer + control_owner (no leak of A's).
	respB, bodyB := do(t, envB, http.MethodGet, "/v1/me", nil)
	if respB.StatusCode != http.StatusOK {
		t.Fatalf("envB GET: status %d", respB.StatusCode)
	}
	rolesB, _ := bodyB["roles"].([]any)
	if len(rolesB) != 2 {
		t.Fatalf("tenant B roles=%v; want exactly [grc_engineer control_owner] (any order)", rolesB)
	}
	seen := map[string]bool{}
	for _, r := range rolesB {
		s, _ := r.(string)
		seen[s] = true
	}
	if !seen["grc_engineer"] || !seen["control_owner"] {
		t.Errorf("tenant B roles=%v; want [grc_engineer control_owner]", rolesB)
	}
	if seen["auditor"] {
		t.Errorf("RLS leak: tenant B sees A's 'auditor' role: %v", rolesB)
	}
}

// TestGetMe_AdminCallerAlsoCarriesRoles — an admin caller (cred.IsAdmin=true)
// who ALSO has explicit user_roles rows sees BOTH the is_admin flag AND the
// roles list. The two are independent surfaces — admin via credential flag,
// roles via user_roles table.
func TestGetMe_AdminCallerAlsoCarriesRoles(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenantID, userID := seedTenantAndUser(t, admin, "admin130@example.com", "Admin 130")
	seedUserRoles(t, admin, tenantID, userID, "admin", "auditor")
	env := testServerForUser(t, app, tenantID, userID, true)

	resp, body := do(t, env, http.MethodGet, "/v1/me", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /v1/me: status %d, want 200", resp.StatusCode)
	}
	if v, ok := body["is_admin"].(bool); !ok || !v {
		t.Errorf("is_admin = %v; want true", body["is_admin"])
	}
	rolesRaw, _ := body["roles"].([]any)
	got := map[string]bool{}
	for _, r := range rolesRaw {
		s, _ := r.(string)
		got[s] = true
	}
	if !got["admin"] || !got["auditor"] {
		t.Errorf("roles=%v; want both admin and auditor present", rolesRaw)
	}
}

// ===== Slice 162 — UA / IP / geo on the /v1/me/sessions wire shape =====
//
// AC-3: GET /v1/me/sessions surfaces user_agent + ip_address + geo_country +
// geo_city when the underlying sessions row carries them. AC-A1 / P0-162-1:
// rows that DON'T carry the columns render with the fields omitted, not
// populated with a placeholder.

// TestListSessions_AugmentedFieldsOnWire — a session row that was created with
// UA/IP/geo populated surfaces all four fields on the JSON response.
func TestListSessions_AugmentedFieldsOnWire(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenantID, userID := seedTenantAndUser(t, admin, "ua162@example.com", "UA Tester")
	env := testServerForUser(t, app, tenantID, userID, false)

	// One session WITH metadata, one WITHOUT — proves both shapes render correctly.
	_ = seedSessionWithMetadata(t, admin, tenantID, userID,
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 Safari/605.1.15",
		"192.0.2.18",
		"US",
		"San Francisco",
	)
	_ = seedSession(t, admin, tenantID, userID)

	resp, body := do(t, env, http.MethodGet, "/v1/me/sessions", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /v1/me/sessions: status %d, want 200; body=%v", resp.StatusCode, body)
	}
	rows, _ := body["sessions"].([]any)
	if len(rows) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(rows))
	}

	var withUA, withoutUA map[string]any
	for _, r := range rows {
		m, _ := r.(map[string]any)
		if _, hasUA := m["user_agent"]; hasUA {
			withUA = m
		} else {
			withoutUA = m
		}
	}
	if withUA == nil || withoutUA == nil {
		t.Fatalf("expected one row with user_agent and one without; got rows=%v", rows)
	}

	// AC-3 happy path: the augmented row carries all four fields.
	if got := withUA["user_agent"]; got != "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 Safari/605.1.15" {
		t.Errorf("user_agent on wire = %v; want Safari UA string", got)
	}
	if got := withUA["ip_address"]; got != "192.0.2.18" {
		t.Errorf("ip_address on wire = %v; want 192.0.2.18", got)
	}
	if got := withUA["geo_country"]; got != "US" {
		t.Errorf("geo_country on wire = %v; want US", got)
	}
	if got := withUA["geo_city"]; got != "San Francisco" {
		t.Errorf("geo_city on wire = %v; want San Francisco", got)
	}

	// P0-162-1: the row WITHOUT metadata has the four fields OMITTED from the
	// JSON object (not present-with-placeholder). omitempty on the wire shape
	// produces this — the frontend session-line helper treats missing identically
	// to empty and renders the row honestly.
	for _, key := range []string{"user_agent", "ip_address", "geo_country", "geo_city"} {
		if v, present := withoutUA[key]; present {
			t.Errorf("row without metadata leaked %q=%v on wire; want field omitted (P0-162-1)", key, v)
		}
	}
}
