//go:build integration

// Slice 064 — integration tests for the control-detail backend read
// endpoints. Real Postgres + the assembled platform router (or, for the
// 403 + RLS-isolation cases, a minimally-wired router) so the tests
// exercise the full request path: tenancy middleware, RLS, the sqlc query
// layer.
//
// Run with:
//
//	go test -tags=integration -race ./internal/api/controldetail/...
//
// Requires DATABASE_URL_APP (atlas_app role) and DATABASE_URL (admin role).
//
// Coverage (>=6 tests, AC-7):
//
//	ISC-28a  evidence list respects the 30-day window + control scoping
//	ISC-28b  policies endpoint returns slice-022-linked policies
//	ISC-28c  risks endpoint returns slice-020-linked risks with residual
//	ISC-28d  history endpoint returns control_evaluations rows newest-first
//	ISC-28e  all four endpoints 403 a role without control-read access
//	ISC-28f  all four endpoints are RLS-isolated across tenants
//	(plus)   evidence keyset pagination returns a stable next_cursor

package controldetail_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api"
	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/controldetail"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/api/tenancymw"
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

// freshTenant returns a new tenant id and registers a cleanup that deletes
// every row this slice's tests can create under it.
func freshTenant(t *testing.T, admin *pgxpool.Pool) string {
	t.Helper()
	tenant := uuid.NewString()
	t.Cleanup(func() {
		ctx := context.Background()
		for _, stmt := range []string{
			`DELETE FROM control_evaluations WHERE tenant_id = $1`,
			`DELETE FROM evidence_records WHERE tenant_id = $1`,
			`DELETE FROM risk_control_links WHERE tenant_id = $1`,
			`DELETE FROM risks WHERE tenant_id = $1`,
			`DELETE FROM policies WHERE tenant_id = $1`,
			`DELETE FROM controls WHERE tenant_id = $1`,
		} {
			if _, err := admin.Exec(ctx, stmt, tenant); err != nil {
				t.Logf("cleanup %s: %v", stmt, err)
			}
		}
	})
	return tenant
}

// seedControl inserts a minimal control row directly via the admin pool.
// bundle_id is set (slice 009 made it NOT NULL) and evidence_queries is the
// empty JSON array, mirroring the slice-012 control-state test harness.
func seedControl(t *testing.T, admin *pgxpool.Pool, tenant string) uuid.UUID {
	t.Helper()
	ctrlID := uuid.New()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO controls (
			id, tenant_id, title, control_family, implementation_type,
			bundle_id, evidence_queries, applicability_expr
		)
		VALUES ($1, $2, 'slice 064 test control', 'AAA', 'automated',
		        $3, '[]'::jsonb, 'true')
	`, ctrlID, tenant, "test-bundle-064-"+ctrlID.String()); err != nil {
		t.Fatalf("seed control: %v", err)
	}
	return ctrlID
}

// seedEvidence inserts one evidence_records row. control_ref is set to the
// control UUID's string form, mirroring slice 012's loadEvidence path.
func seedEvidence(t *testing.T, admin *pgxpool.Pool, tenant string, ctrlID uuid.UUID, kind string, observedAt time.Time) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO evidence_records (
			id, tenant_id, control_id, control_ref, observed_at, ingested_at,
			provenance, result, payload, hash, evidence_kind
		)
		VALUES ($1, $2, $3, $4, $5, now(), $6, 'pass', '{}'::jsonb, $7, $8)
	`, id, tenant, ctrlID, ctrlID.String(), observedAt,
		`{"connector_id":"test-connector"}`,
		"hash-064-"+id.String()[:8], kind); err != nil {
		t.Fatalf("seed evidence: %v", err)
	}
	return id
}

