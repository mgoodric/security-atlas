package authz_test

// #2556: viewers read the risk register and the decision log, and never
// write them (Matt's call, 2026-11-17). Each request runs through authzmw
// with the real OPA bundle and a resolver standing in for user_roles, then
// a handler applying the route's handler gate, if it has one: 204 when both
// layers admit it, else 403. The caller is a signed-in person's JWT session
// whose role claim names the same role as its live user_roles row.
//
// The route lists are the risk module's routes in register_risk.go and
// register_governance.go. A read listed with no gate is decided by OPA
// alone: its handler has no role check. GET /v1/risks/{id}/action-plans is
// the one risk read whose handler gate is not here: it is actionplans'
// hasProgramRead, still on the #2507 allow-list and shared with
// /v1/controls/{id}/action-plans; this file pins its OPA layer only.

import (
	"context"
	"net/http"
	"testing"

	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/authz"
)

const (
	tenant2556 = "00000000-0000-4000-8000-000000002556"
	user2556   = "6f1c2a52-3b0e-4b8e-9a51-1d6a3f0c2556"
)

// noGate2556 stands for a handler with no role check of its own.
var noGate2556 = gate2428{"none", func(credstore.Credential) bool { return true }}

// riskReads2556 is every risk-module read route and its handler gate.
var riskReads2556 = []route2428{
	{http.MethodGet, "/v1/risks", riskReaderGate},
	{http.MethodGet, "/v1/risks/heatmap", noGate2556},
	{http.MethodGet, "/v1/risks/theme-heatmap", riskReaderGate},
	{http.MethodGet, "/v1/risks/categories", noGate2556},
	{http.MethodGet, "/v1/risks/review-cadence", noGate2556},
	{http.MethodGet, "/v1/risks/export", riskReaderGate},
	{http.MethodGet, "/v1/risks/" + id2428, noGate2556},
	{http.MethodGet, "/v1/risks/" + id2428 + "/aggregation", noGate2556},
	{http.MethodGet, "/v1/risks/" + id2428 + "/children", noGate2556},
	{http.MethodGet, "/v1/risks/" + id2428 + "/parents", noGate2556},
	{http.MethodGet, "/v1/risks/" + id2428 + "/audit-log", noGate2556},
	{http.MethodGet, "/v1/risks/" + id2428 + "/action-plans", noGate2556},
	{http.MethodGet, "/v1/themes", noGate2556},
	{http.MethodGet, "/v1/org_units", riskReaderGate},
	{http.MethodGet, "/v1/org_units/" + id2428, noGate2556},
	{http.MethodGet, "/v1/decisions", riskReaderGate},
	{http.MethodGet, "/v1/decisions/overdue", noGate2556},
	{http.MethodGet, "/v1/decisions/" + id2428, noGate2556},
	{http.MethodGet, "/v1/decisions/" + id2428 + "/audit-log", noGate2556},
}

