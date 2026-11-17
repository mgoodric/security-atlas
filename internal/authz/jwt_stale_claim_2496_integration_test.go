//go:build integration

// #2496: a user's session whose role claim still says admin or grc_engineer
// derived control_owner from the claim after every live user_roles row was
// revoked, until the token expired. A user's session now derives nothing
// from its claim; its roles are the live user_roles rows alone.
//
// The matrix holds one credential (one token, no re-login) per claim while
// the user's live roles change underneath it: granted, revoked, granted a
// narrower role.
//
// Run with:  go test -tags=integration -race -run 2496 ./internal/authz/...

package authz_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/authz"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

func TestDecide2496_StaleClaimRoleMatrix(t *testing.T) {
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

	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := adminPool.Exec(context.Background(), sql, args...); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	grant := func(user uuid.UUID, role authz.Role) {
		exec(`INSERT INTO user_roles (tenant_id, user_id, role, granted_by) VALUES ($1, $2, $3, 'test-2496')`,
			tenant, user.String(), string(role))
	}
	revokeAll := func(user uuid.UUID) {
		exec(`DELETE FROM user_roles WHERE tenant_id = $1 AND user_id = $2`, tenant, user.String())
	}

	for _, claim := range []authz.Role{authz.RoleAdmin, authz.RoleGRCEngineer} {
		user := uuid.New()
		exec(`INSERT INTO users (id, tenant_id, email, display_name, status) VALUES ($1, $2, $3, 'u2496', 'active')`,
			user, tenant, "u2496-"+user.String()+"@example.invalid")
		cred := credstore.Credential{
			ID: "jwt:2496" + string(claim), TenantID: tenant, UserID: "user:" + user.String(),
			OwnerRoles: []string{string(claim)},
		}

		// Matches decides cred against the live rows and checks it admits
		// exactly what live alone admits.
		matches := func(stage string, live []authz.Role) {
			t.Helper()
			if got := deriveRoles2496(ctx, cred); len(got) != 0 {
				t.Errorf("claim %s, %s: derived %v from the claim; want nothing", claim, stage, got)
			}
			for _, rt := range routes2401 {
				got := decide2239(t, ctx, engine, cred, rt, nil)
				want := rt.method == http.MethodGet && rolelessSelfReadPaths[rt.path]
				if len(live) > 0 {
					want = decide2239(t, ctx, expect, cred, rt, live)
				}
				if got != want {
					t.Errorf("claim %s, %s: %s %s allow=%v, want %v (live %v only)", claim, stage, rt.method, rt.path, got, want, live)
				}
			}
		}

		grant(user, claim)
		matches("live role granted", []authz.Role{claim})

		// Every live role revoked, same token: nothing but the two /me reads.
		revokeAll(user)
		matches("every live role revoked", nil)
		for _, rt := range append(append([]route2239{}, routes2401...), route2239{http.MethodGet, "/v1/me/tenants"}) {
			self := rt.method == http.MethodGet && rolelessSelfReadPaths[rt.path]
			if got := decide2239(t, ctx, engine, cred, rt, nil); got != self {
				t.Errorf("claim %s, revoked: %s %s allow=%v, want %v", claim, rt.method, rt.path, got, self)
			}
		}

		// Granted back as viewer, same token: viewer applies at once, and
		// the claim adds nothing on top.
		grant(user, authz.RoleViewer)
		matches("live viewer", []authz.Role{authz.RoleViewer})
	}
}

// deriveRoles2496 is the role set BuildInput hands Decide before the live
// user_roles merge.
func deriveRoles2496(ctx context.Context, cred credstore.Credential) []authz.Role {
	req := httptest.NewRequest(http.MethodGet, "/v1/controls", nil)
	req = req.WithContext(authctx.WithCredential(ctx, cred))
	return authz.BuildInput(req, nil).User.Roles
}
