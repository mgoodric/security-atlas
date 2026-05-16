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
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api"
)

// ----- harness -----

func appDSN(t *testing.T) string {
	t.Helper()
	v := os.Getenv("DATABASE_URL_APP")
	if v == "" {
		t.Skip("DATABASE_URL_APP not set; skipping integration test")
	}
	return v
}

func adminDSN(t *testing.T) string {
	t.Helper()
	v := os.Getenv("DATABASE_URL")
	if v == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}
	return v
}

func openPool(t *testing.T, dsn string) *pgxpool.Pool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(func() { pool.Close() })
	return pool
}

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

type testEnv struct {
	server *httptest.Server
	bearer string
}

func testServerForUser(t *testing.T, app *pgxpool.Pool, tenantID, userID string, admin bool) testEnv {
	t.Helper()
	srv := api.New(api.Config{})
	srv.AttachDB(app)
	var bearer string
	var err error
	if admin {
		_, bearer, err = srv.IssueBootstrapAdminCredential(tenantID)
	} else {
		_, bearer, err = srv.IssueBootstrapCredential(tenantID)
	}
	if err != nil {
		t.Fatalf("IssueBootstrap*Credential: %v", err)
	}
	if err := srv.RebindBearerUserIDForTests(bearer, userID); err != nil {
		t.Fatalf("RebindBearerUserIDForTests: %v", err)
	}
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
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
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
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
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
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
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
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
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
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
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
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
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
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
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
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
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
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
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
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))

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
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))

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
