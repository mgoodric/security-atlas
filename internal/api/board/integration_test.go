//go:build integration

// Slice 031 — integration tests for the monthly board brief HTTP API. Real
// Postgres + the assembled platform router so the tests exercise the full
// request path (tenancy middleware, RLS, the board Generator + Store, the
// slice-016 freshness/drift read models the Generator reads from). The DB is
// never mocked.
//
// Run with:
//
//	go test -tags=integration -race ./internal/api/board/...
//
// Requires DATABASE_URL_APP (atlas_app role) and DATABASE_URL (admin role).
//
// Coverage:
//
//	AC-1  POST /v1/board-briefs with period_end generates a pinned brief
//	AC-2  the brief carries framework posture + drift count + top-3 risks
//	AC-3  the narrative is templated over the structured metrics
//	AC-4  GET .../{id}.md and .../{id}/pdf return Markdown / PDF
//	AC-5  re-fetching after live state changes returns the original content
//	AC-6  generation works with no LLM dependency present (no LLM is wired)
//	      a second POST with the same period_end creates a NEW brief, not an edit
//	      malformed period_end -> 400; unknown id -> 404; missing bearer -> 401
//	RLS   cross-tenant isolation — tenant A never sees tenant B's briefs

package board_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api"
	"github.com/mgoodric/security-atlas/internal/api/testjwt"
	"github.com/mgoodric/security-atlas/internal/freshness"
	"github.com/mgoodric/security-atlas/internal/tenancy"
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

func freshTenant(t *testing.T, admin *pgxpool.Pool) string {
	t.Helper()
	tenant := uuid.NewString()
	t.Cleanup(func() {
		ctx := context.Background()
		for _, stmt := range []string{
			`DELETE FROM board_briefs WHERE tenant_id = $1`,
			`DELETE FROM evidence_freshness WHERE tenant_id = $1`,
			`DELETE FROM control_drift_snapshots WHERE tenant_id = $1`,
			`DELETE FROM control_evaluations WHERE tenant_id = $1`,
			`DELETE FROM evidence_records WHERE tenant_id = $1`,
			`DELETE FROM risk_control_links WHERE tenant_id = $1`,
			`DELETE FROM risks WHERE tenant_id = $1`,
			`DELETE FROM controls WHERE tenant_id = $1`,
		} {
			if _, err := admin.Exec(ctx, stmt, tenant); err != nil {
				t.Logf("cleanup %s: %v", stmt, err)
			}
		}
	})
	return tenant
}

func ctxFor(t *testing.T, tenant string) context.Context {
	t.Helper()
	ctx, err := tenancy.WithTenant(context.Background(), tenant)
	if err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
	return ctx
}

func seedControl(t *testing.T, admin *pgxpool.Pool, tenant, freshnessClass string) uuid.UUID {
	t.Helper()
	ctrlID := uuid.New()
	fc := freshnessClass
	bundleID := "test-bundle-031-" + ctrlID.String()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO controls (
			id, tenant_id, title, control_family, implementation_type,
			bundle_id, freshness_class, evidence_queries, applicability_expr
		)
		VALUES ($1, $2, 'slice 031 board test control', 'AAA', 'automated',
		        $3, $4, '[]'::jsonb, 'true')
	`, ctrlID, tenant, bundleID, fc); err != nil {
		t.Fatalf("seed control: %v", err)
	}
	return ctrlID
}

func seedEvidence(t *testing.T, admin *pgxpool.Pool, tenant string, ctrlID uuid.UUID, observedAt time.Time) {
	t.Helper()
	id := uuid.New()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO evidence_records (
			id, tenant_id, control_id, observed_at, ingested_at,
			provenance, result, payload, hash, control_ref
		)
		VALUES ($1, $2, $3, $4, now(), '{}'::jsonb, 'pass', '{}'::jsonb, $5, $6)
	`, id, tenant, ctrlID, observedAt, "hash-031-"+id.String()[:8], ctrlID.String()); err != nil {
		t.Fatalf("seed evidence: %v", err)
	}
}

