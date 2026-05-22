//go:build integration

// Slice 067 — integration tests for the additive richer filters on
// GET /v1/decisions. Real Postgres + the assembled platform router so the
// tests exercise the full request path (program-read authz guard, tenancy
// middleware, RLS, the decision.Store in-memory filter composition).
//
// Run with:
//
//	go test -tags=integration -race ./internal/api/decisions/...
//
// Requires DATABASE_URL_APP (atlas_app role) and DATABASE_URL (admin role).
//
// Coverage:
//
//	ISC-29  (decisions arm) ?constraints= / ?decision_maker= /
//	        ?revisit_by_from= / ?revisit_by_to= filters work and compose
//	        with each other and with the slice-055 ?status= filter;
//	        the endpoint is RLS-isolated across tenants and 403s a role
//	        without program-read access.

package decisions_test

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
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	decisionsapi "github.com/mgoodric/security-atlas/internal/api/decisions"
	"github.com/mgoodric/security-atlas/internal/api/tenancymw"
	"github.com/mgoodric/security-atlas/internal/api/testjwt"
	"github.com/mgoodric/security-atlas/internal/decision"
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
		if _, err := admin.Exec(context.Background(),
			`DELETE FROM decisions WHERE tenant_id = $1`, tenant); err != nil {
			t.Logf("cleanup decisions: %v", err)
		}
	})
	return tenant
}

