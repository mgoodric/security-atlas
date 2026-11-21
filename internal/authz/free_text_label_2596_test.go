package authz_test

// #2596: a free-text role label on a credential whose label is its grant
// (an unbound API key, a client_credentials machine token, a legacy
// credstore credential) derives nothing at OPA, as it grants nothing at the
// handler (#2595, Matt 2026-11-17). derivedRolesFor used to turn any such
// label that was not viewer/auditor-only into control_owner. Now a label
// derives exactly the canonical roles it names.
//
// This file holds the inventory of credential shapes (old roles in each
// row's comment, new roles asserted; docs/audit-log/2596-free-text-labels-
// derive-nothing-decisions.md D2) and the agreement check: for every #2428
// route, the handler gate admits a non-canonical or bound-key shape exactly
// when OPA does, bar the handlerNarrower2428 pairs.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/authzmw"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/authz"
)

const (
	tenant2596 = "00000000-0000-4000-8000-000000002596"
	user2596   = "6f1c2a52-3b0e-4b8e-9a51-1d6a3f0c2596"
)

func unboundKey2596(labels ...string) credstore.Credential {
	return credstore.Credential{ID: "key_2596", UserID: "key_2596", TenantID: tenant2596, OwnerRoles: labels}
}

func machine2596(labels ...string) credstore.Credential {
	return credstore.Credential{ID: "jwt:2596", UserID: "oauth_client:c2596", TenantID: tenant2596, OwnerRoles: labels}
}

func legacy2596(labels ...string) credstore.Credential {
	return credstore.Credential{ID: "cred_2596", TenantID: tenant2596, OwnerRoles: labels}
}

func boundKey2596(labels ...string) credstore.Credential {
	return credstore.Credential{ID: "key_2596b", UserID: user2596, TenantID: tenant2596, OwnerRoles: labels}
}

// TestDerivedRoles2596_Inventory is the shape inventory: the roles BuildInput
// hands OPA for each credential shape, before the resolver merges live
// user_roles. The comment on each row is what it derived before #2596.
func TestDerivedRoles2596_Inventory(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name string
		cred credstore.Credential
		want []authz.Role
	}{
		// Flags: unchanged.
		{"admin flag", credstore.Credential{ID: "key_f1", UserID: "key_f1", TenantID: tenant2596, IsAdmin: true, OwnerRoles: []string{"employee"}}, []authz.Role{rA}},
		{"approver flag", credstore.Credential{ID: "key_f2", UserID: "key_f2", TenantID: tenant2596, IsApprover: true, OwnerRoles: []string{"employee"}}, []authz.Role{rG}},

		// A user's session: nothing from its claim (#2496), unchanged.
		{"user session, admin claim", credstore.Credential{ID: "jwt:u", UserID: user2596, TenantID: tenant2596, OwnerRoles: []string{"admin"}}, nil},
		{"user session, free-text claim", credstore.Credential{ID: "jwt:u", UserID: user2596, TenantID: tenant2596, OwnerRoles: []string{"employee"}}, nil},

		// Unbound API key.
		{"unbound key, no label", unboundKey2596(), []authz.Role{rG}},                                 // was grc_engineer (kept, D3)
		{"unbound key, viewer", unboundKey2596("viewer"), []authz.Role{rV}},                           // was viewer
		{"unbound key, auditor", unboundKey2596("auditor"), []authz.Role{rAU}},                        // was auditor
		{"unbound key, control_owner", unboundKey2596("control_owner"), []authz.Role{rCO}},            // was control_owner
		{"unbound key, grc_engineer", unboundKey2596("grc_engineer"), []authz.Role{rG}},               // was control_owner
		{"unbound key, admin", unboundKey2596("admin"), []authz.Role{rA}},                             // was control_owner
		{"unbound key, free text", unboundKey2596("employee"), nil},                                   // was control_owner
		{"unbound key, free text + viewer", unboundKey2596("employee", "viewer"), []authz.Role{rV}},   // was control_owner
		{"unbound key, viewer + auditor", unboundKey2596("viewer", "auditor"), []authz.Role{rV, rAU}}, // was viewer, auditor
		{"unbound key, free text + admin", unboundKey2596("platform-eng", "admin"), []authz.Role{rA}}, // was control_owner

		// client_credentials machine token.
		{"machine token, no claim", machine2596(), []authz.Role{rG}},                      // was grc_engineer (kept, D3)
		{"machine token, viewer", machine2596("viewer"), []authz.Role{rV}},                // was nothing (#2381)
		{"machine token, control_owner", machine2596("control_owner"), []authz.Role{rCO}}, // was control_owner
		{"machine token, grc_engineer", machine2596("grc_engineer"), []authz.Role{rG}},    // was control_owner
		{"machine token, free text", machine2596("employee"), nil},                        // was control_owner

		// Legacy credstore credential (neither key_ nor jwt:).
		{"legacy, no label", legacy2596(), []authz.Role{rG}},           // was grc_engineer (kept, D3)
		{"legacy, viewer", legacy2596("viewer"), []authz.Role{rV}},     // was control_owner
		{"legacy, free text", legacy2596("employee"), nil},             // was control_owner
		{"legacy, no tenant", credstore.Credential{ID: "cred_x"}, nil}, // was nothing

		// An API key naming no user at all (empty UserID) is unbound.
		{"key without user, no label", credstore.Credential{ID: "key_e", TenantID: tenant2596}, []authz.Role{rG}},                               // was grc_engineer
		{"key without user, viewer", credstore.Credential{ID: "key_e", TenantID: tenant2596, OwnerRoles: []string{"viewer"}}, []authz.Role{rV}}, // was nothing
		{"key without user, free text", credstore.Credential{ID: "key_e", TenantID: tenant2596, OwnerRoles: []string{"employee"}}, nil},         // was control_owner

		// A key bound to a user derives nothing: its roles are its owner's
		// live roles, capped by the label (#2431). Unchanged.
		{"bound key, no label", boundKey2596(), nil},
		{"bound key, free text", boundKey2596("employee"), nil},
		{"bound key, admin", boundKey2596("admin"), nil},
	} {
		r := httptest.NewRequest(http.MethodGet, "/v1/controls", nil)
		r = r.WithContext(authctx.WithCredential(r.Context(), c.cred))
		if got := authz.BuildInput(r, nil).User.Roles; !slices.Equal(got, c.want) {
			t.Errorf("%s: derived roles = %v, want %v", c.name, got, c.want)
		}
	}
}

