package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/authz"
	"github.com/mgoodric/security-atlas/internal/lifecycle"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// TestLifecycleGuards_MatchTableRoles (#2291): for every person's move in
// every declared lifecycle, the handler's own role guard admits exactly the
// roles the table names. cmd/atlas-lifecycles' TestRoles_MatchAuthzBundle
// proves the table's roles are the ones the OPA bundle admits, so together
// the two prove the handler check and the OPA decision agree on every
// lifecycle move: one source of truth, with the handler as its
// defense-in-depth twin rather than a looser second opinion.
//
// The tables come from docs/lifecycles/, which TestExport_MatchesCommitted
// keeps byte-identical to the Go tables. Each route's endpoint handler is
// taken from the assembled router and called directly, past the shared
// middleware (OPA among it), with a credential holding one live tenant
// role. The pool never dials, so a caller the guard admits gets some other
// error (400, 404, 500); a caller it refuses gets 403.
//
// knownDivergent is the ratchet: routes whose handler does not yet match
// the table, each with the issue that closes the gap. A listed route that
// starts agreeing fails the test until it is removed from the list.
var knownDivergent = map[string]string{
	// The handler checks the cred.IsAdmin flag; OPA admits a live-role
	// admin too, so the handler is stricter (#2665, decide).
	"POST /v1/admin/policies/import": "#2665",
}

func TestLifecycleGuards_MatchTableRoles(t *testing.T) {
	handlers := map[string]http.Handler{}
	if err := chi.Walk(routeWalkServer(t).buildRouter(), func(method, route string, h http.Handler, _ ...func(http.Handler) http.Handler) error {
		handlers[method+" "+route] = h
		return nil
	}); err != nil {
		t.Fatalf("chi.Walk: %v", err)
	}

	const tenant = "00000000-0000-4000-8000-000000002291"
	const id = "6f1c2a52-3b0e-4b8e-9a51-1d6a3f0c2291"
	param := regexp.MustCompile(`\{([a-zA-Z_]+)\}`)
	checked := 0
	// diverged records, per route, whether any role disagreed.
	diverged := map[string]bool{}
	check := func(lc, what string, routes, roles []string) {
		for _, route := range routes {
			h, ok := handlers[route]
			if !ok {
				t.Errorf("%s %s: route %q is not registered on the router", lc, what, route)
				continue
			}
			method, pattern, _ := strings.Cut(route, " ")
			for _, role := range authz.CanonicalRoles() {
				rctx := chi.NewRouteContext()
				for _, m := range param.FindAllStringSubmatch(pattern, -1) {
					rctx.URLParams.Add(m[1], id)
				}
				r := httptest.NewRequest(method, param.ReplaceAllString(pattern, id), strings.NewReader("{}"))
				ctx, err := tenancy.WithTenant(r.Context(), tenant)
				if err != nil {
					t.Fatalf("WithTenant: %v", err)
				}
				ctx = authctx.WithCredential(ctx, credstore.Credential{
					ID: "jwt:2291", TenantID: tenant, UserID: "0b6f3c1e-2291-4a7d-9c2e-5f1a8d3b2291",
					TenantRoles: []string{string(role)},
				})
				r = r.WithContext(context.WithValue(ctx, chi.RouteCtxKey, rctx))
				code := statusOf(h, r)
				if admitted, want := code != http.StatusForbidden, slices.Contains(roles, string(role)); admitted != want {
					diverged[route] = true
					if _, known := knownDivergent[route]; known {
						continue
					}
					t.Errorf("%s %s: %s as %s: handler admitted=%v (status %d), table roles %v",
						lc, what, route, role, admitted, code, roles)
				}
				checked++
			}
		}
	}

	files, err := filepath.Glob(filepath.Join("..", "..", "docs", "lifecycles", "*.json"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no lifecycle documents found: %v", err)
	}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var tab lifecycle.Table
		if err := json.Unmarshal(raw, &tab); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		for _, en := range tab.Initial {
			check(tab.Name, en.Action, en.Routes, en.Roles)
		}
		for _, tr := range tab.Transitions {
			check(tab.Name, fmt.Sprintf("%s %s->%s", tr.Action, tr.From, tr.To), tr.Routes, tr.Roles)
		}
	}
	if checked == 0 {
		t.Fatal("no lifecycle route was checked")
	}
	for route, issue := range knownDivergent {
		if _, seen := diverged[route]; !seen {
			t.Errorf("%s is in knownDivergent (%s) but its handler now agrees with the table, or no lifecycle names it: remove it from the list", route, issue)
		}
	}
}

// statusOf serves r and returns the status written. A handler that panics
// got past its guard (only a refusal is a 403), so a panic reads as 500.
func statusOf(h http.Handler, r *http.Request) (code int) {
	w := httptest.NewRecorder()
	defer func() {
		if recover() != nil {
			code = http.StatusInternalServerError
		}
	}()
	h.ServeHTTP(w, r)
	return w.Code
}
