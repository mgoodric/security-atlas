package authz_test

// #2507: the `IsAdmin || IsApprover || len(OwnerRoles) > 0` read gates moved
// onto the live-role helpers. Each route below runs through authzmw with the
// real OPA bundle and a resolver standing in for user_roles, then a handler
// applying the route's gate. The caller is a signed-in person's JWT session
// whose role claim names the same role as its live user_roles row, and the
// test pins which of the five roles both layers admit. A session holding the
// role in its claim alone is refused everywhere.
//
// Where a gate is shared by routes OPA reads differently (controldetail's
// control and evidence routes), the gate is the wider any-role check and OPA
// makes the narrower cut; the matrix records the result of both.

import (
	"context"
	"net/http"
	"testing"

	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/authz"
)

const (
	tenant2507 = "00000000-0000-4000-8000-000000002507"
	user2507   = "6f1c2a52-3b0e-4b8e-9a51-1d6a3f0c2507"
)

const (
	rA  = authz.RoleAdmin
	rG  = authz.RoleGRCEngineer
	rCO = authz.RoleControlOwner
	rAU = authz.RoleAuditor
	rV  = authz.RoleViewer
)

var allRoles2507 = []authz.Role{rA, rG, rCO, rAU, rV}

// evidenceReads2507 are controldetail's evidence routes. Their gate is the
// any-role check it shares with the control-detail routes in routes2428;
// OPA refuses viewers here, and admits auditors only to their own audit
// period's records (resource attrs this harness does not supply).
var evidenceReads2507 = []struct {
	route2428
	admits []authz.Role
}{
	{route2428{http.MethodGet, "/v1/evidence", anyRoleGate}, []authz.Role{rA, rG, rCO}},
	{route2428{http.MethodGet, "/v1/evidence/" + id2428, anyRoleGate}, []authz.Role{rA, rG, rCO}},
}

func session2507(t *testing.T, live []authz.Role, claim authz.Role) (*authz.Engine, credstore.Credential) {
	t.Helper()
	e, err := authz.NewEngine(context.Background(), tenantResolver{{tenant2507, user2507}: live})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	return e, credstore.Credential{
		ID: "jwt:2507", TenantID: tenant2507, UserID: user2507, OwnerRoles: []string{string(claim)},
	}
}

func TestReadGates2507_EvidenceReads(t *testing.T) {
	t.Parallel()
	for _, role := range allRoles2507 {
		e, cred := session2507(t, []authz.Role{role}, role)
		for _, rt := range evidenceReads2507 {
			want := http.StatusForbidden
			for _, a := range rt.admits {
				if a == role {
					want = http.StatusNoContent
				}
			}
			if got := gateStatus2447(t, e, cred, rt.route2428); got != want {
				t.Errorf("%s: %s %s (%s) = %d, want %d", role, rt.method, rt.path, rt.gate.name, got, want)
			}
		}
	}
}

// TestReadGates2507_ClaimAloneRefused: a revoked user's token still names
// the role until it expires (#2381). No handler gate in routes2428 counts
// it: the claim alone passes none of them.
func TestReadGates2507_ClaimAloneRefused(t *testing.T) {
	t.Parallel()
	for _, role := range allRoles2507 {
		_, cred := session2507(t, nil, role)
		for _, rt := range routes2428 {
			if rt.gate.fn(cred) {
				t.Errorf("%s claim, no live role: %s %s gate %s admits", role, rt.method, rt.path, rt.gate.name)
			}
		}
	}
}

func TestReadGates2507_HasAnyTenantRole(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name string
		cred credstore.Credential
		want bool
	}{
		{"admin flag", credstore.Credential{ID: "key_a", IsAdmin: true}, true},
		{"approver flag", credstore.Credential{ID: "key_p", IsApprover: true}, true},
		{"live viewer", credstore.Credential{ID: "jwt:1", UserID: "u1", TenantRoles: []string{"viewer"}}, true},
		{"live auditor", credstore.Credential{ID: "jwt:2", UserID: "u2", TenantRoles: []string{"auditor"}}, true},
		{"live control_owner", credstore.Credential{ID: "jwt:3", UserID: "u3", TenantRoles: []string{"control_owner"}}, true},
		{"viewer claim, no live role", credstore.Credential{ID: "jwt:4", UserID: "u4", OwnerRoles: []string{"viewer"}}, false},
		{"unbound key, auditor label", credstore.Credential{ID: "key_5", UserID: "key_5", OwnerRoles: []string{"auditor"}}, true},
		{"bound key, auditor label", credstore.Credential{ID: "key_6", UserID: "u6", OwnerRoles: []string{"auditor"}}, false},
		{"unbound key, free-text label", credstore.Credential{ID: "key_7", UserID: "key_7", OwnerRoles: []string{"employee"}}, false},
		{"bare credential", credstore.Credential{ID: "key_0", UserID: "key_0"}, false},
	} {
		if got := authz.HasAnyTenantRole(c.cred); got != c.want {
			t.Errorf("%s: HasAnyTenantRole = %v, want %v", c.name, got, c.want)
		}
	}
}
