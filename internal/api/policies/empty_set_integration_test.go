//go:build integration

// Slice 150 — empty-set robustness integration test for the policies HTTP
// API. Reproduces the operator-reported v1.10.0 fresh-install 500 on
// GET /v1/policies and pins the post-fix invariant: every list endpoint
// MUST return 200 with an empty envelope on a zero-row DB, never a 500.
// See docs/issues/150-empty-set-robustness-audit-across-list-endpoints.md.
//
// The test reuses the slice-022 ack_rate integration harness pattern
// (admin/app pool helpers) but only does the HTTP layer — no policy data
// is seeded.

package policies_test

import (
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
	"github.com/mgoodric/security-atlas/internal/api/testjwt"
)

func emptyAppDSN(t *testing.T) string {
	t.Helper()
	v := os.Getenv("DATABASE_URL_APP")
	if v == "" {
		t.Skip("DATABASE_URL_APP not set; skipping integration test")
	}
	return v
}

func emptyAdminDSN(t *testing.T) string {
	t.Helper()
	v := os.Getenv("DATABASE_URL")
	if v == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}
	return v
}

func emptyOpenPool(t *testing.T, dsn string) *pgxpool.Pool {
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

// TestListPolicies_EmptyTenant_Returns200EmptyEnvelope is the slice-150
// reproducer for the operator-reported "Could not load policies · 500
// Internal Server Error" on a fresh install. Asserts:
//
//   - 200 OK (NOT 500)
//   - body.policies is a JSON array (not null)
//   - body.policies length 0
//   - body.count 0
//
// Both the plain `?status=` path and the slice-107 `?include=ack_rate`
// path are exercised — the dashboard list view hard-codes
// `?include=ack_rate` (web/lib/api.ts listPolicies) so the joined-row
// path is on the hot frontend path.
func TestListPolicies_EmptyTenant_Returns200EmptyEnvelope(t *testing.T) {
	admin := emptyOpenPool(t, emptyAdminDSN(t))
	app := emptyOpenPool(t, emptyAppDSN(t))
	tenant := uuid.NewString()
	t.Cleanup(func() {
		ctx := context.Background()
		for _, stmt := range []string{
			`DELETE FROM policy_acknowledgments WHERE tenant_id = $1`,
			`DELETE FROM policies WHERE tenant_id = $1`,
		} {
			if _, err := admin.Exec(ctx, stmt, tenant); err != nil {
				t.Logf("cleanup %s: %v", stmt, err)
			}
		}
	})

	srv := api.New(api.Config{})
	srv.AttachDB(app)
	// Slice 197: JWT bearer via slice 190 path (owner roles).
	bearer := srv.IssueTestJWT(t, testjwt.OwnerFor(uuid.MustParse(tenant), []string{"owner"}))
	ts := httptest.NewServer(srv.HTTPHandlerForTests())
	t.Cleanup(ts.Close)

	for _, path := range []string{
		"/v1/policies",
		"/v1/policies?include=ack_rate",
	} {
		t.Run(path, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodGet, ts.URL+path, nil)
			if err != nil {
				t.Fatalf("new request: %v", err)
			}
			req.Header.Set("Authorization", "Bearer "+bearer)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("GET %s: %v", path, err)
			}
			defer resp.Body.Close()
			var body map[string]any
			_ = json.NewDecoder(resp.Body).Decode(&body)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, want 200; body=%v", resp.StatusCode, body)
			}
			arr, ok := body["policies"].([]any)
			if !ok {
				t.Fatalf("policies is not a JSON array: %T (%v)", body["policies"], body["policies"])
			}
			if len(arr) != 0 {
				t.Errorf("policies length = %d, want 0", len(arr))
			}
			if got, want := body["count"], float64(0); got != want {
				t.Errorf("count = %v, want %v", got, want)
			}
		})
	}
}