func seedRisk(t *testing.T, admin *pgxpool.Pool, tenant, title string, residualScore float64, updatedDaysAgo int) uuid.UUID {
	t.Helper()
	id := uuid.New()
	updatedAt := time.Now().UTC().AddDate(0, 0, -updatedDaysAgo)
	residual := []byte(`{"score": ` + floatStr(residualScore) + `}`)
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO risks (
			id, tenant_id, title, description, category, methodology,
			inherent_score, treatment, treatment_owner, residual_score,
			accepted_until, accepter, created_at, updated_at
		)
		VALUES ($1, $2, $3, '', 'operational', 'nist_800_30',
		        '{"likelihood": 3, "impact": 3}'::jsonb, 'mitigate', 'sec-lead', $4,
		        NULL, '', $5, $5)
	`, id, tenant, title, residual, updatedAt); err != nil {
		t.Fatalf("seed risk: %v", err)
	}
	return id
}

func floatStr(f float64) string {
	b, _ := json.Marshal(f)
	return string(b)
}

func seedFramework(t *testing.T, admin *pgxpool.Pool, tenant, slug, name string) {
	t.Helper()
	id := uuid.New()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO frameworks (id, tenant_id, name, slug, issuer)
		VALUES ($1, $2, $3, $4, 'test-issuer')
	`, id, tenant, name, slug); err != nil {
		t.Fatalf("seed framework: %v", err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), `DELETE FROM frameworks WHERE id = $1`, id)
	})
}

// testEnv bundles the running server with the bearer token bound to the
// tenant, plus the freshness Store the test uses to populate the read model
// the Generator reads from.
type testEnv struct {
	server    *httptest.Server
	bearer    string
	freshness *freshness.Store
}

func testServer(t *testing.T, app *pgxpool.Pool, tenant string) testEnv {
	t.Helper()
	srv := api.New(api.Config{})
	srv.AttachDB(app)

	// Slice 197: JWT bearer via slice 190 path (owner roles).
	bearer := srv.IssueTestJWT(t, testjwt.OwnerFor(uuid.MustParse(tenant), []string{"owner"}))

	h := srv.HTTPHandlerForTests()
	if h == nil {
		t.Fatal("HTTPHandlerForTests returned nil — DB pool not attached")
	}
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	return testEnv{
		server:    ts,
		bearer:    bearer,
		freshness: freshness.NewStore(app),
	}
}

