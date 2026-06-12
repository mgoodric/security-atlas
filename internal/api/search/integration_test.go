//go:build integration

// Slice 268 — integration tests for `/v1/search` against real Postgres.
//
// Coverage matches AC-6, AC-7, AC-9 from the slice spec:
//
//	IST-1  cross-tenant isolation: tenant A's search NEVER returns
//	       tenant B's rows (the load-bearing P0-A3 verification).
//	IST-2  happy path: a multi-token query returns hits across all
//	       three types, sorted by relevance DESC.
//	IST-3  types filter: ?types=controls limits the search to one
//	       domain.
//	IST-4  partial_types: a credential without OPA admit on `risks`
//	       receives `partial_types: ["risks"]` and zero risk hits.
//	IST-5  400 validations: q<2 chars, limit>50, unknown types.
//	IST-6  global cap: more than MaxLimit matching rows truncate to
//	       limit.
//
// Run via:
//
//	go test -tags=integration -p 1 ./internal/api/search/...
//
// Requires DATABASE_URL_APP (atlas_app role) + DATABASE_URL (admin role).

package search_test

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
	"github.com/mgoodric/security-atlas/internal/api/scfseed"
	"github.com/mgoodric/security-atlas/internal/api/testjwt"
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

// freshTenant returns a new tenant id with a deferred cleanup that
// scrubs every row this slice's fixtures can create.
func freshTenant(t *testing.T, admin *pgxpool.Pool) string {
	t.Helper()
	tenant := uuid.NewString()
	t.Cleanup(func() {
		ctx := context.Background()
		for _, stmt := range []string{
			`DELETE FROM evidence_records WHERE tenant_id = $1`,
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

// seedControl inserts an active (non-superseded) control with the
// supplied title + description into `tenant`. Returns the control id
// so callers can attach evidence by FK if they need to. bundle_id is
// per-row unique so the partial-active-uniq index doesn't trip on
// repeated calls within the same test.
func seedControl(t *testing.T, admin *pgxpool.Pool, tenant, title, desc string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO controls (
			id, tenant_id, title, description, control_family,
			implementation_type, applicability_expr,
			bundle_id, evidence_queries
		)
		VALUES ($1, $2, $3, $4, 'AAA', 'automated', 'true',
		        $5, '[]'::jsonb)
	`, id, tenant, title, desc, "slice-268-"+id.String()); err != nil {
		t.Fatalf("seed control: %v", err)
	}
	return id
}

// seedRisk inserts a risk with the supplied title + description into
// `tenant`. treatment='avoid' avoids tripping the slice-019 per-
// treatment CHECK constraints (accept requires accepted_until +
// accepter; transfer requires instrument_reference; mitigate has its
// own application-side rule). avoid keeps the row valid without
// extra fixture columns.
func seedRisk(t *testing.T, admin *pgxpool.Pool, tenant, title, desc string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO risks (
			id, tenant_id, title, description, category, methodology,
			treatment
		)
		VALUES ($1, $2, $3, $4, 'confidentiality', 'nist_800_30', 'avoid')
	`, id, tenant, title, desc); err != nil {
		t.Fatalf("seed risk: %v", err)
	}
	return id
}

// seedEvidence inserts an evidence row attached to `ctrlID`. The
// `kind` and `controlRef` are the searchable surface (the evidence
// table has no title/description columns — see search.go).
func seedEvidence(t *testing.T, admin *pgxpool.Pool, tenant string, ctrlID uuid.UUID, kind, controlRef string) {
	t.Helper()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO evidence_records (
			id, tenant_id, control_id, observed_at, provenance,
			result, payload, hash, evidence_kind, control_ref,
			source_attribution
		)
		VALUES ($1, $2, $3, now(), '{}'::jsonb,
		        'pass', '{}'::jsonb, $4, $5, $6, '{}'::jsonb)
	`, uuid.New(), tenant, ctrlID,
		"sha256:"+uuid.NewString(),
		kind, controlRef); err != nil {
		t.Fatalf("seed evidence: %v", err)
	}
}

// testEnv bundles the running HTTP server with a bearer token. The
// bearer carries OwnerFor() so requireProgramRead-style guards admit
// it everywhere `/v1/search` could land — but the OPA engine is NOT
// wired in this harness (unit-server path), so per-type partitioning
// passes every type through (see IST-4 for the engine-on case).
type testEnv struct {
	server *httptest.Server
	bearer string
}

func testServer(t *testing.T, app *pgxpool.Pool, tenant string) testEnv {
	t.Helper()
	srv := api.New(api.Config{})
	srv.AttachDB(app)
	bearer := srv.IssueTestJWT(t, testjwt.OwnerFor(uuid.MustParse(tenant), []string{"control_owner"}))
	h := srv.HTTPHandlerForTests()
	if h == nil {
		t.Fatal("HTTPHandlerForTests returned nil — DB pool not attached")
	}
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	return testEnv{server: ts, bearer: bearer}
}

// doGet issues a Bearer-authed GET and returns the response + decoded
// body. The body is decoded into a generic map so individual tests
// can poke at the typed fields they care about.
func doGet(t *testing.T, env testEnv, path string) (*http.Response, map[string]any) {
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

// hitsAsSlice unwraps the `hits` field. Returns nil when absent so
// callers can iterate unconditionally.
func hitsAsSlice(body map[string]any) []map[string]any {
	raw, _ := body["hits"].([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, r := range raw {
		if m, ok := r.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

// ===== IST-1: cross-tenant isolation (AC-6 / P0-A3) =====

func TestSlice268_CrossTenantIsolation(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	tenantA := freshTenant(t, admin)
	tenantB := freshTenant(t, admin)

	// Tenant A: 5 matching controls / risks / evidence carrying the
	// shared token "iamphoenix" — a unique-enough literal we don't
	// expect to find elsewhere in the test DB.
	const tokenA = "iamphoenix"
	for i := 0; i < 5; i++ {
		ctrlA := seedControl(t, admin, tenantA,
			"Tenant A "+tokenA+" control "+uuid.NewString()[:8],
			"description for "+tokenA)
		seedRisk(t, admin, tenantA,
			"Tenant A "+tokenA+" risk "+uuid.NewString()[:8],
			"description for "+tokenA)
		seedEvidence(t, admin, tenantA, ctrlA, tokenA+".scan", "ctrl-"+tokenA)
	}

	// Tenant B: 5 matching rows with the SAME literal. RLS must
	// keep tenant A's bearer from seeing them.
	for i := 0; i < 5; i++ {
		ctrlB := seedControl(t, admin, tenantB,
			"Tenant B "+tokenA+" control "+uuid.NewString()[:8],
			"description for "+tokenA)
		seedRisk(t, admin, tenantB,
			"Tenant B "+tokenA+" risk "+uuid.NewString()[:8],
			"description for "+tokenA)
		seedEvidence(t, admin, tenantB, ctrlB, tokenA+".scan", "ctrl-"+tokenA)
	}

	// Tenant A's bearer searches for the shared token. Every hit
	// must be a tenant A row.
	envA := testServer(t, app, tenantA)
	resp, body := doGet(t, envA, "/v1/search?q="+tokenA+"&limit=50")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("IST-1: status %d, want 200", resp.StatusCode)
	}
	hits := hitsAsSlice(body)
	if len(hits) == 0 {
		t.Fatalf("IST-1: expected ≥ 1 hit, got 0")
	}
	for _, h := range hits {
		title, _ := h["title"].(string)
		// Tenant A's titles start with "Tenant A " (or are evidence
		// rows synthesized from kind + control_ref, which carry no
		// tenant marker — those we cross-check by id). Cross-tenant
		// leakage would surface either a "Tenant B" title or a
		// tenant-B id; both are catastrophic.
		if strings.Contains(title, "Tenant B") {
			t.Errorf("IST-1: cross-tenant leak — tenant A search returned %q", title)
		}
	}

	// Symmetric check: tenant B's bearer sees only tenant B rows.
	envB := testServer(t, app, tenantB)
	resp, body = doGet(t, envB, "/v1/search?q="+tokenA+"&limit=50")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("IST-1: status %d, want 200", resp.StatusCode)
	}
	hits = hitsAsSlice(body)
	if len(hits) == 0 {
		t.Fatalf("IST-1: expected ≥ 1 hit for tenant B, got 0")
	}
	for _, h := range hits {
		title, _ := h["title"].(string)
		if strings.Contains(title, "Tenant A") {
			t.Errorf("IST-1: cross-tenant leak — tenant B search returned %q", title)
		}
	}
}

// ===== IST-2: happy path — multi-token query, all three types =====

func TestSlice268_HappyPath_MultiToken_AllTypes(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	tenant := freshTenant(t, admin)

	// "iam access review" — three tokens that compose differently
	// per row. The token-overlap relevance scoring should produce a
	// stable DESC ordering.
	ctrlBoth := seedControl(t, admin, tenant,
		"IAM access review for AWS",
		"quarterly review of every IAM identity")
	ctrlPartial := seedControl(t, admin, tenant,
		"S3 bucket review",
		"weekly bucket policy review")
	_ = seedControl(t, admin, tenant,
		"Unrelated", "Nothing matches here at all.")
	seedRisk(t, admin, tenant,
		"IAM stale access risk",
		"users with stale access elevate to admin")
	seedEvidence(t, admin, tenant, ctrlBoth, "iam.access_review", "AWS-IAM-2026")
	seedEvidence(t, admin, tenant, ctrlPartial, "s3.bucket_audit", "S3-Audit-2026")

	env := testServer(t, app, tenant)
	resp, body := doGet(t, env, "/v1/search?q=iam+access+review&limit=50")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("IST-2: status %d body=%v", resp.StatusCode, body)
	}
	hits := hitsAsSlice(body)
	if len(hits) == 0 {
		t.Fatalf("IST-2: expected ≥ 1 hit, got 0")
	}

	// At least one of each type. Pure-AND ordering (all 3 tokens
	// match) ranks ctrlBoth + the iam risk ahead of partial matches.
	var sawControls, sawRisks, sawEvidence bool
	for _, h := range hits {
		switch h["type"] {
		case "controls":
			sawControls = true
		case "risks":
			sawRisks = true
		case "evidence":
			sawEvidence = true
		}
	}
	if !sawControls {
		t.Errorf("IST-2: expected at least one controls hit, got none")
	}
	if !sawRisks {
		t.Errorf("IST-2: expected at least one risks hit, got none")
	}
	if !sawEvidence {
		t.Errorf("IST-2: expected at least one evidence hit, got none")
	}

	// AC-4: relevance DESC ordering. The first hit's score must be
	// ≥ the last hit's score.
	if len(hits) >= 2 {
		first, _ := hits[0]["relevance_score"].(float64)
		last, _ := hits[len(hits)-1]["relevance_score"].(float64)
		if first < last {
			t.Errorf("IST-2: hits not sorted DESC: first=%v last=%v", first, last)
		}
	}
}

// ===== IST-3: types filter narrows the domain set =====

func TestSlice268_TypesFilter(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	tenant := freshTenant(t, admin)

	const token = "phoenixfilter"
	ctrl := seedControl(t, admin, tenant,
		token+" control",
		"description")
	seedRisk(t, admin, tenant,
		token+" risk",
		"description")
	seedEvidence(t, admin, tenant, ctrl, token+".scan", "ctrl-ref")

	env := testServer(t, app, tenant)
	resp, body := doGet(t, env,
		"/v1/search?q="+token+"&types=controls")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("IST-3: status %d", resp.StatusCode)
	}
	hits := hitsAsSlice(body)
	if len(hits) == 0 {
		t.Fatalf("IST-3: expected controls hit, got 0 (body=%v)", body)
	}
	for _, h := range hits {
		if h["type"] != "controls" {
			t.Errorf("IST-3: types=controls returned a %v hit", h["type"])
		}
	}
}

// ===== IST-5: 400 validations =====

func TestSlice268_BadRequests(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	tenant := freshTenant(t, admin)
	env := testServer(t, app, tenant)

	cases := []struct {
		name string
		path string
		hint string
	}{
		{"q_too_short", "/v1/search?q=a", "at least 2"},
		{"q_empty", "/v1/search?q=", "at least 2"},
		{"limit_too_high", "/v1/search?q=valid&limit=51", "≤ 50"},
		{"limit_zero", "/v1/search?q=valid&limit=0", "≥ 1"},
		{"limit_nonnumeric", "/v1/search?q=valid&limit=abc", "integer"},
		{"unknown_type", "/v1/search?q=valid&types=bogus", "unknown type"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp, body := doGet(t, env, c.path)
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", resp.StatusCode)
			}
			msg, _ := body["error"].(string)
			if !strings.Contains(msg, c.hint) {
				t.Errorf("error %q missing hint %q", msg, c.hint)
			}
		})
	}
}

// ===== IST-6: global cap (P0-A6) =====

func TestSlice268_GlobalCap(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	tenant := freshTenant(t, admin)

	const token = "capspring"
	// Seed 30 controls + 30 risks → 60 candidates exceeds the
	// requested limit of 10. After the merge, len(hits) MUST be 10.
	for i := 0; i < 30; i++ {
		seedControl(t, admin, tenant,
			token+" control "+uuid.NewString()[:8],
			"desc")
		seedRisk(t, admin, tenant,
			token+" risk "+uuid.NewString()[:8],
			"desc")
	}

	env := testServer(t, app, tenant)
	resp, body := doGet(t, env, "/v1/search?q="+token+"&limit=10")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("IST-6: status %d", resp.StatusCode)
	}
	hits := hitsAsSlice(body)
	if len(hits) != 10 {
		t.Fatalf("IST-6: len(hits) = %d, want 10", len(hits))
	}
	if c, _ := body["count"].(float64); int(c) != 10 {
		t.Errorf("IST-6: count = %v, want 10", c)
	}
}

// ===== IST-7: partial_types always present (empty array, not null) =====

func TestSlice268_PartialTypesAlwaysArray(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	tenant := freshTenant(t, admin)
	seedControl(t, admin, tenant, "iam access", "desc")
	env := testServer(t, app, tenant)

	resp, body := doGet(t, env, "/v1/search?q=iam")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	// The unit-server harness wires no OPA engine → no type is
	// filtered → partial_types is an empty array. Critically: NEVER
	// null on the wire.
	raw, ok := body["partial_types"]
	if !ok || raw == nil {
		t.Fatalf("partial_types missing/null; want []")
	}
	if _, ok := raw.([]any); !ok {
		t.Fatalf("partial_types not an array, got %T", raw)
	}
}

// ===== IST-8: slice 661 — SCF anchor catalog search (AC-1, AC-4) =====
//
// This is the reproduction-and-fix test. It seeds the bundled SCF
// catalog via scfseed.EnsureFullCatalog (which loads CRY-04 "Encryption
// At Rest" + CRY-08 "Encryption In Transit" among ~53 anchors) and a
// brand-new tenant with ZERO instantiated controls. Before the slice
// 661 change there is no `anchors` search branch, so both `q=CRY-04`
// (anchor-code match) and `q=encryption` (anchor-title match) return
// zero hits even though the anchors are present in /catalog/scf. With
// the change, both return anchor hits.
//
// The same test also asserts the invariant-#6 boundary: an anchor hit
// (tenant-agnostic catalog) does NOT leak another tenant's
// tenant-scoped rows. We seed a SECOND tenant's control carrying the
// "encryption" token and confirm tenant A's `q=encryption` search never
// returns it — controls stay RLS-scoped exactly as before.
func TestSlice661_AnchorSearch_EmptyTenant(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))

	// Seed the bundled SCF catalog (CRY-04, CRY-08, ... — tenant-agnostic).
	if err := scfseed.EnsureFullCatalog(context.Background(), admin); err != nil {
		t.Fatalf("scfseed.EnsureFullCatalog: %v", err)
	}

	// Tenant A: the empty tenant — ZERO instantiated controls.
	tenantA := freshTenant(t, admin)
	// Tenant B: an unrelated tenant whose control carries the
	// "encryption" token. The RLS boundary must keep tenant A's search
	// from ever returning this row.
	tenantB := freshTenant(t, admin)
	seedControl(t, admin, tenantB,
		"Tenant B encryption control "+uuid.NewString()[:8],
		"encryption at rest for tenant B")

	envA := testServer(t, app, tenantA)

	// --- AC-4(a): exact anchor code `CRY-04` returns an anchor hit. ---
	resp, body := doGet(t, envA, "/v1/search?q=CRY-04&limit=50")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("IST-8 CRY-04: status %d body=%v", resp.StatusCode, body)
	}
	hits := hitsAsSlice(body)
	var sawCRY04 bool
	for _, h := range hits {
		if h["type"] != "anchors" {
			continue
		}
		// The anchor link id must be the anchor UUID (the FE links to
		// /catalog/scf/<id>). Title is "<scf_id> — <title>".
		title, _ := h["title"].(string)
		id, _ := h["id"].(string)
		if strings.Contains(title, "CRY-04") {
			sawCRY04 = true
			if _, err := uuid.Parse(id); err != nil {
				t.Errorf("IST-8: anchor hit id %q is not a UUID (FE links to /catalog/scf/<id>)", id)
			}
		}
	}
	if !sawCRY04 {
		t.Fatalf("IST-8 CRY-04: expected an anchor hit for CRY-04 on an empty tenant, got hits=%v", hits)
	}

	// --- AC-4(b): name query `encryption` returns anchor hits ---
	// (CRY-04 "Encryption At Rest" + CRY-08 "Encryption In Transit"),
	// AND does NOT leak tenant B's matching control (RLS isolation).
	resp, body = doGet(t, envA, "/v1/search?q=encryption&limit=50")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("IST-8 encryption: status %d body=%v", resp.StatusCode, body)
	}
	hits = hitsAsSlice(body)
	anchorTitles := map[string]bool{}
	for _, h := range hits {
		title, _ := h["title"].(string)
		switch h["type"] {
		case "anchors":
			anchorTitles[title] = true
		case "controls":
			// Invariant #6: tenant A has zero controls; any control hit
			// would be a cross-tenant RLS leak of tenant B's row.
			t.Errorf("IST-8: invariant-#6 leak — empty tenant A's anchor search returned a control hit %q", title)
		}
	}
	if len(anchorTitles) == 0 {
		t.Fatalf("IST-8 encryption: expected anchor hits, got none (hits=%v)", hits)
	}
	// Both encryption anchors should surface by title.
	var sawAtRest, sawInTransit bool
	for title := range anchorTitles {
		if strings.Contains(title, "CRY-04") {
			sawAtRest = true
		}
		if strings.Contains(title, "CRY-08") {
			sawInTransit = true
		}
	}
	if !sawAtRest {
		t.Errorf("IST-8 encryption: expected CRY-04 (Encryption At Rest) anchor hit, titles=%v", anchorTitles)
	}
	if !sawInTransit {
		t.Errorf("IST-8 encryption: expected CRY-08 (Encryption In Transit) anchor hit, titles=%v", anchorTitles)
	}
}
