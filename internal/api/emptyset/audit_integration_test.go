//go:build integration

// Slice 150 — cross-cutting empty-set robustness AUDIT sweep. One test,
// many subtests, one per audited GET list/aggregate endpoint. The sweep
// runs against a freshly-issued tenant on a real Postgres + RLS stack
// (no mocks) so the contract is enforced end-to-end.
//
// The audit's purpose is to lock the post-fix invariant: every list /
// aggregate endpoint returns 200 with a well-shaped empty envelope on a
// fresh-install zero-row DB. A future regression that re-introduces a
// `rows[0]` access without bounds, a divide-by-zero on a rate
// calculation, or a nil-deref on an empty aggregation will fail this
// test before merge.
//
// New list endpoints land with a new sweep entry — see CONTRIBUTING.md
// ("Empty-set robustness").
//
// Per-package companions (the per-handler equivalents) live next to
// each handler:
//
//   - internal/api/freshnessdrift/empty_set_integration_test.go
//   - internal/api/policies/empty_set_integration_test.go
//   - internal/api/dashboard/empty_set_integration_test.go
//
// They pin per-package wire-shape contracts the sweep cannot make
// without knowing the per-endpoint envelope keys.

package emptyset_test

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
)

// openAppPool returns a connection pool for the application role, which
// the integration suite uses to exercise RLS-bound handlers exactly as
// production does. Tests skip when DATABASE_URL_APP is unset (CI sets
// both, local dev typically uses just-up's bootstrap).
func openAppPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL_APP")
	if dsn == "" {
		t.Skip("DATABASE_URL_APP not set; skipping integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(func() { pool.Close() })
	return pool
}

// TestAllListEndpoints_EmptyTenant_NeverReturn5xx is the slice-150
// cross-cutting sweep. The audit set was chosen by the slice-150
// engineer from a grep of the live route table in
// internal/api/httpserver.go and the slice-doc suspect list. Adding a
// new GET list endpoint to the platform is a constitutional commitment
// to also add a row to `cases` below — that is how the convention
// scales (CONTRIBUTING.md "Empty-set robustness").
//
// A subtest FAILS when:
//
//   - The handler returns 5xx (the bug we are preventing)
//   - The response is not valid JSON (a 200 with a partial body is the
//     half-broken shape the panel cannot render)
//
// A subtest PASSES on 200, 401 (unauth — surface-acceptable for
// endpoints that gate on a richer cred than the bootstrap owner), or
// 403 (forbidden — same rationale). 4xx is acceptable because the
// invariant is "never crash on empty"; an authorization decision is a
// separate concern.
func TestAllListEndpoints_EmptyTenant_NeverReturn5xx(t *testing.T) {
	app := openAppPool(t)
	srv := api.New(api.Config{})
	srv.AttachDB(app)
	tenant := uuid.NewString()
	_, bearer, err := srv.IssueBootstrapOwnerCredential(tenant, []string{"owner", "admin", "approver"})
	if err != nil {
		t.Fatalf("IssueBootstrapOwnerCredential: %v", err)
	}
	ts := httptest.NewServer(srv.HTTPHandlerForTests())
	t.Cleanup(ts.Close)

	// The audit set. Keep the list alphabetised by path so additions
	// are easy to merge. Comments at the right edge cite the slice
	// the endpoint was introduced by. Paths reflect the LIVE routes
	// registered in internal/api/httpserver.go (and the per-package
	// RegisterRoutes methods it calls) — not every domain noun has a
	// bare-list endpoint.
	cases := []string{
		"/v1/activity",                // slice 066 dashboard activity feed
		"/v1/admin/audit-log",         // slice 062 admin audit log
		"/v1/admin/audit-log/unified", // slice 081 unified audit log
		// /v1/admin/credentials is intentionally OMITTED: the route
		// only mounts when s.apikeyStore != nil, which the
		// api.New(api.Config{}) test harness does not provide. The
		// admincreds/integration_test.go in that package owns the
		// empty-set contract for that route under its real wiring.
		"/v1/admin/features",                  // slice 060 features toggle list
		"/v1/admin/users",                     // slice 060 user admin
		"/v1/aggregation-rules",               // slice 020 risk aggregation rules
		"/v1/anchors",                         // slice 006 SCF anchors
		"/v1/audit-notes",                     // slice 029 audit notes
		"/v1/audit-periods",                   // slice 028 audit periods
		"/v1/board-briefs",                    // slice 031 board briefs list
		"/v1/controls/drift?since=7d",         // slice 016 drift panel
		"/v1/decisions",                       // slice 091 decisions list
		"/v1/decisions/overdue",               // slice 091 overdue decisions
		"/v1/evidence",                        // slice 013 evidence ledger reads
		"/v1/evidence/freshness?bucket=class", // slice 016 freshness distribution
		"/v1/exceptions",                      // slice 021 exceptions list
		"/v1/exceptions/expiring?within=30d",  // slice 021 expiring rollup
		"/v1/framework-scopes",                // slice 018 framework scopes
		"/v1/frameworks",                      // slice 006 frameworks list
		"/v1/frameworks/posture",              // slice 066 framework posture
		"/v1/me/acknowledgments",              // slice 023 my acknowledgments
		"/v1/me/audit-periods",                // slice 028 my audit periods
		"/v1/me/notifications",                // slice 060 my notifications
		"/v1/metrics",                         // slice 076 metrics catalog
		"/v1/metrics/cascade",                 // slice 076 metrics cascade
		"/v1/org_units",                       // slice 060 org units
		"/v1/policies",                        // slice 022 policies list
		"/v1/policies?include=ack_rate",       // slice 107 joined ack-rate path (hot path)
		"/v1/risks",                           // slice 019 risks list
		"/v1/risks?treatment=mitigate",        // slice 066 mitigate-risks dashboard path
		"/v1/risks/heatmap",                   // slice 019 heatmap aggregation
		"/v1/risks/theme-heatmap",             // slice 019 theme heatmap aggregation
		"/v1/scopes/cells",                    // slice 017 scope cells
		"/v1/scopes/dimensions",               // slice 017 scope dimensions
		"/v1/themes",                          // slice 019 risk themes
		"/v1/upcoming",                        // slice 066 upcoming rollup
		"/v1/vendors",                         // slice 024 vendor lite
		"/v1/vendors/burndown",                // slice 024 vendor burndown
		"/v1/walkthroughs",                    // slice 027 walkthroughs list
	}

	for _, path := range cases {
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
			body, _ := readAll(resp)
			if resp.StatusCode >= 500 {
				t.Fatalf("status = %d, want < 500; body=%s", resp.StatusCode, body)
			}
			if !json.Valid(body) && len(body) > 0 {
				t.Fatalf("response body is not valid JSON: %s", body)
			}
			// Lock the empty-envelope contract for the well-behaved
			// 200 path. We don't gate on a specific top-level key
			// here (the per-package tests do that); only on the
			// "never null where an array is expected" shape.
			if resp.StatusCode == http.StatusOK {
				var parsed map[string]any
				_ = json.Unmarshal(body, &parsed)
				for k, v := range parsed {
					if v == nil && looksLikeArrayKey(k) {
						t.Errorf("field %q in %s is null on empty path; want []",
							k, path)
					}
				}
			}
		})
	}
}