// seedPolicy inserts one policies row linking ctrlID via linked_control_ids.
// The slice-022 schema enforces non-empty owner_role / approver_role /
// created_by and an effective_date when status is published/superseded, so
// the seed populates all of them. `status` is the caller-supplied lifecycle
// state; published/superseded rows get an effective_date.
func seedPolicy(t *testing.T, admin *pgxpool.Pool, tenant string, ctrlID uuid.UUID, title, version, status string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	var effectiveDate *time.Time
	if status == "published" || status == "superseded" {
		d := time.Now().UTC()
		effectiveDate = &d
	}
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO policies (
			id, tenant_id, title, version, body_md, status,
			owner_role, approver_role, created_by, effective_date,
			linked_control_ids
		)
		VALUES ($1, $2, $3, $4, $5, $6,
		        'grc_engineer', 'grc_engineer', 'test-seeder', $7,
		        ARRAY[$8]::uuid[])
	`, id, tenant, title, version, "# "+title, status, effectiveDate, ctrlID); err != nil {
		t.Fatalf("seed policy: %v", err)
	}
	return id
}

// seedRiskWithLink inserts one risk + one risk_control_links row to ctrlID.
func seedRiskWithLink(t *testing.T, admin *pgxpool.Pool, tenant string, ctrlID uuid.UUID, title string, designScore float64) uuid.UUID {
	t.Helper()
	riskID := uuid.New()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO risks (
			id, tenant_id, title, category, methodology,
			inherent_score, treatment, residual_score
		)
		VALUES ($1, $2, $3, 'confidentiality', 'qualitative_5x5',
		        '{"likelihood":3,"impact":4}'::jsonb, 'mitigate',
		        '{"likelihood":2,"impact":3}'::jsonb)
	`, riskID, tenant, title); err != nil {
		t.Fatalf("seed risk: %v", err)
	}
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO risk_control_links (
			risk_id, control_id, tenant_id, design_score
		)
		VALUES ($1, $2, $3, $4)
	`, riskID, ctrlID, tenant, designScore); err != nil {
		t.Fatalf("seed risk_control_link: %v", err)
	}
	return riskID
}

// seedEvaluation inserts one control_evaluations row.
func seedEvaluation(t *testing.T, admin *pgxpool.Pool, tenant string, ctrlID uuid.UUID, result string, evaluatedAt time.Time) {
	t.Helper()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO control_evaluations (
			id, tenant_id, control_id, eval_run_id, evaluated_at,
			result, freshness_status, evidence_count_in_window, trigger
		)
		VALUES ($1, $2, $3, $4, $5, $6, 'fresh', 1, 'manual')
	`, uuid.New(), tenant, ctrlID, uuid.New(), evaluatedAt, result); err != nil {
		t.Fatalf("seed control_evaluation: %v", err)
	}
}

// testEnv bundles the running server with a bearer token bound to the tenant.
type testEnv struct {
	server *httptest.Server
	bearer string
}

// testServer assembles the full platform router with an owner credential —
// owner credentials carry OwnerRoles, so requireControlRead admits them.
func testServer(t *testing.T, app *pgxpool.Pool, tenant string) testEnv {
	t.Helper()
	srv := api.New(api.Config{})
	srv.AttachDB(app)
	_, bearer, err := srv.IssueBootstrapOwnerCredential(tenant, []string{"control_owner"})
	if err != nil {
		t.Fatalf("IssueBootstrapOwnerCredential: %v", err)
	}
	h := srv.HTTPHandlerForTests()
	if h == nil {
		t.Fatal("HTTPHandlerForTests returned nil — DB pool not attached")
	}
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	return testEnv{server: ts, bearer: bearer}
}

// get issues an authenticated GET and decodes the JSON body.
func get(t *testing.T, env testEnv, path string) (*http.Response, map[string]any) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, env.server.URL+path, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+env.bearer)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	_ = resp.Body.Close()
	return resp, body
}

// noRoleRouter wires the four routes behind a credential carrying NO
// control-read signal — no IsAdmin, no IsApprover, no OwnerRoles. That is
// the v1 representation of a viewer-only credential (credstore does not
// issue one first-class). It exercises the handler-level requireControlRead
// guard without standing up OPA. The guard runs FIRST in every handler —
// before tenant resolution — so the 403 fires regardless of the tenant
// context, which is why this router can give the credential a real tenant
// id and still observe the 403.
func noRoleRouter(t *testing.T, app *pgxpool.Pool, tenant string) http.Handler {
	t.Helper()
	h := controldetail.New(controldetail.NewStore(app))
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := authctx.WithCredential(req.Context(), credstore.Credential{
				TenantID:   tenant,
				UserID:     "viewer-test",
				OwnerRoles: nil,
			})
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	r.Use(tenancymw.Middleware)
	r.Get("/v1/evidence", h.Evidence)
	r.Get("/v1/controls/{id}/policies", h.Policies)
	r.Get("/v1/controls/{id}/risks", h.Risks)
	r.Get("/v1/controls/{id}/history", h.History)
	return r
}

