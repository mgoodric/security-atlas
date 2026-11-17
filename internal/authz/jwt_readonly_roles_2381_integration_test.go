//go:build integration

// #2381: jwtmw copies a JWT's role claim into OwnerRoles, and BuildInput
// derived control_owner from any non-empty OwnerRoles. So a viewer or
// auditor JWT session was evaluated as control_owner and could write
// evidence, artifacts and walkthroughs. A JWT whose claim names only viewer
// and auditor now derives no credential role; its roles come from the live
// user_roles rows alone.
//
// The matrix runs every principal kind x role against the real RLS pool,
// once with the pre-#2381 derivation (control_owner for any non-empty
// OwnerRoles) and once with the shipped one. The only change it allows is a
// user JWT losing a decision that control_owner alone granted. #2496 widened
// that from viewer and auditor claims to every claim: a user session derives
// nothing from its claim, so a stale admin or grc_engineer claim loses its
// control_owner too.
//
// Run with:  go test -tags=integration -race -run 2381 ./internal/authz/...

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

// writes2381 are the control_owner write surfaces the bug opened.
var writes2381 = []route2239{
	{http.MethodPost, "/v1/evidence:push"},
	{http.MethodPost, "/v1/artifacts:upload"},
	{http.MethodPost, "/v1/walkthroughs"},
}

var routes2381 = append(append([]route2239{}, routes2239...), append(writes2381,
	route2239{http.MethodGet, "/v1/evidence"},
	route2239{http.MethodGet, "/v1/evidence/6f1c2a9e-1d2b-4c3d-8e4f-5a6b7c8d9e0f"},
	route2239{http.MethodGet, "/v1/evidence/freshness"},
	route2239{http.MethodGet, "/v1/artifacts/a1"},
	route2239{http.MethodGet, "/v1/walkthroughs"},
	route2239{http.MethodGet, "/v1/calendar"},
	route2239{http.MethodGet, "/v1/me/audit-periods"},
	route2239{http.MethodPost, "/v1/audit-notes"},
	route2239{http.MethodGet, "/v1/risks"},
)...)

// legacyDerived is the pre-#2381 credential derivation.
func legacyDerived(c credstore.Credential) []authz.Role {
	switch {
	case c.IsAdmin:
		return []authz.Role{authz.RoleAdmin}
	case c.IsApprover:
		return []authz.Role{authz.RoleGRCEngineer}
	case len(c.OwnerRoles) > 0:
		return []authz.Role{authz.RoleControlOwner}
	default:
		return []authz.Role{authz.RoleGRCEngineer}
	}
}

func TestDecide2381_RoleMatrixPerPrincipalKind(t *testing.T) {
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
	// coOnly decides with control_owner and nothing else, no DB hop.
	coOnly, err := authz.NewEngine(context.Background(), authz.NoopRolesResolver{})
	if err != nil {
		t.Fatalf("NewEngine(coOnly): %v", err)
	}

	seedUser := func(status string, role authz.Role) uuid.UUID {
		id := uuid.New()
		if _, err := adminPool.Exec(context.Background(),
			`INSERT INTO users (id, tenant_id, email, display_name, status) VALUES ($1, $2, $3, 'u2381', $4)`,
			id, tenant, "u2381-"+id.String()+"@example.invalid", status); err != nil {
			t.Fatalf("seed user: %v", err)
		}
		if role != "" {
			if _, err := adminPool.Exec(context.Background(),
				`INSERT INTO user_roles (tenant_id, user_id, role, granted_by) VALUES ($1, $2, $3, 'test-2381')`,
				tenant, id.String(), string(role)); err != nil {
				t.Fatalf("seed user_roles: %v", err)
			}
		}
		return id
	}

	type principal struct {
		name     string
		cred     credstore.Credential
		narrows  bool // a user JWT: may lose control_owner-only grants
		mustLose bool // its live roles do not cover control_owner
	}
	var principals []principal
	for _, r := range authz.CanonicalRoles() {
		// Live viewer or auditor does not cover control_owner.
		ro := r == authz.RoleViewer || r == authz.RoleAuditor
		granted := seedUser("active", r)
		revoked := seedUser("active", "")
		jti := uuid.NewString()
		// API keys moved to their own matrix when #2402 narrowed them:
		// apikey_readonly_roles_2402_integration_test.go.
		principals = append(principals,
			principal{"JWT " + string(r) + ", granted", credstore.Credential{ID: "jwt:" + jti, TenantID: tenant, UserID: "user:" + granted.String(), OwnerRoles: []string{string(r)}}, true, ro},
			principal{"JWT " + string(r) + ", grant revoked", credstore.Credential{ID: "jwt:r" + jti, TenantID: tenant, UserID: "user:" + revoked.String(), OwnerRoles: []string{string(r)}}, true, true},
		)
	}
	both := seedUser("active", authz.RoleAuditor)
	principals = append(principals,
		principal{"JWT viewer+auditor", credstore.Credential{ID: "jwt:va", TenantID: tenant, UserID: "user:" + both.String(), OwnerRoles: []string{"viewer", "auditor"}}, true, true},
		principal{"JWT auditor+control_owner", credstore.Credential{ID: "jwt:ac", TenantID: tenant, UserID: "user:" + both.String(), OwnerRoles: []string{"auditor", "control_owner"}}, true, true},
		principal{"JWT viewer+owner label", credstore.Credential{ID: "jwt:vl", TenantID: tenant, UserID: "user:" + both.String(), OwnerRoles: []string{"viewer", "platform-eng"}}, true, true},
		// The empty-claim JWT moved to its own matrix when #2401 narrowed it:
		// jwt_empty_claim_2401_integration_test.go.
		principal{"JWT super_admin viewer", credstore.Credential{ID: "jwt:s", TenantID: tenant, UserID: "user:" + uuid.NewString(), IsAdmin: true, IsApprover: true, OwnerRoles: []string{"viewer"}}, false, false},
		principal{"OAuth client", credstore.Credential{ID: "jwt:o", TenantID: tenant, UserID: "oauth_client:ci2381"}, false, false},
	)

	for _, p := range principals {
		lost := 0
		for _, rt := range routes2381 {
			before := decide2239(t, ctx, engine, p.cred, rt, legacyDerived(p.cred))
			after := decide2239(t, ctx, engine, p.cred, rt, nil)
			if before == after {
				continue
			}
			co := decide2239(t, ctx, coOnly, p.cred, rt, []authz.Role{authz.RoleControlOwner})
			switch {
			case !p.narrows:
				t.Errorf("%s: %s %s changed %v -> %v; #2381 must not touch this principal", p.name, rt.method, rt.path, before, after)
			case after:
				t.Errorf("%s: %s %s gained access; #2381 only narrows", p.name, rt.method, rt.path)
			case !co:
				t.Errorf("%s: %s %s lost a grant control_owner does not give", p.name, rt.method, rt.path)
			default:
				lost++
				t.Logf("%s: %s %s before=true after=false", p.name, rt.method, rt.path)
			}
		}
		if p.mustLose {
			if lost == 0 {
				t.Errorf("%s: lost nothing; the JWT still derives control_owner", p.name)
			}
			for _, rt := range writes2381 {
				if decide2239(t, ctx, engine, p.cred, rt, nil) {
					t.Errorf("%s: %s %s allowed; want deny", p.name, rt.method, rt.path)
				}
			}
		}
	}
}
