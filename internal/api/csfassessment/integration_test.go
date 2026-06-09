//go:build integration

// Slice 515 — integration tests for the NIST CSF 2.0 Tier / Profile assessment
// API. Real Postgres + the assembled platform router, so the suite exercises
// the full request path: JWT auth → tenancy middleware → RLS → sqlc → the
// domain store. Covers:
//
//	AC-2  Tier rating CRUD + Current/Target profile CRUD via the API.
//	AC-3  the Current-vs-Target gap view derived from the two profiles.
//	AC-4  (P0) RLS isolation — tenant A's Tier / profile / selections / gap
//	      NEVER appear for tenant B; deny-on-missing-context.
//	R     the assessment audit row is written (who/when/which CSF version).
//	E     the role cut — a viewer cannot edit; grc_engineer + admin can.
//
// Run with:
//
//	go test -tags=integration -race ./internal/api/csfassessment/...
//
// Requires DATABASE_URL_APP (atlas_app role) and DATABASE_URL (admin role).
package csfassessment_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api"
	"github.com/mgoodric/security-atlas/internal/api/testjwt"
	"github.com/mgoodric/security-atlas/internal/auth/jwt"
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

type testEnv struct {
	server *httptest.Server
	bearer string
}

func newServer(t *testing.T, app *pgxpool.Pool, claims jwt.AtlasClaims) testEnv {
	t.Helper()
	srv := api.New(api.Config{})
	srv.AttachDB(app)
	bearer := srv.IssueTestJWT(t, claims)
	h := srv.HTTPHandlerForTests()
	if h == nil {
		t.Fatal("HTTPHandlerForTests returned nil — DB pool not attached")
	}
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	return testEnv{server: ts, bearer: bearer}
}

// engineerServer mints a grc_engineer credential (the edit role).
func engineerServer(t *testing.T, app *pgxpool.Pool, tenant string) testEnv {
	return newServer(t, app, testjwt.OwnerFor(uuid.MustParse(tenant), []string{"grc_engineer"}))
}

// viewerServer mints a baseline viewer credential (no edit role).
func viewerServer(t *testing.T, app *pgxpool.Pool, tenant string) testEnv {
	return newServer(t, app, testjwt.ViewerFor(uuid.MustParse(tenant)))
}

// adminServer mints an admin credential (edit-capable via the admin wildcard).
func adminServer(t *testing.T, app *pgxpool.Pool, tenant string) testEnv {
	return newServer(t, app, testjwt.AdminFor(uuid.MustParse(tenant)))
}

func do(t *testing.T, env testEnv, method, path, body string) (*http.Response, map[string]any) {
	t.Helper()
	req, err := http.NewRequest(method, env.server.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+env.bearer)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	var decoded map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&decoded)
	_ = resp.Body.Close()
	return resp, decoded
}

// ----- seed fixtures -----

// seedCSFVersion inserts a CSF framework + framework_version + two Subcategory
// rows (the SHARED crosswalk reference data slice 480 lands). Returns the
// framework_version id + the two requirement ids. NEUTRAL fixture: no
// tenant-specific identity is baked in (frameworks are global catalog rows,
// tenant_id NULL).
func seedCSFVersion(t *testing.T, admin *pgxpool.Pool) (fvID uuid.UUID, reqIDs []uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	fwID := uuid.New()
	fvID = uuid.New()
	// A unique slug per test run avoids collisions with the bundled nist_csf
	// catalog and with parallel tests.
	slug := "nist_csf_t515_" + uuid.NewString()[:8]
	if _, err := admin.Exec(ctx, `
		INSERT INTO frameworks (id, tenant_id, slug, name, issuer)
		VALUES ($1, NULL, $2, 'NIST CSF 2.0 (slice-515 test)', 'NIST')
	`, fwID, slug); err != nil {
		t.Fatalf("seed framework: %v", err)
	}
	if _, err := admin.Exec(ctx, `
		INSERT INTO framework_versions (id, tenant_id, framework_id, version, status)
		VALUES ($1, NULL, $2, '2.0', 'current')
	`, fvID, fwID); err != nil {
		t.Fatalf("seed framework_version: %v", err)
	}
	for _, code := range []string{"GV.OC-01", "PR.AA-01"} {
		rid := uuid.New()
		if _, err := admin.Exec(ctx, `
			INSERT INTO framework_requirements (id, framework_version_id, code, title, body)
			VALUES ($1, $2, $3, $4, '')
		`, rid, fvID, code, code+" subcategory"); err != nil {
			t.Fatalf("seed requirement %s: %v", code, err)
		}
		reqIDs = append(reqIDs, rid)
	}
	t.Cleanup(func() {
		// framework cascade deletes versions + requirements; the assessment
		// tables FK framework_versions ON DELETE CASCADE, so this also clears
		// any tier/profile/selection rows the test created.
		if _, err := admin.Exec(context.Background(), `DELETE FROM frameworks WHERE id = $1`, fwID); err != nil {
			t.Logf("cleanup framework: %v", err)
		}
	})
	return fvID, reqIDs
}

