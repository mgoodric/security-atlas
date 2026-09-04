//go:build integration

// Slice 150 — empty-set robustness integration test for the dashboard
// HTTP API. The slice-040 dashboard panel ("Could not load board
// metrics · 500 Internal Server Error") consumes /v1/frameworks/posture,
// /v1/activity, /v1/upcoming — every one of those must return 200 with
// an empty envelope on a fresh-install zero-row DB.
//
// See docs/issues/150-empty-set-robustness-audit-across-list-endpoints.md.

package dashboard_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api"
	"github.com/mgoodric/security-atlas/internal/api/testjwt"
	"github.com/mgoodric/security-atlas/internal/dbtest"
)

// TestDashboard_EmptyTenant_AllPanelsReturn200 is the slice-150 reproducer
// for the operator-reported dashboard 500s on a fresh install. Hits every
// dashboard panel-backing endpoint and asserts 200 + a well-shaped empty
// envelope (array, not null). The panel-shape contract is what the
// frontend iterates — null breaks the panel render.
func TestDashboard_EmptyTenant_AllPanelsReturn200(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	// No data is seeded, but the cleanup stays defensive in case a parallel
	// test re-uses the tenant id (it won't — SeedTenant mints a fresh UUID —
	// but the idiom matches the rest of the suite).
	tenant := dbtest.SeedTenant(t, admin,
		"control_evaluations",
		"evidence_records",
	)

	srv := api.New(api.Config{})
	srv.AttachDB(app)
	// Slice 197: JWT bearer via slice 190 path (owner roles).
	bearer := srv.IssueTestJWT(t, testjwt.OwnerFor(uuid.MustParse(tenant), []string{"owner"}))
	ts := httptest.NewServer(srv.HTTPHandlerForTests())
	t.Cleanup(ts.Close)

	cases := []struct {
		path string
		// arrayKey is the top-level JSON field that holds the rows
		// array. Per the slice-150 wire contract, this MUST be a
		// JSON array (even when empty), never null — the frontend
		// dashboards iterate it directly.
		arrayKey string
		// tenantScoped reports whether the array contents are
		// tenant-scoped (and therefore empty for a fresh tenant).
		// /v1/frameworks/posture surfaces the platform-shared
		// framework_versions table when present in the DB, so on a
		// shared test database the row count may be non-zero even
		// for a brand-new tenant. The 200 + JSON-array contract
		// still holds; only the strict length-zero assertion is
		// suppressed for the platform-shared shape.
		tenantScoped bool
	}{
		{"/v1/frameworks/posture", "frameworks", false},
		{"/v1/activity", "activity", true},
		{"/v1/upcoming", "upcoming", true},
	}

	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodGet, ts.URL+tc.path, nil)
			if err != nil {
				t.Fatalf("new request: %v", err)
			}
			req.Header.Set("Authorization", "Bearer "+bearer)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("GET %s: %v", tc.path, err)
			}
			defer resp.Body.Close()
			var body map[string]any
			_ = json.NewDecoder(resp.Body).Decode(&body)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, want 200; body=%v", resp.StatusCode, body)
			}
			arr, ok := body[tc.arrayKey].([]any)
			if !ok {
				t.Fatalf("%s is not a JSON array: %T (%v)", tc.arrayKey, body[tc.arrayKey], body[tc.arrayKey])
			}
			if tc.tenantScoped {
				if len(arr) != 0 {
					t.Errorf("%s length = %d, want 0", tc.arrayKey, len(arr))
				}
				if got, want := body["count"], float64(0); got != want {
					t.Errorf("count = %v, want %v", got, want)
				}
			}
		})
	}
}