// readAll reads the entire response body. Pulled out so the test loop
// stays readable.
func readAll(r *http.Response) ([]byte, error) {
	const max = 1 << 20 // 1 MiB ceiling — list responses are small
	buf := make([]byte, 0, 512)
	tmp := make([]byte, 8192)
	for {
		n, err := r.Body.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
			if len(buf) > max {
				return buf[:max], nil
			}
		}
		if err != nil {
			return buf, nil
		}
	}
}

// looksLikeArrayKey reports whether a top-level response field name
// implies the value should be a JSON array. Used by the
// never-null-where-array assertion. The heuristic is intentionally
// conservative — it catches the obvious shape ("items", "policies",
// "controls") and skips ambiguous singletons.
func looksLikeArrayKey(k string) bool {
	switch k {
	case "items", "rows", "results", "data", "list",
		"policies", "controls", "risks", "vendors", "scopes",
		"frameworks", "anchors", "users", "evidence", "exceptions",
		"audit_periods", "briefs", "metrics", "buckets", "activity",
		"upcoming", "walkthroughs", "flipped_out", "nodes":
		return true
	}
	// Plural-ish: trailing 's' on a noun-looking key. Excludes obvious
	// singulars ("address", "status", "stats", "config", "alias").
	if !strings.HasSuffix(k, "s") {
		return false
	}
	for _, suffix := range []string{"status", "config", "stats", "alias", "address", "next_cursor"} {
		if k == suffix {
			return false
		}
	}
	return false // conservative — explicit list above is the authority
}
