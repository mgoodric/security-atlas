package authz_test

// #2507: the `IsAdmin || IsApprover || len(OwnerRoles) > 0` read gates moved
// onto the live-role helpers. Their routes are rows in routes2428, where
// TestTenantGates2428_HandlerAgreesWithOPA checks each gate against OPA. This
// file adds what that table cannot hold:
//
//   - controldetail's evidence routes, whose gate is the any-role check it
//     shares with the control-detail routes while OPA makes a narrower cut;
//     the matrix pins the result of both layers;
//   - that no routes2428 gate admits a session holding a role in its claim
//     alone (a revoked user's token, #2381);
//   - the HasAnyTenantRole helper itself, and the recorded split where OPA
//     admits a free-text label the handler refuses.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
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

// TestReadGates2507_FreeTextLabelOPAAdmitsHandlerRefuses pins a recorded
// split between the layers (Matt, 2026-11-17: keep the stricter handler).
// derivedRolesFor turns a free-text owner_roles label into control_owner,
// so OPA admits these credentials to the control, evidence and activity
// reads: an unbound API key, a client_credentials machine token and a
// legacy credential, whose label is their grant, and a key bound to a user,
// whose label is not. HasAnyTenantRole counts canonical roles only, and a
// bound key's owner's live roles alone, so the handler refuses all four: a
// free-text label grants nothing at the handler. The policy layer catching
// up is #2596; when it does, this test flips to agreement.
func TestReadGates2507_FreeTextLabelOPAAdmitsHandlerRefuses(t *testing.T) {
	t.Parallel()
	e := engine(t)
	shapes := []struct {
		name string
		cred credstore.Credential
	}{
		{"unbound key", credstore.Credential{ID: "key_2507", UserID: "key_2507", TenantID: tenant2507, OwnerRoles: []string{"employee"}}},
		{"machine token", credstore.Credential{ID: "jwt:2507", UserID: "oauth_client:c2507", TenantID: tenant2507, OwnerRoles: []string{"employee"}}},
		{"legacy credential", credstore.Credential{ID: "cred_2507", TenantID: tenant2507, OwnerRoles: []string{"employee"}}},
		{"bound key", credstore.Credential{ID: "key_2507b", UserID: user2507, TenantID: tenant2507, OwnerRoles: []string{"employee"}}},
	}
	routes := []string{
		"/v1/controls/" + id2428 + "/policies",
		"/v1/controls/export",
		"/v1/evidence",
		"/v1/activity",
	}
	for _, sh := range shapes {
		if authz.HasAnyTenantRole(sh.cred) {
			t.Errorf("%s with a free-text label: HasAnyTenantRole admits it", sh.name)
		}
		for _, path := range routes {
			r := httptest.NewRequest(http.MethodGet, path, nil)
			r = r.WithContext(authctx.WithCredential(r.Context(), sh.cred))
			d, err := e.Decide(context.Background(), authz.BuildInput(r, nil))
			if err != nil {
				t.Fatalf("%s GET %s: Decide: %v", sh.name, path, err)
			}
			if !d.Allow {
				t.Errorf("%s GET %s: OPA refuses; the recorded split is gone, update this test and D2", sh.name, path)
			}
		}
	}
}