// seedDecision inserts a decision row directly (admin/BYPASSRLS) so the
// filter tests can pin constraints / decision_maker / revisit_by / status
// deterministically. revisitBy is optional (nil -> NULL).
func seedDecision(t *testing.T, admin *pgxpool.Pool, tenant, decisionID, maker string, constraints []string, status string, revisitBy *time.Time) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO decisions (
			id, tenant_id, decision_id, title, narrative, constraints,
			tradeoffs, decision_maker, decided_at, revisit_by, status
		)
		VALUES ($1, $2, $3, 'slice 067 decision', '', $4::text[],
		        '', $5, now(), $6, $7)
	`, id, tenant, decisionID, constraints, maker, revisitBy, status); err != nil {
		t.Fatalf("seed decision: %v", err)
	}
	return id
}

type testEnv struct {
	server *httptest.Server
	bearer string
}

func testServer(t *testing.T, app *pgxpool.Pool, tenant string) testEnv {
	t.Helper()
	srv := api.New(api.Config{})
	srv.AttachDB(app)
	// Slice 197: JWT bearer via slice 190 path (owner roles).
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
	req, _ := http.NewRequest(http.MethodGet, env.server.URL+path, nil)
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

// noRoleRouter wires GET /v1/decisions behind a credential carrying NO
// program-read signal — the v1 representation of a viewer-only
// credential. It exercises the handler-level requireProgramRead guard
// without standing up OPA.
func noRoleRouter(t *testing.T, app *pgxpool.Pool, tenant string) http.Handler {
	t.Helper()
	h := decisionsapi.New(decision.NewStore(app))
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
	r.Get("/v1/decisions", h.ListDecisions)
	return r
}

// idsOf collects the decision_id field of every row in a list response.
func idsOf(body map[string]any) map[string]bool {
	out := map[string]bool{}
	rows, _ := body["decisions"].([]any)
	for _, r := range rows {
		m := r.(map[string]any)
		if did, ok := m["decision_id"].(string); ok {
			out[did] = true
		}
	}
	return out
}

// ===== ISC-29: richer filters work and compose =====

func TestListDecisions_RicherFiltersComposeAndWork(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	tenant := freshTenant(t, admin)
	env := testServer(t, app, tenant)

	jan := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)
	jun := time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)
	dec := time.Date(2026, 12, 15, 0, 0, 0, 0, time.UTC)

	// D1: alice, [time-pressure, cost], active, revisit Jan.
	seedDecision(t, admin, tenant, "DL-067-0001", "alice",
		[]string{"time-pressure", "cost"}, "active", &jan)
	// D2: alice, [cost], active, revisit Jun.
	seedDecision(t, admin, tenant, "DL-067-0002", "alice",
		[]string{"cost"}, "active", &jun)
	// D3: bob, [dependency-blocked], active, revisit Dec.
	seedDecision(t, admin, tenant, "DL-067-0003", "bob",
		[]string{"dependency-blocked"}, "active", &dec)
	// D4: bob, [time-pressure], revisited, no revisit_by.
	seedDecision(t, admin, tenant, "DL-067-0004", "bob",
		[]string{"time-pressure"}, "revisited", nil)

	// ?constraints=time-pressure -> D1 + D4 (OR-within-facet, array intersect).
	resp, body := get(t, env, "/v1/decisions?constraints=time-pressure")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ISC-29: status %d", resp.StatusCode)
	}
	got := idsOf(body)
	if len(got) != 2 || !got["DL-067-0001"] || !got["DL-067-0004"] {
		t.Fatalf("ISC-29: ?constraints=time-pressure = %v, want D1+D4", got)
	}

	// ?constraints=time-pressure,dependency-blocked -> D1 + D3 + D4 (any match).
	_, body = get(t, env, "/v1/decisions?constraints=time-pressure,dependency-blocked")
	got = idsOf(body)
	if len(got) != 3 {
		t.Fatalf("ISC-29: multi-constraint = %v, want 3 rows", got)
	}

	// ?decision_maker=alice -> D1 + D2.
	_, body = get(t, env, "/v1/decisions?decision_maker=alice")
	got = idsOf(body)
	if len(got) != 2 || !got["DL-067-0001"] || !got["DL-067-0002"] {
		t.Fatalf("ISC-29: ?decision_maker=alice = %v, want D1+D2", got)
	}

	// ?revisit_by_from=2026-03-01&revisit_by_to=2026-09-01 -> D2 only
	// (D1's Jan is before the window, D3's Dec is after, D4 has none).
	_, body = get(t, env, "/v1/decisions?revisit_by_from=2026-03-01&revisit_by_to=2026-09-01")
	got = idsOf(body)
	if len(got) != 1 || !got["DL-067-0002"] {
		t.Fatalf("ISC-29: revisit_by range = %v, want D2", got)
	}

	// Compose: ?decision_maker=alice&constraints=cost -> D1 + D2 (both
	// alice, both carry cost).
	_, body = get(t, env, "/v1/decisions?decision_maker=alice&constraints=cost")
	got = idsOf(body)
	if len(got) != 2 {
		t.Fatalf("ISC-29: alice+cost = %v, want D1+D2", got)
	}

	// Compose with the slice-055 ?status= filter: status=active +
	// constraints=time-pressure -> D1 only (D4 carries time-pressure but
	// is revisited, not active).
	_, body = get(t, env, "/v1/decisions?status=active&constraints=time-pressure")
	got = idsOf(body)
	if len(got) != 1 || !got["DL-067-0001"] {
		t.Fatalf("ISC-29: status=active+constraints=time-pressure = %v, want D1", got)
	}

	// A malformed revisit_by_from is a 400.
	resp, _ = get(t, env, "/v1/decisions?revisit_by_from=not-a-date")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("ISC-29: malformed revisit_by_from = %d, want 400", resp.StatusCode)
	}
}

// ===== ISC-29: RLS isolation across tenants =====

func TestListDecisions_RLSIsolatedAcrossTenants(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	tenantA := freshTenant(t, admin)
	tenantB := freshTenant(t, admin)

	seedDecision(t, admin, tenantA, "DL-A-0001", "alice",
		[]string{"cost"}, "active", nil)
	seedDecision(t, admin, tenantB, "DL-B-0001", "bob",
		[]string{"cost"}, "active", nil)

	// Tenant B's bearer with the same ?constraints=cost filter sees only
	// tenant B's decision — RLS scopes the underlying SELECT.
	envB := testServer(t, app, tenantB)
	_, body := get(t, envB, "/v1/decisions?constraints=cost")
	got := idsOf(body)
	if len(got) != 1 || !got["DL-B-0001"] {
		t.Fatalf("ISC-29: tenant B saw %v, want only DL-B-0001", got)
	}
}

// ===== ISC-29: 403 for a role without program-read access =====

func TestListDecisions_403WithoutProgramRead(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	tenant := freshTenant(t, admin)
	r := noRoleRouter(t, app, tenant)
	ts := httptest.NewServer(r)
	t.Cleanup(ts.Close)

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/v1/decisions", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /v1/decisions: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("ISC-29: GET /v1/decisions status %d, want 403", resp.StatusCode)
	}
}
