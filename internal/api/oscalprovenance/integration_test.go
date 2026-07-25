//go:build integration

// Slice 599 — integration tests for the OSCAL resolved-chain provenance
// read endpoint. Real Postgres + the assembled platform router, so the test
// exercises the full request path: tenancy middleware, RLS, the sqlc query
// layer. The read NEVER touches the compliance-trestle bridge — the
// provenance is seeded directly as DB rows (mirroring how slice 578 persists
// it: an imported_catalogs profile baseline + a `profile_imported`
// audit-log row carrying the chain in detail JSON), so the read path is
// bridge-free and runs in CI without the Python oscal-bridge.
//
// Run with:
//
//	go test -tags=integration -race ./internal/api/oscalprovenance/...
//
// Requires DATABASE_URL_APP (atlas_app role) and DATABASE_URL (admin role).
//
// Coverage:
//
//	AC-1  a chained import returns its ordered chain (entry-profile, profile, catalog)
//	AC-2  a single-level import returns its two-element [entry-profile, catalog] chain
//	AC-3  the read is RLS-isolated: tenant A cannot read tenant B's provenance (404)
//	(plus) an unknown id 404s; a no-role credential 403s
package oscalprovenance_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api"
	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/api/oscalprovenance"
	"github.com/mgoodric/security-atlas/internal/api/tenancymw"
	"github.com/mgoodric/security-atlas/internal/api/testjwt"
	"github.com/mgoodric/security-atlas/internal/dbtest"
)

// ----- harness -----

// freshTenant returns a new tenant id and registers a cleanup that deletes
// every row this slice's tests can create under it.
func freshTenant(t *testing.T, admin *pgxpool.Pool) string {
	t.Helper()
	return dbtest.SeedTenant(t, admin,
		"imported_catalog_audit_log",
		"imported_catalogs",
	)
}

// chainSeed is one {role, sha256, bytes} entry in a seeded provenance chain.
type chainSeed struct {
	Role   string `json:"role"`
	Sha256 string `json:"sha256"`
	Bytes  int    `json:"bytes"`
}

// seedProfileBaseline inserts an imported_catalogs PROFILE row + a
// `profile_imported` audit-log row carrying the chain in detail JSON,
// exactly as slice 578's persist() does. It returns the baseline id. The
// sha256 columns must match the table's ^[0-9a-f]{64}$ CHECK.
func seedProfileBaseline(t *testing.T, admin *pgxpool.Pool, tenant string, chain []chainSeed, depth int) uuid.UUID {
	t.Helper()
	baselineID := uuid.New()
	const sha = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO imported_catalogs (
			id, tenant_id, source, kind, imported_by, source_sha256,
			source_label, oscal_version, catalog_title, profile_title,
			control_count
		)
		VALUES ($1, $2, 'oscal-profile-import', 'profile', 'test-importer',
		        $3, 'FedRAMP Moderate', '1.1.2', '', 'FedRAMP Moderate Baseline',
		        0)
	`, baselineID, tenant, sha); err != nil {
		t.Fatalf("seed profile baseline: %v", err)
	}

	detail, _ := json.Marshal(map[string]any{
		"mapped":      0,
		"unmapped":    0,
		"kind":        "profile",
		"chain":       chain,
		"chain_depth": depth,
	})
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO imported_catalog_audit_log (
			id, tenant_id, catalog_id, action, actor, source_sha256,
			source_label, control_count, detail
		)
		VALUES ($1, $2, $3, 'profile_imported', 'test-importer', $4,
		        'FedRAMP Moderate', 0, $5)
	`, uuid.New(), tenant, baselineID, sha, detail); err != nil {
		t.Fatalf("seed audit log: %v", err)
	}
	return baselineID
}

// testEnv bundles the running server with a bearer token bound to the tenant.
type testEnv struct {
	server *httptest.Server
	bearer string
}

// testServer assembles the full platform router with an owner credential —
// owner credentials carry OwnerRoles, so requireOscalRead admits them.
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

func provURL(id uuid.UUID) string {
	return "/v1/oscal/imported-profiles/" + id.String() + "/provenance"
}

// ===== AC-1: a chained import returns its ordered chain =====

