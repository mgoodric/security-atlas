//go:build integration

// #2556: a viewer reads the risk register and never writes it, through the
// assembled router with the real OPA bundle and a live user_roles row
// (liveroletest). The no-DB matrix over every route and role is
// internal/authz/risk_reads_2556_test.go; this file shows the same answer
// end to end, handler gates included.
//
// Run with:  go test -tags=integration -run 2556 ./internal/api/risks/...

package risks_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api"
	"github.com/mgoodric/security-atlas/internal/api/liveroletest"
	"github.com/mgoodric/security-atlas/internal/dbtest"
)

func TestViewerReads2556_ReadsEveryRiskRouteWritesNone(t *testing.T) {
	t.Parallel()
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshHierarchyTenant(t, admin)
	unit := seedOrgUnit(t, admin, tenant, "Platform 2556", "team")
	risk := seedHierRisk(t, admin, tenant, &unit, []string{}, 3, 4).String()

	srv := api.New(api.Config{})
	srv.AttachDB(app)
	liveroletest.Attach(t, srv, app)
	viewer := liveroletest.Bearer(t, srv, tenant, "viewer")
	ts := httptest.NewServer(srv.HTTPHandlerForTests())
	t.Cleanup(ts.Close)

	do := func(method, path string) (int, string) {
		t.Helper()
		var body io.Reader
		if method != http.MethodGet {
			body = strings.NewReader(`{}`)
		}
		req, _ := http.NewRequest(method, ts.URL+path, body)
		req.Header.Set("Authorization", "Bearer "+viewer)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}

	// Reads: 200 where the route answers for any tenant, and never a
	// refusal where the id names nothing (404 is past both gates).
	missing := uuid.NewString()
	for _, path := range []string{
		"/v1/risks",
		"/v1/risks/heatmap",
		"/v1/risks/theme-heatmap",
		"/v1/risks/categories",
		"/v1/risks/review-cadence",
		"/v1/risks/export?format=csv",
		"/v1/risks/" + risk,
		"/v1/risks/" + risk + "/children",
		"/v1/risks/" + risk + "/parents",
		"/v1/risks/" + risk + "/audit-log",
		"/v1/themes",
		"/v1/org_units",
		"/v1/org_units/" + unit.String(),
		"/v1/decisions",
		"/v1/decisions/overdue",
	} {
		if code, body := do(http.MethodGet, path); code != http.StatusOK {
			t.Errorf("viewer GET %s = %d %s, want 200", path, code, body)
		}
	}
	// The seeded risk is not a roll-up, so its aggregation is a 404.
	for _, path := range []string{
		"/v1/risks/" + risk + "/aggregation",
		"/v1/decisions/" + missing,
		"/v1/decisions/" + missing + "/audit-log",
	} {
		if code, body := do(http.MethodGet, path); code == http.StatusForbidden || code == http.StatusUnauthorized {
			t.Errorf("viewer GET %s = %d %s, want past the gates", path, code, body)
		}
	}

	// Writes: 403 on every one.
	for _, w := range []struct{ method, path string }{
		{http.MethodPost, "/v1/risks"},
		{http.MethodPost, "/v1/risks/categories"},
		{http.MethodPatch, "/v1/risks/categories/c1"},
		{http.MethodDelete, "/v1/risks/categories/c1"},
		{http.MethodPost, "/v1/risks/categories/c1/archive"},
		{http.MethodPost, "/v1/risks/categories/c1/unarchive"},
		{http.MethodPost, "/v1/risks/categories/c1/set-default"},
		{http.MethodPost, "/v1/risks/aggregate"},
		{http.MethodPost, "/v1/risks/" + risk + "/controls"},
		{http.MethodDelete, "/v1/risks/" + risk + "/controls/" + missing},
		{http.MethodPost, "/v1/risks/" + risk + "/children"},
		{http.MethodDelete, "/v1/risks/" + risk + "/children/" + missing},
		{http.MethodPatch, "/v1/risks/" + risk + "/treatment"},
		{http.MethodPatch, "/v1/risks/" + risk + "/acceptance/propose"},
		{http.MethodPatch, "/v1/risks/" + risk + "/acceptance/approve"},
		{http.MethodPatch, "/v1/risks/" + risk + "/acceptance/reject"},
		{http.MethodPatch, "/v1/risks/" + risk + "/acceptance/revoke"},
		{http.MethodPatch, "/v1/risks/" + risk + "/close"},
		{http.MethodPatch, "/v1/risks/" + risk + "/reopen"},
		{http.MethodPatch, "/v1/risks/" + risk},
		{http.MethodDelete, "/v1/risks/" + risk},
		{http.MethodPost, "/v1/themes"},
		{http.MethodPatch, "/v1/themes/t1"},
		{http.MethodDelete, "/v1/themes/t1"},
		{http.MethodPost, "/v1/risks/" + risk + "/themes"},
		{http.MethodDelete, "/v1/risks/" + risk + "/themes/t1"},
		{http.MethodPost, "/v1/org_units"},
		{http.MethodPatch, "/v1/org_units/" + unit.String()},
		{http.MethodDelete, "/v1/org_units/" + unit.String()},
		{http.MethodPost, "/v1/decisions"},
		{http.MethodPatch, "/v1/decisions/" + missing},
		{http.MethodPost, "/v1/decisions/" + missing + "/supersede"},
		{http.MethodPost, "/v1/decisions/" + missing + "/links/risk"},
		{http.MethodDelete, "/v1/decisions/" + missing + "/links/risk/" + risk},
	} {
		if code, body := do(w.method, w.path); code != http.StatusForbidden {
			t.Errorf("viewer %s %s = %d %s, want 403", w.method, w.path, code, body)
		}
	}
}