// shape2596 is a credential, the live roles its owner holds (bound keys
// only), and the one role the handler should see it hold, or "" for none.
type shape2596 struct {
	name      string
	cred      credstore.Credential
	ownerLive []authz.Role
	effective authz.Role
}

func shapes2596() []shape2596 {
	var out []shape2596
	for _, mk := range []struct {
		kind string
		fn   func(...string) credstore.Credential
	}{
		{"unbound key", unboundKey2596},
		{"machine token", machine2596},
		{"legacy credential", legacy2596},
	} {
		out = append(out,
			shape2596{mk.kind + ", free text", mk.fn("employee"), nil, ""},
			shape2596{mk.kind + ", two free-text labels", mk.fn("employee", "platform-eng"), nil, ""},
		)
		for _, role := range allRoles2507 {
			out = append(out, shape2596{mk.kind + ", free text + " + string(role), mk.fn("employee", string(role)), nil, role})
		}
	}
	// A key bound to a user: its owner's live role, capped by the label.
	for _, live := range append([]authz.Role{""}, allRoles2507...) {
		var owner []authz.Role
		if live != "" {
			owner = []authz.Role{live}
		}
		out = append(out,
			// No label: the owner's live roles, uncapped.
			shape2596{"bound key, no label, owner " + string(live), boundKey2596(), owner, live},
		)
		// A canonical label caps the owner's live role to itself; an owner
		// holding live admin gets the whole ceiling.
		for _, label := range allRoles2507 {
			eff := authz.Role("")
			if live == label || live == rA {
				eff = label
			}
			out = append(out, shape2596{"bound key, label " + string(label) + ", owner " + string(live), boundKey2596(string(label)), owner, eff})
		}
		// A free-text label is a control_owner ceiling (#2431, kept: D4).
		eff := authz.Role("")
		if live == rCO || live == rA {
			eff = rCO
		}
		out = append(out, shape2596{"bound key, free text, owner " + string(live), boundKey2596("employee"), owner, eff})
	}
	return out
}

// roleFree2596 are the routes2428 routes OPA admits whatever the caller's
// roles, so a shape deriving none still passes OPA there and the handler
// gate is the narrower layer: frameworks is a catalog read
// (defaults.rego), and system.rego admits a machine actor (an unbound key,
// a machine token, a legacy credential) to the bundle upload.
var roleFree2596 = map[string]bool{
	"/v1/frameworks/posture":     true,
	"/v1/controls:upload-bundle": true,
}

// TestFreeTextLabels2596_HandlerAgreesWithOPA sends every shape through
// authzmw (BuildInput + Decide, then the live roles stamped on the
// credential) and the route's handler gate, for every routes2428 route.
// The handler admits exactly when OPA does, bar the handlerNarrower2428
// pairs for the shape's effective role; and never admits what OPA refuses.
func TestFreeTextLabels2596_HandlerAgreesWithOPA(t *testing.T) {
	t.Parallel()
	for _, sh := range shapes2596() {
		e, err := authz.NewEngine(context.Background(), tenantResolver{{tenant2596, user2596}: sh.ownerLive})
		if err != nil {
			t.Fatalf("NewEngine: %v", err)
		}
		for _, rt := range routes2428 {
			opa, handler := false, false
			h := authzmw.Middleware(e, nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				opa = true
				c, ok := authctx.CredentialFromContext(r.Context())
				handler = ok && rt.gate.fn(c)
			}))
			r := httptest.NewRequest(rt.method, rt.path, nil)
			r = r.WithContext(authctx.WithCredential(r.Context(), sh.cred))
			h.ServeHTTP(httptest.NewRecorder(), r)

			narrower := roleFree2596[rt.path] ||
				sh.effective != "" && handlerNarrower2428[rt.path+" "+string(sh.effective)]
			switch {
			case handler && !opa:
				t.Errorf("%s: %s %s: handler %s admits what OPA refuses", sh.name, rt.method, rt.path, rt.gate.name)
			case !narrower && handler != opa:
				t.Errorf("%s: %s %s: handler %s=%v, OPA allow=%v", sh.name, rt.method, rt.path, rt.gate.name, handler, opa)
			}
			// The shape acts as its effective role, or as nothing.
			want := sh.effective != "" && wantAdmit2428(rt.gate, sh.effective)
			if handler != want {
				t.Errorf("%s: %s %s: handler %s=%v, want %v (effective role %q)", sh.name, rt.method, rt.path, rt.gate.name, handler, want, sh.effective)
			}
		}
	}
}