func TestProvenance_ChainedImport(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	env := testServer(t, app, tenant)

	chain := []chainSeed{
		{Role: "entry-profile", Sha256: "aa", Bytes: 100},
		{Role: "profile", Sha256: "bb", Bytes: 200},
		{Role: "catalog", Sha256: "cc", Bytes: 300},
	}
	baseline := seedProfileBaseline(t, admin, tenant, chain, 2)

	resp, body := get(t, env, provURL(baseline))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200; body=%v", resp.StatusCode, body)
	}
	if got := body["baseline_id"]; got != baseline.String() {
		t.Errorf("baseline_id = %v, want %v", got, baseline.String())
	}
	if d, _ := body["chain_depth"].(float64); int(d) != 2 {
		t.Errorf("chain_depth = %v, want 2", body["chain_depth"])
	}
	rows, _ := body["chain"].([]any)
	if len(rows) != 3 {
		t.Fatalf("AC-1: chain len = %d, want 3", len(rows))
	}
	wantRoles := []string{"entry-profile", "profile", "catalog"}
	for i, raw := range rows {
		link := raw.(map[string]any)
		if link["role"] != wantRoles[i] {
			t.Errorf("chain[%d].role = %v, want %q", i, link["role"], wantRoles[i])
		}
	}
}

// ===== AC-2: a single-level import returns its two-element chain =====

func TestProvenance_SingleLevelImport(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	env := testServer(t, app, tenant)

	chain := []chainSeed{
		{Role: "entry-profile", Sha256: "11", Bytes: 50},
		{Role: "catalog", Sha256: "22", Bytes: 500},
	}
	baseline := seedProfileBaseline(t, admin, tenant, chain, 1)

	resp, body := get(t, env, provURL(baseline))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200", resp.StatusCode)
	}
	rows, _ := body["chain"].([]any)
	if len(rows) != 2 {
		t.Fatalf("AC-2: chain len = %d, want 2 (entry-profile + catalog)", len(rows))
	}
	first := rows[0].(map[string]any)
	last := rows[1].(map[string]any)
	if first["role"] != "entry-profile" || last["role"] != "catalog" {
		t.Errorf("AC-2: two-element chain roles wrong: %v", rows)
	}
}

// ===== AC-3: the read is RLS-isolated across tenants =====

func TestProvenance_RLSIsolation(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)

	tenantA := freshTenant(t, admin)
	tenantB := freshTenant(t, admin)

	// A baseline owned by tenant B.
	chain := []chainSeed{
		{Role: "entry-profile", Sha256: "aa", Bytes: 100},
		{Role: "catalog", Sha256: "bb", Bytes: 200},
	}
	baselineB := seedProfileBaseline(t, admin, tenantB, chain, 1)

	// Tenant A's bearer must NOT be able to read tenant B's provenance.
	envA := testServer(t, app, tenantA)
	resp, _ := get(t, envA, provURL(baselineB))
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("AC-3: tenant A reading tenant B's provenance: status %d, want 404", resp.StatusCode)
	}

	// Sanity: tenant B CAN read its own.
	envB := testServer(t, app, tenantB)
	resp, _ = get(t, envB, provURL(baselineB))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("AC-3 sanity: tenant B reading its own provenance: status %d, want 200", resp.StatusCode)
	}
}

// ===== plus: an unknown id 404s =====

func TestProvenance_UnknownID(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	env := testServer(t, app, tenant)

	resp, _ := get(t, env, provURL(uuid.New()))
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown id: status %d, want 404", resp.StatusCode)
	}
}

// ===== plus: a no-role credential 403s (handler-level guard) =====

func TestProvenance_Forbidden_NoRole(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)

	chain := []chainSeed{{Role: "entry-profile", Sha256: "aa", Bytes: 1}, {Role: "catalog", Sha256: "bb", Bytes: 2}}
	baseline := seedProfileBaseline(t, admin, tenant, chain, 1)

	// A router wiring the route behind a credential with NO oscal-read signal.
	h := oscalprovenance.New(oscalprovenance.NewStore(app))
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := authctx.WithCredential(req.Context(), credstore.Credential{
				TenantID: tenant,
				UserID:   "viewer-test",
			})
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	r.Use(tenancymw.Middleware)
	r.Get("/v1/oscal/imported-profiles/{id}/provenance", h.Provenance)
	ts := httptest.NewServer(r)
	t.Cleanup(ts.Close)

	req, _ := http.NewRequest(http.MethodGet, ts.URL+provURL(baseline), nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("no-role credential: status %d, want 403", resp.StatusCode)
	}
}
