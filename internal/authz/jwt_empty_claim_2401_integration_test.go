//go:build integration

// #2401: a JWT whose role claim is empty for the current tenant derived
// grc_engineer from the credential default. Every login path mints that
// claim for a user with no user_roles row in the tenant, so revoking a
// user's only role lasted until their next login. Such a session now
// derives nothing; its roles are the live user_roles rows alone. Only a
// client_credentials machine token keeps the default.
//
// The matrix decides every principal kind against the real RLS pool, once
// with the pre-#2401 derivation (grc_engineer for an empty claim) and once
// with the shipped one. The only change it allows is narrowing, and a
// narrowed principal decides exactly as its live user_roles rows do.
//
// Run with:  go test -tags=integration -race -run 2401 ./internal/authz/...

package authz_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/authz"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

var routes2401 = append(append([]route2239{}, routes2381...),
	route2239{http.MethodGet, "/v1/me"},
	route2239{http.MethodGet, "/v1/dashboard/drift"},
	route2239{http.MethodGet, "/v1/dashboard/risks"},
)

// legacyDerived2401 is the pre-#2401 derivation for the empty-claim
// principals this matrix decides: grc_engineer for any tenant credential.
func legacyDerived2401(c credstore.Credential) []authz.Role {
	if len(c.OwnerRoles) == 0 && !c.IsAdmin && !c.IsApprover && c.TenantID != "" {
		return []authz.Role{authz.RoleGRCEngineer}
	}
	return nil
}

func TestDecide2401_EmptyClaimRoleMatrix(t *testing.T) {
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
	// expect decides on a fixed role set with no DB hop.
	expect, err := authz.NewEngine(context.Background(), authz.NoopRolesResolver{})
	if err != nil {
		t.Fatalf("NewEngine(expect): %v", err)
	}

	seedUser := func(role authz.Role) uuid.UUID {
		id := uuid.New()
		if _, err := adminPool.Exec(context.Background(),
			`INSERT INTO users (id, tenant_id, email, display_name, status) VALUES ($1, $2, $3, 'u2401', 'active')`,
			id, tenant, "u2401-"+id.String()+"@example.invalid"); err != nil {
			t.Fatalf("seed user: %v", err)
		}
		if role != "" {
			if _, err := adminPool.Exec(context.Background(),
				`INSERT INTO user_roles (tenant_id, user_id, role, granted_by) VALUES ($1, $2, $3, 'test-2401')`,
				tenant, id.String(), string(role)); err != nil {
				t.Fatalf("seed user_roles: %v", err)
			}
		}
		return id
	}

	type principal struct {
		name    string
		cred    credstore.Credential
		grant   []authz.Role // the live user_roles rows
		narrows bool
	}
	roleless := seedUser("")
	principals := []principal{
		{"JWT user, no user_roles row", credstore.Credential{ID: "jwt:n", TenantID: tenant, UserID: "user:" + roleless.String()}, nil, true},
		{"JWT bare uuid, no user_roles row", credstore.Credential{ID: "jwt:b", TenantID: tenant, UserID: roleless.String()}, nil, true},
		{"JWT test-harness subject", credstore.Credential{ID: "jwt:h", TenantID: tenant, UserID: "test-viewer:" + tenant}, nil, true},
		{"JWT unknown subject", credstore.Credential{ID: "jwt:x", TenantID: tenant, UserID: "someone@example.invalid"}, nil, true},
		{"OAuth client", credstore.Credential{ID: "jwt:o", TenantID: tenant, UserID: "oauth_client:ci2401"}, nil, false},
		{"push key", credstore.Credential{ID: "key_push2401", TenantID: tenant, UserID: "key_push2401"}, nil, false},
	}
	// A role granted after the token was minted: the claim is empty, the
	// live row is not.
	for _, r := range authz.CanonicalRoles() {
		u := seedUser(r)
		principals = append(principals, principal{
			"JWT user, empty claim, live " + string(r),
			credstore.Credential{ID: "jwt:g" + string(r), TenantID: tenant, UserID: "user:" + u.String()},
			[]authz.Role{r}, true,
		})
	}

	for _, p := range principals {
		lost := 0
		for _, rt := range routes2401 {
			before := decide2239(t, ctx, engine, p.cred, rt, legacyDerived2401(p.cred))
			after := decide2239(t, ctx, engine, p.cred, rt, nil)
			if !p.narrows {
				if before != after {
					t.Errorf("%s: %s %s changed %v -> %v; #2401 must not touch this principal", p.name, rt.method, rt.path, before, after)
				}
				continue
			}
			if after && !before {
				t.Errorf("%s: %s %s gained access; #2401 only narrows", p.name, rt.method, rt.path)
			}
			// Exactly the live grant, nothing derived from the credential.
			want := false
			if len(p.grant) > 0 {
				want = decide2239(t, ctx, expect, p.cred, rt, p.grant)
			}
			if after != want {
				t.Errorf("%s: %s %s allow=%v, want %v (live grant %v only)", p.name, rt.method, rt.path, after, want, p.grant)
			}
			if before && !after {
				lost++
			}
		}
		if p.narrows && len(p.grant) == 0 && lost == 0 {
			t.Errorf("%s: lost nothing; the empty claim still derives grc_engineer", p.name)
		}
	}
}