// ===== ISC-28a: evidence list respects the 30-day window + control scoping =====

func TestEvidence_WindowAndControlScoping(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	tenant := freshTenant(t, admin)
	env := testServer(t, app, tenant)

	ctrlID := seedControl(t, admin, tenant)
	otherCtrl := seedControl(t, admin, tenant)

	// In-window evidence for the target control.
	seedEvidence(t, admin, tenant, ctrlID, "sast.scan_result", time.Now().UTC().Add(-5*24*time.Hour))
	seedEvidence(t, admin, tenant, ctrlID, "sast.scan_result", time.Now().UTC().Add(-10*24*time.Hour))
	// Out-of-window evidence (older than 30 days) — must be excluded by default.
	seedEvidence(t, admin, tenant, ctrlID, "sast.scan_result", time.Now().UTC().Add(-45*24*time.Hour))
	// Evidence for a DIFFERENT control — must not appear.
	seedEvidence(t, admin, tenant, otherCtrl, "sast.scan_result", time.Now().UTC().Add(-2*24*time.Hour))

	resp, body := get(t, env, "/v1/evidence?control_id="+ctrlID.String())
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET evidence: status %d, want 200", resp.StatusCode)
	}
	rows, _ := body["evidence"].([]any)
	if len(rows) != 2 {
		t.Fatalf("ISC-28a: expected 2 in-window rows for the control, got %d", len(rows))
	}
	// Verify the row shape carries the AC-1 fields.
	first := rows[0].(map[string]any)
	for _, field := range []string{"evidence_id", "evidence_kind", "observed_at", "source", "content_hash", "scope_cell"} {
		if _, ok := first[field]; !ok {
			t.Fatalf("ISC-28a: evidence row missing field %q", field)
		}
	}
}

// (plus): keyset pagination returns a stable next_cursor.

func TestEvidence_KeysetPagination(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	tenant := freshTenant(t, admin)
	env := testServer(t, app, tenant)

	ctrlID := seedControl(t, admin, tenant)
	for i := 0; i < 5; i++ {
		seedEvidence(t, admin, tenant, ctrlID, "sast.scan_result",
			time.Now().UTC().Add(-time.Duration(i+1)*24*time.Hour))
	}

	// Page 1: limit 2 -> 2 rows + a next_cursor.
	resp, body := get(t, env, "/v1/evidence?control_id="+ctrlID.String()+"&limit=2")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("page1: status %d", resp.StatusCode)
	}
	rows, _ := body["evidence"].([]any)
	if len(rows) != 2 {
		t.Fatalf("page1: expected 2 rows, got %d", len(rows))
	}
	cursor, _ := body["next_cursor"].(string)
	if cursor == "" {
		t.Fatalf("page1: expected a non-empty next_cursor")
	}

	// Page 2: same limit + the cursor -> the next 2 rows, no overlap.
	page1IDs := map[string]bool{}
	for _, r := range rows {
		page1IDs[r.(map[string]any)["evidence_id"].(string)] = true
	}
	resp, body = get(t, env, "/v1/evidence?control_id="+ctrlID.String()+"&limit=2&cursor="+cursor)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("page2: status %d", resp.StatusCode)
	}
	rows, _ = body["evidence"].([]any)
	if len(rows) != 2 {
		t.Fatalf("page2: expected 2 rows, got %d", len(rows))
	}
	for _, r := range rows {
		if page1IDs[r.(map[string]any)["evidence_id"].(string)] {
			t.Fatalf("page2: row overlaps page1 — keyset pagination drifted")
		}
	}
}

// ===== ISC-28b: policies endpoint returns slice-022-linked policies =====