func doJSON(t *testing.T, env testEnv, method, path string, body any) (*http.Response, map[string]any) {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, env.server.URL+path, rdr)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+env.bearer)
	if body != nil {
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

func doRaw(t *testing.T, env testEnv, path string) (*http.Response, []byte) {
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
	raw, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	return resp, raw
}

// ===== AC-1 + AC-2 + AC-3: POST generates a pinned brief carrying framework
// posture, drift count, and top-3 risks, with a templated narrative =====

func TestGenerate_PinnedBriefWithAllSections(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	tenant := freshTenant(t, admin)
	env := testServer(t, app, tenant)

	// Seed a framework, a fresh control (drives coverage/freshness), and
	// three risks of differing residual severity.
	seedFramework(t, admin, tenant, "soc2-031test", "SOC 2 (031 test)")
	ctrl := seedControl(t, admin, tenant, "monthly")
	seedEvidence(t, admin, tenant, ctrl, time.Now().UTC().Add(-1*24*time.Hour))
	if _, err := env.freshness.Refresh(ctxFor(t, tenant)); err != nil {
		t.Fatalf("freshness Refresh: %v", err)
	}
	seedRisk(t, admin, tenant, "Critical residual risk", 20, 120)
	seedRisk(t, admin, tenant, "High residual risk", 16, 80)
	seedRisk(t, admin, tenant, "Moderate residual risk", 12, 60)
	seedRisk(t, admin, tenant, "Low residual risk", 4, 10)

	resp, body := doJSON(t, env, http.MethodPost, "/v1/board-briefs",
		map[string]string{"period_end": "2026-04-30"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("AC-1: POST /v1/board-briefs = %d, want 201; body=%v", resp.StatusCode, body)
	}
	if body["id"] == nil || body["id"] == "" {
		t.Fatal("AC-1: response missing brief id")
	}
	if body["period_end"] != "2026-04-30" {
		t.Errorf("AC-1: period_end = %v, want 2026-04-30", body["period_end"])
	}

	content, ok := body["content"].(map[string]any)
	if !ok {
		t.Fatalf("AC-2: response missing structured content; body=%v", body)
	}
	// AC-2: framework posture — at least the seeded framework is present.
	frameworks, ok := content["frameworks"].([]any)
	if !ok || len(frameworks) == 0 {
		t.Fatalf("AC-2: brief has no framework posture rows; content=%v", content)
	}
	// AC-2: drift section present with a 30-day window.
	drift, ok := content["drift"].(map[string]any)
	if !ok {
		t.Fatalf("AC-2: brief missing drift section; content=%v", content)
	}
	if drift["window_days"].(float64) != 30 {
		t.Errorf("AC-2: drift window_days = %v, want 30", drift["window_days"])
	}
	// AC-2: top-3 risks — exactly 3 (of the 4 seeded), highest residual
	// severity first.
	topRisks, ok := content["top_risks"].([]any)
	if !ok || len(topRisks) != 3 {
		t.Fatalf("AC-2: expected 3 top risks (4 seeded), got %v", content["top_risks"])
	}
	first := topRisks[0].(map[string]any)
	if first["title"] != "Critical residual risk" {
		t.Errorf("AC-2: top risk = %v, want 'Critical residual risk' (highest residual)", first["title"])
	}
	// "Low residual risk" (severity 4) must be dropped — only the top 3 by
	// residual severity survive.
	for _, r := range topRisks {
		if r.(map[string]any)["title"] == "Low residual risk" {
			t.Error("AC-2: low-severity risk should not appear in the top 3 when 4 risks exist")
		}
	}

	// AC-3: the narrative is templated over the structured metrics.
	narrative, _ := body["narrative_md"].(string)
	if narrative == "" {
		t.Fatal("AC-3: response missing narrative_md")
	}
	if !bytes.Contains([]byte(narrative), []byte("Monthly Board Brief — 2026-04-30")) {
		t.Errorf("AC-3: narrative missing templated heading; got:\n%s", narrative)
	}
	if !bytes.Contains([]byte(narrative), []byte("SOC 2 (031 test)")) {
		t.Errorf("AC-3: narrative missing seeded framework name; got:\n%s", narrative)
	}
}

// ===== AC-1: a malformed period_end is rejected 400 =====

func TestGenerate_MalformedPeriodEndIs400(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	tenant := freshTenant(t, admin)
	env := testServer(t, app, tenant)

	for _, bad := range []string{"2026-13-01", "not-a-date", "04/30/2026", ""} {
		resp, _ := doJSON(t, env, http.MethodPost, "/v1/board-briefs",
			map[string]string{"period_end": bad})
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("AC-1: POST period_end=%q = %d, want 400", bad, resp.StatusCode)
		}
	}
}

// ===== AC-5: re-fetching a brief after live state changes returns the
// ORIGINAL frozen content — the snapshot is immutable =====

func TestGet_ReturnsFrozenContentAfterLiveChange(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	tenant := freshTenant(t, admin)
	env := testServer(t, app, tenant)

	seedRisk(t, admin, tenant, "Original risk", 15, 30)

	// Generate the brief.
	resp, body := doJSON(t, env, http.MethodPost, "/v1/board-briefs",
		map[string]string{"period_end": "2026-04-30"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST: status %d; body=%v", resp.StatusCode, body)
	}
	briefID := body["id"].(string)
	originalContent := body["content"].(map[string]any)
	originalRisks := originalContent["top_risks"].([]any)
	if len(originalRisks) != 1 {
		t.Fatalf("setup: expected 1 risk in the original brief, got %d", len(originalRisks))
	}

	// Mutate live state: add two more risks AFTER the brief was pinned.
	seedRisk(t, admin, tenant, "Risk added later A", 25, 5)
	seedRisk(t, admin, tenant, "Risk added later B", 22, 5)

	// Re-fetch the brief — it must return the ORIGINAL frozen content,
	// untouched by the live mutation (AC-5).
	resp, refetched := doJSON(t, env, http.MethodGet, "/v1/board-briefs/"+briefID, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("AC-5: GET brief = %d, want 200", resp.StatusCode)
	}
	refetchedContent := refetched["content"].(map[string]any)
	refetchedRisks := refetchedContent["top_risks"].([]any)
	if len(refetchedRisks) != 1 {
		t.Errorf("AC-5: re-fetched brief has %d risks, want 1 — the snapshot must be frozen, not recomputed against live state",
			len(refetchedRisks))
	}
	if refetchedRisks[0].(map[string]any)["title"] != "Original risk" {
		t.Errorf("AC-5: re-fetched top risk = %v, want 'Original risk' — frozen content changed",
			refetchedRisks[0].(map[string]any)["title"])
	}
}

// ===== anti-criterion P0: a second POST with the SAME period_end creates a
// NEW brief row with a NEW id — never an edit of the pinned snapshot =====

func TestGenerate_RepeatedPeriodEndCreatesNewBrief(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	tenant := freshTenant(t, admin)
	env := testServer(t, app, tenant)

	resp1, body1 := doJSON(t, env, http.MethodPost, "/v1/board-briefs",
		map[string]string{"period_end": "2026-04-30"})
	if resp1.StatusCode != http.StatusCreated {
		t.Fatalf("first POST: status %d", resp1.StatusCode)
	}
	resp2, body2 := doJSON(t, env, http.MethodPost, "/v1/board-briefs",
		map[string]string{"period_end": "2026-04-30"})
	if resp2.StatusCode != http.StatusCreated {
		t.Fatalf("second POST: status %d", resp2.StatusCode)
	}
	id1, id2 := body1["id"].(string), body2["id"].(string)
	if id1 == id2 {
		t.Errorf("P0: second POST with the same period_end returned the same id %s — must be a NEW snapshot, not an edit", id1)
	}

	// Both briefs must still be independently fetchable — neither replaced
	// the other.
	for _, id := range []string{id1, id2} {
		resp, _ := doJSON(t, env, http.MethodGet, "/v1/board-briefs/"+id, nil)
		if resp.StatusCode != http.StatusOK {
			t.Errorf("P0: GET brief %s = %d, want 200 — both snapshots must persist", id, resp.StatusCode)
		}
	}
}

// ===== AC-4: GET .../{id}.md returns the frozen Markdown narrative =====

func TestMarkdown_ReturnsFrozenNarrative(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	tenant := freshTenant(t, admin)
	env := testServer(t, app, tenant)

	resp, body := doJSON(t, env, http.MethodPost, "/v1/board-briefs",
		map[string]string{"period_end": "2026-04-30"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST: status %d", resp.StatusCode)
	}
	briefID := body["id"].(string)
	wantNarrative := body["narrative_md"].(string)

	resp, raw := doRaw(t, env, "/v1/board-briefs/"+briefID+".md")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("AC-4: GET .md = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/markdown; charset=utf-8" {
		t.Errorf("AC-4: .md Content-Type = %q, want text/markdown; charset=utf-8", ct)
	}
	if string(raw) != wantNarrative {
		t.Errorf("AC-4: .md body does not match the frozen narrative\ngot:\n%s\nwant:\n%s", raw, wantNarrative)
	}
}

// ===== AC-4: GET .../{id}/pdf returns a PDF (or 503 when chrome is absent) =====

func TestPDF_ReturnsPDFOrServiceUnavailable(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	tenant := freshTenant(t, admin)
	env := testServer(t, app, tenant)

	resp, body := doJSON(t, env, http.MethodPost, "/v1/board-briefs",
		map[string]string{"period_end": "2026-04-30"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST: status %d", resp.StatusCode)
	}
	briefID := body["id"].(string)

	resp, raw := doRaw(t, env, "/v1/board-briefs/"+briefID+"/pdf")
	switch resp.StatusCode {
	case http.StatusOK:
		// AC-4: a real PDF — bytes begin with the %PDF- magic header.
		if len(raw) < 5 || string(raw[:5]) != "%PDF-" {
			t.Errorf("AC-4: PDF body does not start with %%PDF- magic; got prefix %q", safePrefix(raw))
		}
		if ct := resp.Header.Get("Content-Type"); ct != "application/pdf" {
			t.Errorf("AC-4: PDF Content-Type = %q, want application/pdf", ct)
		}
	case http.StatusServiceUnavailable:
		// Acceptable when chrome is unavailable in the test env — the
		// handler degrades gracefully rather than 500ing.
		t.Logf("AC-4: PDF endpoint returned 503 (chrome unavailable in this env) — acceptable degradation")
	default:
		t.Fatalf("AC-4: GET /pdf = %d, want 200 or 503; body=%q", resp.StatusCode, raw)
	}
}

func safePrefix(b []byte) string {
	if len(b) > 16 {
		return string(b[:16])
	}
	return string(b)
}

// ===== unknown id -> 404; missing bearer -> 401 =====

func TestGet_UnknownIDIs404(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	tenant := freshTenant(t, admin)
	env := testServer(t, app, tenant)

	resp, _ := doJSON(t, env, http.MethodGet, "/v1/board-briefs/"+uuid.NewString(), nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("GET unknown id = %d, want 404", resp.StatusCode)
	}
}

func TestBoardBriefs_MissingBearerIs401(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	tenant := freshTenant(t, admin)
	env := testServer(t, app, tenant)

	resp, err := http.Get(env.server.URL + "/v1/board-briefs")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("missing bearer = %d, want 401", resp.StatusCode)
	}
}

// ===== RLS: cross-tenant isolation — tenant A never sees tenant B's briefs ==

func TestBoardBriefs_CrossTenantIsolation(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	tenantA := freshTenant(t, admin)
	tenantB := freshTenant(t, admin)

	// Tenant B generates a brief.
	envB := testServer(t, app, tenantB)
	resp, bodyB := doJSON(t, envB, http.MethodPost, "/v1/board-briefs",
		map[string]string{"period_end": "2026-04-30"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("tenant B POST: status %d", resp.StatusCode)
	}
	briefBID := bodyB["id"].(string)

	// Tenant A must NOT be able to fetch tenant B's brief — RLS makes the
	// row invisible, so the lookup 404s.
	envA := testServer(t, app, tenantA)
	resp, _ = doJSON(t, envA, http.MethodGet, "/v1/board-briefs/"+briefBID, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("RLS: tenant A GET of tenant B's brief = %d, want 404", resp.StatusCode)
	}

	// Tenant A's list must be empty — it sees none of tenant B's briefs.
	resp, listA := doJSON(t, envA, http.MethodGet, "/v1/board-briefs", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("RLS: tenant A list = %d, want 200", resp.StatusCode)
	}
	briefs, _ := listA["briefs"].([]any)
	if len(briefs) != 0 {
		t.Errorf("RLS: tenant A's brief list has %d entries, want 0 — saw tenant B's briefs", len(briefs))
	}
}