// riskWrites2556 is every risk-module write route. Its handler gate does
// not matter here: OPA must refuse a viewer before the handler runs.
var riskWrites2556 = []route2428{
	{http.MethodPost, "/v1/risks", noGate2556},
	{http.MethodPost, "/v1/risks/categories", tenantApproverGate},
	{http.MethodPatch, "/v1/risks/categories/c1", tenantApproverGate},
	{http.MethodDelete, "/v1/risks/categories/c1", tenantApproverGate},
	{http.MethodPost, "/v1/risks/categories/c1/archive", tenantApproverGate},
	{http.MethodPost, "/v1/risks/categories/c1/unarchive", tenantApproverGate},
	{http.MethodPost, "/v1/risks/categories/c1/set-default", tenantApproverGate},
	{http.MethodPost, "/v1/risks/aggregate", noGate2556},
	{http.MethodPost, "/v1/risks/" + id2428 + "/controls", noGate2556},
	{http.MethodDelete, "/v1/risks/" + id2428 + "/controls/" + id2428, noGate2556},
	{http.MethodPost, "/v1/risks/" + id2428 + "/children", noGate2556},
	{http.MethodDelete, "/v1/risks/" + id2428 + "/children/" + id2428, noGate2556},
	{http.MethodPatch, "/v1/risks/" + id2428 + "/treatment", noGate2556},
	{http.MethodPatch, "/v1/risks/" + id2428 + "/acceptance/propose", noGate2556},
	{http.MethodPatch, "/v1/risks/" + id2428 + "/acceptance/approve", tenantApproverGate},
	{http.MethodPatch, "/v1/risks/" + id2428 + "/acceptance/reject", tenantApproverGate},
	{http.MethodPatch, "/v1/risks/" + id2428 + "/acceptance/revoke", tenantApproverGate},
	{http.MethodPatch, "/v1/risks/" + id2428 + "/close", noGate2556},
	{http.MethodPatch, "/v1/risks/" + id2428 + "/reopen", noGate2556},
	{http.MethodPatch, "/v1/risks/" + id2428, noGate2556},
	{http.MethodDelete, "/v1/risks/" + id2428, tenantAdminGate},
	{http.MethodPost, "/v1/themes", tenantApproverGate},
	{http.MethodPatch, "/v1/themes/t1", tenantApproverGate},
	{http.MethodDelete, "/v1/themes/t1", tenantApproverGate},
	{http.MethodPost, "/v1/risks/" + id2428 + "/themes", noGate2556},
	{http.MethodDelete, "/v1/risks/" + id2428 + "/themes/t1", noGate2556},
	{http.MethodPost, "/v1/org_units", noGate2556},
	{http.MethodPatch, "/v1/org_units/" + id2428, noGate2556},
	{http.MethodDelete, "/v1/org_units/" + id2428, noGate2556},
	{http.MethodPost, "/v1/decisions", noGate2556},
	{http.MethodPatch, "/v1/decisions/" + id2428, noGate2556},
	{http.MethodPost, "/v1/decisions/" + id2428 + "/supersede", noGate2556},
	{http.MethodPost, "/v1/decisions/" + id2428 + "/links/risk", noGate2556},
	{http.MethodDelete, "/v1/decisions/" + id2428 + "/links/risk/" + id2428, noGate2556},
}

// session2556 is a signed-in person holding role, live and in the claim.
func session2556(t *testing.T, role authz.Role) (*authz.Engine, credstore.Credential) {
	t.Helper()
	e, err := authz.NewEngine(context.Background(), tenantResolver{
		{tenant2556, user2556}: {role},
	})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	return e, credstore.Credential{
		ID: "jwt:2556", TenantID: tenant2556, UserID: user2556, OwnerRoles: []string{string(role)},
	}
}

func TestRiskReads2556_ViewerReadsEveryRiskRoute(t *testing.T) {
	t.Parallel()
	e, viewer := session2556(t, authz.RoleViewer)
	for _, rt := range riskReads2556 {
		if got := gateStatus2447(t, e, viewer, rt); got != http.StatusNoContent {
			t.Errorf("viewer: %s %s = %d, want 204", rt.method, rt.path, got)
		}
	}
}

func TestRiskReads2556_ViewerWritesNothing(t *testing.T) {
	t.Parallel()
	e, viewer := session2556(t, authz.RoleViewer)
	for _, rt := range riskWrites2556 {
		// Every handler admits: the refusal must come from OPA.
		open := route2428{rt.method, rt.path, noGate2556}
		if got := gateStatus2447(t, e, viewer, open); got != http.StatusForbidden {
			t.Errorf("viewer: %s %s = %d past OPA, want 403", rt.method, rt.path, got)
		}
		// Where the handler has a role gate of its own, it refuses too.
		if rt.gate.name != noGate2556.name &&
			rt.gate.fn(credstore.Credential{ID: "jwt:2556", TenantRoles: []string{string(authz.RoleViewer)}}) {
			t.Errorf("viewer: %s %s handler gate %s admits a viewer", rt.method, rt.path, rt.gate.name)
		}
	}
}