func TestPolicies_ReturnsLinkedPolicies(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	tenant := freshTenant(t, admin)
	env := testServer(t, app, tenant)

	ctrlID := seedControl(t, admin, tenant)
	otherCtrl := seedControl(t, admin, tenant)
	seedPolicy(t, admin, tenant, ctrlID, "Access Control Policy", "1.2.0", "published")
	seedPolicy(t, admin, tenant, ctrlID, "Encryption Policy", "2.0.0", "approved")
	// Policy linked to a different control — must not appear.
	seedPolicy(t, admin, tenant, otherCtrl, "Unrelated Policy", "1.0.0", "draft")

	resp, body := get(t, env, "/v1/controls/"+ctrlID.String()+"/policies")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET policies: status %d, want 200", resp.StatusCode)
	}
	rows, _ := body["policies"].([]any)
	if len(rows) != 2 {
		t.Fatalf("ISC-28b: expected 2 linked policies, got %d", len(rows))
	}
	first := rows[0].(map[string]any)
	for _, field := range []string{"policy_id", "title", "version", "status"} {
		if _, ok := first[field]; !ok {
			t.Fatalf("ISC-28b: policy row missing field %q", field)
		}
	}
}

// ===== ISC-28c: risks endpoint returns slice-020-linked risks with residual =====

func TestRisks_ReturnsLinkedRisksWithResidual(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	tenant := freshTenant(t, admin)
	env := testServer(t, app, tenant)

	ctrlID := seedControl(t, admin, tenant)
	otherCtrl := seedControl(t, admin, tenant)
	seedRiskWithLink(t, admin, tenant, ctrlID, "Data exfiltration", 0.75)
	// Risk linked to a different control — must not appear.
	seedRiskWithLink(t, admin, tenant, otherCtrl, "Unrelated risk", 0.5)

	resp, body := get(t, env, "/v1/controls/"+ctrlID.String()+"/risks")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET risks: status %d, want 200", resp.StatusCode)
	}
	rows, _ := body["risks"].([]any)
	if len(rows) != 1 {
		t.Fatalf("ISC-28c: expected 1 linked risk, got %d", len(rows))
	}
	first := rows[0].(map[string]any)
	for _, field := range []string{"risk_id", "title", "inherent_score", "residual_score", "link_weight"} {
		if _, ok := first[field]; !ok {
			t.Fatalf("ISC-28c: risk row missing field %q", field)
		}
	}
	// residual_score is the risk's computed JSONB — must be a real object,
	// not null, for a seeded risk.
	if _, ok := first["residual_score"].(map[string]any); !ok {
		t.Fatalf("ISC-28c: residual_score should be a JSON object, got %v", first["residual_score"])
	}
	if lw, ok := first["link_weight"].(float64); !ok || lw != 0.75 {
		t.Fatalf("ISC-28c: link_weight = %v, want 0.75", first["link_weight"])
	}
}

// ===== ISC-28d: history endpoint returns control_evaluations rows newest-first =====

func TestHistory_ReturnsEvaluationsNewestFirst(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	tenant := freshTenant(t, admin)
	env := testServer(t, app, tenant)

	ctrlID := seedControl(t, admin, tenant)
	oldest := time.Now().UTC().Add(-3 * time.Hour)
	middle := time.Now().UTC().Add(-2 * time.Hour)
	newest := time.Now().UTC().Add(-1 * time.Hour)
	seedEvaluation(t, admin, tenant, ctrlID, "pass", oldest)
	seedEvaluation(t, admin, tenant, ctrlID, "fail", middle)
	seedEvaluation(t, admin, tenant, ctrlID, "pass", newest)

	resp, body := get(t, env, "/v1/controls/"+ctrlID.String()+"/history")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET history: status %d, want 200", resp.StatusCode)
	}
	rows, _ := body["history"].([]any)
	if len(rows) != 3 {
		t.Fatalf("ISC-28d: expected 3 evaluation rows, got %d", len(rows))
	}
	first := rows[0].(map[string]any)
	for _, field := range []string{"evaluated_at", "scope_cell", "computed_state", "freshness_status", "evidence_count"} {
		if _, ok := first[field]; !ok {
			t.Fatalf("ISC-28d: history row missing field %q", field)
		}
	}
	// Newest-first: the first row's evaluated_at must be after the last's.
	firstTS := first["evaluated_at"].(string)
	lastTS := rows[2].(map[string]any)["evaluated_at"].(string)
	if firstTS <= lastTS {
		t.Fatalf("ISC-28d: history not newest-first: first %s <= last %s", firstTS, lastTS)
	}
}

