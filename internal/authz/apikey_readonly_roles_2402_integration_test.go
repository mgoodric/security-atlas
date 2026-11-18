//go:build integration

// #2402: an API key's owner_roles reach BuildInput as OwnerRoles, and any
// non-empty OwnerRoles derived control_owner. So a key minted with
// owner_roles = {viewer} or {auditor} could write evidence, artifacts and
// walkthroughs. #2381 closed the same hole for JWT sessions only (its D3).
// Now an unbound viewer/auditor key derives exactly its label, and a bound
// one derives nothing and holds its owner's live user_roles rows.
//
// The matrix runs every key shape x role against the real RLS pool, once
// with the pre-#2402 derivation (control_owner) and once with the shipped
// one. It allows two changes for a viewer or auditor key: losing a decision
// that control_owner alone granted, and, for an unbound key only, gaining a
// GET its label's own role grants (D1 in the decisions log). Nothing may
// gain a write.
//
// Keys bound to a user are no longer here: #2431 caps them at their label,
// and TestDecide2431_BoundKeyRoleMatrix covers them.
//
// Run with:  go test -tags=integration -race -run 2402 ./internal/authz/...

package authz_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/authz"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

func TestDecide2402_APIKeyRoleMatrix(t *testing.T) {
	t.Parallel()
	appPool := dbtest.NewAppPool(t)
	adminPool := dbtest.NewMigratePool(t)
	tenant := freshTenant(t, adminPool)
	ctx, err := tenancy.WithTenant(context.Background(), tenant)
	if err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
	engine, err := authz.NewEngine(context.Background(), authz.NewDBRolesResolver(appPool))
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	coOnly, err := authz.NewEngine(context.Background(), authz.NoopRolesResolver{})
	if err != nil {
		t.Fatalf("NewEngine(coOnly): %v", err)
	}

	seedUser := func(roles ...authz.Role) uuid.UUID {
		id := uuid.New()
		if _, err := adminPool.Exec(context.Background(),
			`INSERT INTO users (id, tenant_id, email, display_name, status) VALUES ($1, $2, $3, 'u2402', 'active')`,
			id, tenant, "u2402-"+id.String()+"@example.invalid"); err != nil {
			t.Fatalf("seed user: %v", err)
		}
		for _, r := range roles {
			if _, err := adminPool.Exec(context.Background(),
				`INSERT INTO user_roles (tenant_id, user_id, role, granted_by) VALUES ($1, $2, $3, 'test-2402')`,
				tenant, id.String(), string(r)); err != nil {
				t.Fatalf("seed user_roles: %v", err)
			}
		}
		return id
	}
	unbound := func(id string, labels ...string) credstore.Credential {
		return credstore.Credential{ID: id, TenantID: tenant, UserID: id, OwnerRoles: labels}
	}

	type principal struct {
		name    string
		cred    credstore.Credential
		narrows bool // a viewer/auditor-only key: may lose control_owner-only grants
		keepsCO bool // its owner's live roles still write, so writes stay open
	}
	var principals []principal
	for _, r := range authz.CanonicalRoles() {
		ro := r == authz.RoleViewer || r == authz.RoleAuditor
		label := string(r)
		// Keys bound to a user moved to the #2431 matrix
		// (bound_key_ceiling_2431_integration_test.go), which caps them.
		principals = append(principals,
			principal{"unbound key " + label, unbound("key_u2402_"+label, label), ro, false},
		)
	}
	principals = append(principals,
		principal{"unbound key viewer+auditor", unbound("key_va2402", "viewer", "auditor"), true, false},
		principal{"unbound key viewer+owner label", unbound("key_vl2402", "viewer", "platform-eng"), false, false},
		principal{"unbound key auditor+control_owner", unbound("key_ac2402", "auditor", "control_owner"), false, false},
		principal{"unbound key, no label", unbound("key_e2402"), false, false},
		principal{"admin key viewer", credstore.Credential{ID: "key_ad2402", TenantID: tenant, UserID: "key_ad2402", IsAdmin: true, OwnerRoles: []string{"viewer"}}, false, false},
		principal{"approver key auditor", credstore.Credential{ID: "key_ap2402", TenantID: tenant, UserID: "key_ap2402", IsApprover: true, OwnerRoles: []string{"auditor"}}, false, false},
		principal{"JWT viewer (#2381, unchanged here)", credstore.Credential{ID: "jwt:v2402", TenantID: tenant, UserID: "user:" + seedUser(authz.RoleViewer).String(), OwnerRoles: []string{"viewer"}}, false, false},
	)

	narrowed, gained := 0, 0
	for _, p := range principals {
		unboundKey := p.cred.UserID == p.cred.ID
		lost := 0
		for _, rt := range routes2381 {
			// before: the #2381 derivation, which gave every key with a
			// non-empty label control_owner. JWTs use the shipped one.
			var legacy []authz.Role
			if strings.HasPrefix(p.cred.ID, "key_") {
				legacy = legacyDerived(p.cred)
			}
			before := decide2239(t, ctx, engine, p.cred, rt, legacy)
			after := decide2239(t, ctx, engine, p.cred, rt, nil)
			if before == after {
				continue
			}
			co := decide2239(t, ctx, coOnly, p.cred, rt, []authz.Role{authz.RoleControlOwner})
			switch {
			case !p.narrows:
				t.Errorf("%s: %s %s changed %v -> %v; #2402 must not touch this principal", p.name, rt.method, rt.path, before, after)
			case after && rt.method == http.MethodGet && unboundKey && decide2239(t, ctx, coOnly, p.cred, rt, labelRoles(p.cred)):
				// D1: an unbound key now holds its label's role, so it
				// reads what that role alone reads. Reads only.
				gained++
				t.Logf("%s: %s %s before=false after=true (its label's role grants it)", p.name, rt.method, rt.path)
			case after:
				t.Errorf("%s: %s %s gained access its label's role does not give; #2402 only narrows", p.name, rt.method, rt.path)
			case !co:
				t.Errorf("%s: %s %s lost a grant control_owner does not give", p.name, rt.method, rt.path)
			default:
				lost++
				t.Logf("%s: %s %s before=true after=false", p.name, rt.method, rt.path)
			}
		}
		if lost > 0 {
			narrowed++
		}
		if p.narrows && !p.keepsCO {
			for _, rt := range writes2381 {
				if decide2239(t, ctx, engine, p.cred, rt, nil) {
					t.Errorf("%s: %s %s allowed; want deny", p.name, rt.method, rt.path)
				}
			}
		}
	}
	if narrowed == 0 {
		t.Error("no key lost anything; viewer/auditor keys still derive control_owner")
	}
	t.Logf("%d principals narrowed; %d label-role reads gained by unbound keys", narrowed, gained)
}

// labelRoles is the role set an unbound key's viewer/auditor label names.
func labelRoles(c credstore.Credential) []authz.Role {
	var out []authz.Role
	for _, l := range c.OwnerRoles {
		out = append(out, authz.Role(l))
	}
	return out
}