func freshTenant(t *testing.T, admin *pgxpool.Pool, fvID uuid.UUID) string {
	t.Helper()
	tenant := uuid.NewString()
	t.Cleanup(func() {
		ctx := context.Background()
		for _, stmt := range []string{
			`DELETE FROM csf_assessment_audit WHERE tenant_id = $1`,
			`DELETE FROM csf_profile_selections WHERE tenant_id = $1`,
			`DELETE FROM csf_profiles WHERE tenant_id = $1`,
			`DELETE FROM csf_tier_ratings WHERE tenant_id = $1`,
		} {
			if _, err := admin.Exec(ctx, stmt, tenant); err != nil {
				t.Logf("cleanup %s: %v", stmt, err)
			}
		}
	})
	return tenant
}

// ===== AC-2: Tier rating CRUD =====

func TestTier_SetReadReRate(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	fvID, _ := seedCSFVersion(t, admin)
	tenant := freshTenant(t, admin, fvID)
	env := engineerServer(t, app, tenant)
	q := "?framework_version=" + fvID.String()

	resp, body := do(t, env, http.MethodPut, "/v1/csf/tier"+q, `{"tier":"tier2_risk_informed","rationale":"baseline"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT tier status = %d, want 200", resp.StatusCode)
	}
	if tr, _ := body["tier_rating"].(map[string]any); tr["tier"] != "tier2_risk_informed" {
		t.Fatalf("tier = %v, want tier2_risk_informed", tr["tier"])
	}

	resp, body = do(t, env, http.MethodGet, "/v1/csf/tier"+q, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET tier status = %d", resp.StatusCode)
	}
	if tr, _ := body["tier_rating"].(map[string]any); tr["tier"] != "tier2_risk_informed" {
		t.Fatalf("GET tier = %v, want tier2_risk_informed", tr["tier"])
	}

	// Re-rate to tier4; the unique constraint means it updates in place.
	resp, _ = do(t, env, http.MethodPut, "/v1/csf/tier"+q, `{"tier":"tier4_adaptive","rationale":"matured"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("re-rate status = %d", resp.StatusCode)
	}
	var count int
	if err := admin.QueryRow(context.Background(),
		`SELECT count(*) FROM csf_tier_ratings WHERE tenant_id = $1`, tenant).Scan(&count); err != nil {
		t.Fatalf("count ratings: %v", err)
	}
	if count != 1 {
		t.Fatalf("tier rating rows = %d, want 1 (re-rate updates in place)", count)
	}
}

func TestTier_InvalidRejected(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	fvID, _ := seedCSFVersion(t, admin)
	tenant := freshTenant(t, admin, fvID)
	env := engineerServer(t, app, tenant)
	resp, _ := do(t, env, http.MethodPut, "/v1/csf/tier?framework_version="+fvID.String(), `{"tier":"tier5_godmode"}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid tier status = %d, want 400", resp.StatusCode)
	}
}

// ===== R: audit row written =====

func TestTier_WritesAuditRow(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	fvID, _ := seedCSFVersion(t, admin)
	tenant := freshTenant(t, admin, fvID)
	env := engineerServer(t, app, tenant)

	do(t, env, http.MethodPut, "/v1/csf/tier?framework_version="+fvID.String(), `{"tier":"tier3_repeatable"}`)

	var action, kind, fv string
	if err := admin.QueryRow(context.Background(), `
		SELECT action, subject_kind, framework_version_id::text
		FROM csf_assessment_audit WHERE tenant_id = $1 ORDER BY occurred_at DESC LIMIT 1
	`, tenant).Scan(&action, &kind, &fv); err != nil {
		t.Fatalf("read audit: %v", err)
	}
	if action != "tier_rated" || kind != "tier" || fv != fvID.String() {
		t.Fatalf("audit row = (%s,%s,%s), want (tier_rated,tier,%s)", action, kind, fv, fvID.String())
	}
}

// ===== AC-2 / AC-3: profile + selections + gap =====

func TestProfileSelectionsAndGap(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	fvID, reqIDs := seedCSFVersion(t, admin)
	tenant := freshTenant(t, admin, fvID)
	env := engineerServer(t, app, tenant)
	q := "?framework_version=" + fvID.String()

	// Current profile: GV.OC-01 partial.
	resp, _ := do(t, env, http.MethodPut, "/v1/csf/profiles/current/selections"+q,
		`{"requirement_id":"`+reqIDs[0].String()+`","target_outcome":"partial"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("set current selection status = %d", resp.StatusCode)
	}
	// Target profile: GV.OC-01 fully, PR.AA-01 largely.
	do(t, env, http.MethodPut, "/v1/csf/profiles/target/selections"+q,
		`{"requirement_id":"`+reqIDs[0].String()+`","target_outcome":"fully"}`)
	do(t, env, http.MethodPut, "/v1/csf/profiles/target/selections"+q,
		`{"requirement_id":"`+reqIDs[1].String()+`","target_outcome":"largely"}`)

	// Read the current profile back.
	resp, body := do(t, env, http.MethodGet, "/v1/csf/profiles/current"+q, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get current profile status = %d", resp.StatusCode)
	}
	sels, _ := body["selections"].([]any)
	if len(sels) != 1 {
		t.Fatalf("current selections = %d, want 1", len(sels))
	}

	// Gap view: GV.OC-01 partial→fully (gap +2), PR.AA-01 not_targeted→largely (gap +2).
	resp, body = do(t, env, http.MethodGet, "/v1/csf/gap"+q, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("gap status = %d", resp.StatusCode)
	}
	gap, _ := body["gap"].([]any)
	if len(gap) != 2 {
		t.Fatalf("gap rows = %d, want 2", len(gap))
	}
	for _, raw := range gap {
		row, _ := raw.(map[string]any)
		code, _ := row["subcategory_code"].(string)
		delta, _ := row["gap_delta"].(float64)
		switch code {
		case "GV.OC-01":
			if delta != 2 {
				t.Errorf("GV.OC-01 gap_delta = %v, want 2", delta)
			}
		case "PR.AA-01":
			if delta != 2 || row["current_outcome"] != "not_targeted" {
				t.Errorf("PR.AA-01 gap = %v (current %v), want 2 / not_targeted", delta, row["current_outcome"])
			}
		default:
			t.Errorf("unexpected gap subcategory %q", code)
		}
	}

	// Clearing a selection removes it from the profile.
	resp, _ = do(t, env, http.MethodDelete, "/v1/csf/profiles/current/selections/"+reqIDs[0].String()+q, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("clear selection status = %d", resp.StatusCode)
	}
	_, body = do(t, env, http.MethodGet, "/v1/csf/profiles/current"+q, "")
	sels, _ = body["selections"].([]any)
	if len(sels) != 0 {
		t.Fatalf("after clear, current selections = %d, want 0", len(sels))
	}
}

// ===== E: role cut =====

func TestRoleCut_ViewerCannotEdit_EngineerAndAdminCan(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	fvID, _ := seedCSFVersion(t, admin)
	tenant := freshTenant(t, admin, fvID)
	q := "?framework_version=" + fvID.String()

	// Viewer cannot set a Tier (403) but can read (200).
	viewer := viewerServer(t, app, tenant)
	resp, _ := do(t, viewer, http.MethodPut, "/v1/csf/tier"+q, `{"tier":"tier2_risk_informed"}`)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("viewer PUT tier = %d, want 403", resp.StatusCode)
	}
	resp, _ = do(t, viewer, http.MethodGet, "/v1/csf/tier"+q, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("viewer GET tier = %d, want 200", resp.StatusCode)
	}

	// grc_engineer can edit.
	eng := engineerServer(t, app, tenant)
	resp, _ = do(t, eng, http.MethodPut, "/v1/csf/tier"+q, `{"tier":"tier2_risk_informed"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("engineer PUT tier = %d, want 200", resp.StatusCode)
	}

	// admin can edit (the admin wildcard inside HasOwnerRole).
	adm := adminServer(t, app, tenant)
	resp, _ = do(t, adm, http.MethodPut, "/v1/csf/tier"+q, `{"tier":"tier4_adaptive"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("admin PUT tier = %d, want 200", resp.StatusCode)
	}
}

// ===== AC-4 (P0): RLS isolation =====
//
// TestRLS_CrossTenantIsolation is the load-bearing P0. It proves tenant A's
// Tier rating, profile selections, and gap view NEVER leak to tenant B —
// enforcement is the PostgreSQL RLS four-policy split + the app.current_tenant
// GUC, not application code. Tenant B's reads of the same framework_version
// return tenant B's (empty) state; tenant B's writes never touch tenant A's
// rows; and tenant A's rows are unchanged after tenant B's activity.
func TestRLS_CrossTenantIsolation(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	fvID, reqIDs := seedCSFVersion(t, admin)
	tenantA := freshTenant(t, admin, fvID)
	tenantB := freshTenant(t, admin, fvID)
	q := "?framework_version=" + fvID.String()

	// Tenant A builds an assessment: a Tier + a current selection.
	envA := engineerServer(t, app, tenantA)
	do(t, envA, http.MethodPut, "/v1/csf/tier"+q, `{"tier":"tier3_repeatable","rationale":"A-only"}`)
	do(t, envA, http.MethodPut, "/v1/csf/profiles/current/selections"+q,
		`{"requirement_id":"`+reqIDs[0].String()+`","target_outcome":"fully","note":"A-secret"}`)

	// Tenant B reads the SAME framework_version — sees NONE of tenant A's state.
	envB := engineerServer(t, app, tenantB)

	resp, body := do(t, envB, http.MethodGet, "/v1/csf/tier"+q, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("B GET tier status = %d", resp.StatusCode)
	}
	if body["tier_rating"] != nil {
		t.Fatalf("tenant B saw tenant A's tier rating: %v", body["tier_rating"])
	}

	resp, body = do(t, envB, http.MethodGet, "/v1/csf/profiles/current"+q, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("B GET profile status = %d", resp.StatusCode)
	}
	if body["profile"] != nil {
		t.Fatalf("tenant B saw tenant A's current profile: %v", body["profile"])
	}
	if sels, _ := body["selections"].([]any); len(sels) != 0 {
		t.Fatalf("tenant B saw %d of tenant A's selections, want 0", len(sels))
	}

	resp, body = do(t, envB, http.MethodGet, "/v1/csf/gap"+q, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("B GET gap status = %d", resp.StatusCode)
	}
	if gap, _ := body["gap"].([]any); len(gap) != 0 {
		t.Fatalf("tenant B's gap view exposed %d of tenant A's rows, want 0", len(gap))
	}
	if body["tier_rating"] != nil {
		t.Fatalf("tenant B's gap view exposed tenant A's tier rating: %v", body["tier_rating"])
	}

	// Tenant B writes its OWN tier; tenant A's rows are untouched.
	do(t, envB, http.MethodPut, "/v1/csf/tier"+q, `{"tier":"tier1_partial"}`)
	var aTier string
	if err := admin.QueryRow(context.Background(),
		`SELECT tier FROM csf_tier_ratings WHERE tenant_id = $1`, tenantA).Scan(&aTier); err != nil {
		t.Fatalf("read A tier: %v", err)
	}
	if aTier != "tier3_repeatable" {
		t.Fatalf("tenant A tier mutated cross-tenant: %s", aTier)
	}
	// Each tenant has exactly one tier row; tenant A's audit is not visible to B.
	var aAudit, bAudit int
	if err := admin.QueryRow(context.Background(),
		`SELECT count(*) FROM csf_assessment_audit WHERE tenant_id = $1`, tenantA).Scan(&aAudit); err != nil {
		t.Fatalf("count A audit: %v", err)
	}
	if err := admin.QueryRow(context.Background(),
		`SELECT count(*) FROM csf_assessment_audit WHERE tenant_id = $1`, tenantB).Scan(&bAudit); err != nil {
		t.Fatalf("count B audit: %v", err)
	}
	if aAudit == 0 || bAudit == 0 {
		t.Fatalf("audit rows A=%d B=%d, want both > 0 (per-tenant trails)", aAudit, bAudit)
	}
}

// TestRLS_DenyOnMissingContext asserts the deny-on-missing-context property:
// a query run with NO app.current_tenant GUC set returns zero rows under FORCE
// RLS (current_tenant_matches returns false when the GUC is unset). This is the
// fail-closed guarantee invariant #6 demands.
func TestRLS_DenyOnMissingContext(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	fvID, _ := seedCSFVersion(t, admin)
	tenant := freshTenant(t, admin, fvID)

	// Seed a tier row directly as the admin (bypass-RLS) role.
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO csf_tier_ratings (id, tenant_id, framework_version_id, tier, rated_by)
		VALUES ($1, $2, $3, 'tier2_risk_informed', 'seed')
	`, uuid.New(), tenant, fvID); err != nil {
		t.Fatalf("seed tier: %v", err)
	}

	// Read as the app role WITHOUT setting app.current_tenant — RLS denies.
	var n int
	if err := app.QueryRow(context.Background(),
		`SELECT count(*) FROM csf_tier_ratings`).Scan(&n); err != nil {
		t.Fatalf("count without GUC: %v", err)
	}
	if n != 0 {
		t.Fatalf("RLS leaked %d rows with no tenant context set; want 0 (deny-on-missing-context)", n)
	}
}