// ===== ISC-28e: all four endpoints 403 a role without control-read access =====

func TestAllEndpoints_ForbidNonControlReadRole(t *testing.T) {
	app := openPool(t, appDSN(t))
	admin := openPool(t, adminDSN(t))
	tenant := freshTenant(t, admin)
	ctrlID := seedControl(t, admin, tenant)
	r := noRoleRouter(t, app, tenant)

	paths := []string{
		"/v1/evidence?control_id=" + ctrlID.String(),
		"/v1/controls/" + ctrlID.String() + "/policies",
		"/v1/controls/" + ctrlID.String() + "/risks",
		"/v1/controls/" + ctrlID.String() + "/history",
	}
	for _, p := range paths {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("ISC-28e: GET %s — status %d, want 403; body %s", p, rec.Code, rec.Body.String())
		}
	}
}

// ===== ISC-28f: all four endpoints are RLS-isolated across tenants =====

func TestAllEndpoints_RLSIsolatedAcrossTenants(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))

	// Tenant A owns a control with evidence, a policy, a risk link, and an
	// evaluation. Tenant B's server must see NONE of it.
	tenantA := freshTenant(t, admin)
	tenantB := freshTenant(t, admin)

	ctrlID := seedControl(t, admin, tenantA)
	seedEvidence(t, admin, tenantA, ctrlID, "sast.scan_result", time.Now().UTC().Add(-1*24*time.Hour))
	seedPolicy(t, admin, tenantA, ctrlID, "Tenant A policy", "1.0.0", "published")
	seedRiskWithLink(t, admin, tenantA, ctrlID, "Tenant A risk", 0.6)
	seedEvaluation(t, admin, tenantA, ctrlID, "pass", time.Now().UTC().Add(-1*time.Hour))

	// Tenant B's bearer — RLS must scope every read to tenant B (which owns
	// nothing referencing ctrlID).
	envB := testServer(t, app, tenantB)

	cases := []struct {
		path string
		key  string
	}{
		{"/v1/evidence?control_id=" + ctrlID.String(), "evidence"},
		{"/v1/controls/" + ctrlID.String() + "/policies", "policies"},
		{"/v1/controls/" + ctrlID.String() + "/risks", "risks"},
		{"/v1/controls/" + ctrlID.String() + "/history", "history"},
	}
	for _, c := range cases {
		resp, body := get(t, envB, c.path)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("ISC-28f: GET %s — status %d, want 200", c.path, resp.StatusCode)
		}
		rows, _ := body[c.key].([]any)
		if len(rows) != 0 {
			t.Fatalf("ISC-28f: GET %s leaked %d cross-tenant rows", c.path, len(rows))
		}
	}
}

// ===== input validation =====

func TestEvidence_RejectsBadInput(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	tenant := freshTenant(t, admin)
	env := testServer(t, app, tenant)
	ctrlID := seedControl(t, admin, tenant)

	cases := []struct {
		path string
		want int
	}{
		{"/v1/evidence", http.StatusBadRequest},                                               // missing control_id
		{"/v1/evidence?control_id=not-a-uuid", http.StatusBadRequest},                         // non-uuid control_id
		{"/v1/evidence?control_id=" + ctrlID.String() + "&limit=999", http.StatusBadRequest},  // limit over cap
		{"/v1/evidence?control_id=" + ctrlID.String() + "&limit=abc", http.StatusBadRequest},  // non-int limit
		{"/v1/evidence?control_id=" + ctrlID.String() + "&cursor=@@@", http.StatusBadRequest}, // malformed cursor
		{"/v1/evidence?control_id=" + ctrlID.String() + "&since=not-a-time", http.StatusBadRequest},
		{"/v1/controls/not-a-uuid/policies", http.StatusBadRequest},
		{"/v1/controls/not-a-uuid/risks", http.StatusBadRequest},
		{"/v1/controls/not-a-uuid/history", http.StatusBadRequest},
	}
	for _, c := range cases {
		resp, _ := get(t, env, c.path)
		if resp.StatusCode != c.want {
			t.Fatalf("GET %s — status %d, want %d", c.path, resp.StatusCode, c.want)
		}
	}
}
