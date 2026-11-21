//go:build integration

// #2431: an API key bound to a user (issued_by set) acted with its owner's
// full live user_roles whatever its owner_roles label said, so a {viewer}
// key minted for an admin acted as admin. Now the label is a ceiling: the
// key holds the owner's live roles that its label allows, and an owner
// holding live admin gets the whole ceiling.
//
// The matrix runs every label shape x owner against the real RLS pool. For
// each route it compares the pre-#2431 decision (the old derivation merged
// with the owner's live roles) with the shipped one, and allows only
// narrowing: nothing may gain. It also checks the shipped decision equals
// the decision for the roles the ceiling rule names, so the narrowing is the
// intended one. An unlabelled key is not capped, and holds exactly its
// owner's live roles: it loses the grc_engineer default it used to derive.
// Unbound keys are not capped; their derivation is #2402's and #2596's.
//
// Run with:  go test -tags=integration -race -run 2431 ./internal/authz/...

package authz_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/authz"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// legacyBoundDerived2431 is what a key bound to a user derived before #2431
// (#2402): nothing for a viewer/auditor-only label, control_owner for any
// other non-empty label, grc_engineer for an empty one.
func legacyBoundDerived2431(labels []string) []authz.Role {
	if len(labels) == 0 {
		return []authz.Role{authz.RoleGRCEngineer}
	}
	for _, l := range labels {
		if l != string(authz.RoleViewer) && l != string(authz.RoleAuditor) {
			return []authz.Role{authz.RoleControlOwner}
		}
	}
	return nil
}

// ceiling2431 is the ceiling rule written out independently of the code
// under test: canonical label entries give themselves, free text gives
// control_owner; a live admin owner gets the whole ceiling.
func ceiling2431(labels []string, live []authz.Role) []authz.Role {
	isAdmin := false
	for _, r := range live {
		if r == authz.RoleAdmin {
			isAdmin = true
		}
	}
	var out []authz.Role
	for _, l := range labels {
		r := authz.Role(l)
		if !authz.IsCanonical(r) {
			r = authz.RoleControlOwner
		}
		held := isAdmin
		for _, x := range live {
			held = held || x == r
		}
		if held {
			out = append(out, r)
		}
	}
	return out
}

func union2431(a, b []authz.Role) []authz.Role {
	out := append([]authz.Role{}, a...)
	for _, r := range b {
		dup := false
		for _, x := range out {
			dup = dup || x == r
		}
		if !dup {
			out = append(out, r)
		}
	}
	return out
}

func TestDecide2431_BoundKeyRoleMatrix(t *testing.T) {
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
	// fixed decides on exactly the roles it is handed: no user_roles hop.
	fixed, err := authz.NewEngine(context.Background(), authz.NoopRolesResolver{})
	if err != nil {
		t.Fatalf("NewEngine(fixed): %v", err)
	}

	seedUser := func(status string, roles ...authz.Role) uuid.UUID {
		id := uuid.New()
		if _, err := adminPool.Exec(context.Background(),
			`INSERT INTO users (id, tenant_id, email, display_name, status) VALUES ($1, $2, $3, 'u2431', $4)`,
			id, tenant, "u2431-"+id.String()+"@example.invalid", status); err != nil {
			t.Fatalf("seed user: %v", err)
		}
		for _, r := range roles {
			if _, err := adminPool.Exec(context.Background(),
				`INSERT INTO user_roles (tenant_id, user_id, role, granted_by) VALUES ($1, $2, $3, 'test-2431')`,
				tenant, id.String(), string(r)); err != nil {
				t.Fatalf("seed user_roles: %v", err)
			}
		}
		return id
	}

	labels := [][]string{{}, {"platform-eng"}, {"viewer", "control_owner"}, {"viewer", "auditor"}}
	for _, r := range authz.CanonicalRoles() {
		labels = append(labels, []string{string(r)})
	}
	type owner struct {
		name   string
		status string
		live   []authz.Role // what the resolver returns for this owner
	}
	owners := []owner{{"no roles", "active", nil}, {"admin+viewer", "active", []authz.Role{authz.RoleAdmin, authz.RoleViewer}}}
	for _, r := range authz.CanonicalRoles() {
		owners = append(owners, owner{string(r), "active", []authz.Role{r}})
	}
	// A disabled owner keeps its user_roles rows, but the resolver returns
	// none; apikeystore also refuses the key at lookup (#1915).
	disabled := owner{"disabled admin", "disabled", nil}
	owners = append(owners, disabled)

	narrowed, unchanged := 0, 0
	for _, o := range owners {
		var seeded []authz.Role
		if o.status == "disabled" {
			seeded = []authz.Role{authz.RoleAdmin}
		} else {
			seeded = o.live
		}
		for _, l := range labels {
			id := seedUser(o.status, seeded...)
			cred := credstore.Credential{
				ID: "key_2431_" + id.String(), TenantID: tenant, UserID: id.String(), OwnerRoles: l,
			}
			name := "label {" + strings.Join(l, ",") + "}, owner " + o.name
			before := union2431(legacyBoundDerived2431(l), o.live)
			want := ceiling2431(l, o.live)
			if len(l) == 0 {
				// No cap, and no grc_engineer default either (Matt,
				// 2026-11-18): exactly the owner's live roles.
				want = o.live
			}
			lost := 0
			for _, rt := range routes2381 {
				was := decide2239(t, ctx, fixed, cred, rt, before)
				is := decide2239(t, ctx, engine, cred, rt, nil)
				if exp := decide2239(t, ctx, fixed, cred, rt, want); is != exp {
					t.Errorf("%s: %s %s allow=%v, want %v (roles %v)", name, rt.method, rt.path, is, exp, want)
				}
				switch {
				case is && !was:
					t.Errorf("%s: %s %s gained access; #2431 only narrows", name, rt.method, rt.path)
				case was && !is:
					lost++
				}
			}
			if lost > 0 {
				narrowed++
				t.Logf("%s: narrowed on %d routes", name, lost)
			} else {
				unchanged++
			}
		}
	}
	// Unbound keys are not capped: an unlabelled one keeps grc_engineer, a
	// labelled one derives the canonical roles its label names, and a
	// free-text label derives nothing (#2402, #2596).
	for _, u := range []struct {
		cred credstore.Credential
		want []authz.Role
	}{
		{credstore.Credential{ID: "key_2431_ue", TenantID: tenant, UserID: "key_2431_ue"}, []authz.Role{authz.RoleGRCEngineer}},
		{credstore.Credential{ID: "key_2431_uv", TenantID: tenant, UserID: "key_2431_uv", OwnerRoles: []string{"viewer"}}, []authz.Role{authz.RoleViewer}},
		{credstore.Credential{ID: "key_2431_uf", TenantID: tenant, UserID: "key_2431_uf", OwnerRoles: []string{"platform-eng"}}, []authz.Role{}},
	} {
		for _, rt := range routes2381 {
			if is, exp := decide2239(t, ctx, engine, u.cred, rt, nil), decide2239(t, ctx, fixed, u.cred, rt, u.want); is != exp {
				t.Errorf("unbound key %v: %s %s allow=%v, want %v (roles %v)", u.cred.OwnerRoles, rt.method, rt.path, is, exp, u.want)
			}
		}
	}

	if narrowed == 0 {
		t.Error("no bound key narrowed; the label is not capping the owner's roles")
	}
	t.Logf("%d bound keys narrowed; %d unchanged", narrowed, unchanged)
}