// TestRiskReads2556_OtherRolesUnchanged pins the rest of the matrix. admin
// and grc_engineer read and write as before. control_owner and auditor are
// refused every risk-module read and write, by OPA, as they were before
// #2556; the handler gates moving off the role claim does not change that.
// The exception is GET /v1/themes, a catalog read (defaults.rego
// catalog_resources) every signed-in role gets.
func TestRiskReads2556_OtherRolesUnchanged(t *testing.T) {
	t.Parallel()
	for _, role := range []authz.Role{authz.RoleAdmin, authz.RoleGRCEngineer, authz.RoleControlOwner, authz.RoleAuditor} {
		e, cred := session2556(t, role)
		reads := role == authz.RoleAdmin || role == authz.RoleGRCEngineer
		for _, rt := range riskReads2556 {
			want := http.StatusForbidden
			if reads || rt.path == "/v1/themes" {
				want = http.StatusNoContent
			}
			if got := gateStatus2447(t, e, cred, rt); got != want {
				t.Errorf("%s: %s %s = %d, want %d", role, rt.method, rt.path, got, want)
			}
		}
		if reads {
			continue
		}
		for _, rt := range riskWrites2556 {
			open := route2428{rt.method, rt.path, noGate2556}
			if got := gateStatus2447(t, e, cred, open); got != http.StatusForbidden {
				t.Errorf("%s: %s %s = %d past OPA, want 403", role, rt.method, rt.path, got)
			}
		}
	}
}

func TestRiskReads2556_IsTenantRiskReader(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name string
		cred credstore.Credential
		want bool
	}{
		{"admin flag", credstore.Credential{ID: "key_a", IsAdmin: true}, true},
		{"approver flag", credstore.Credential{ID: "key_p", IsApprover: true}, true},
		{"live admin", credstore.Credential{ID: "jwt:1", UserID: "u1", TenantRoles: []string{"admin"}}, true},
		{"live grc_engineer", credstore.Credential{ID: "jwt:2", UserID: "u2", TenantRoles: []string{"grc_engineer"}}, true},
		{"live viewer", credstore.Credential{ID: "jwt:3", UserID: "u3", TenantRoles: []string{"viewer"}}, true},
		{"live control_owner", credstore.Credential{ID: "jwt:4", UserID: "u4", TenantRoles: []string{"control_owner"}}, false},
		{"live auditor", credstore.Credential{ID: "jwt:5", UserID: "u5", TenantRoles: []string{"auditor"}}, false},
		// A session's role claim outlives a revoke (#2381); only live roles count.
		{"viewer claim, no live role", credstore.Credential{ID: "jwt:6", UserID: "u6", OwnerRoles: []string{"viewer"}}, false},
		{"control_owner claim, no live role", credstore.Credential{ID: "jwt:7", UserID: "u7", OwnerRoles: []string{"control_owner"}}, false},
		// An unbound key's label is its grant; a bound key's is a copy.
		{"unbound key, viewer label", credstore.Credential{ID: "key_8", UserID: "key_8", OwnerRoles: []string{"viewer"}}, true},
		{"bound key, viewer label", credstore.Credential{ID: "key_9", UserID: "u9", OwnerRoles: []string{"viewer"}}, false},
		{"unbound key, control_owner label", credstore.Credential{ID: "key_10", UserID: "key_10", OwnerRoles: []string{"control_owner"}}, false},
		{"bare credential", credstore.Credential{ID: "key_0", UserID: "key_0"}, false},
	} {
		if got := authz.IsTenantRiskReader(c.cred); got != c.want {
			t.Errorf("%s: IsTenantRiskReader = %v, want %v", c.name, got, c.want)
		}
	}
}
