//go:build integration

// #2239: Engine.Decide looked user_roles up by the raw credential UserID.
// A local-login / OIDC / PKCE / device-grant JWT carries "user:<uuid>",
// while role assignment stores the bare uuid, so assigned roles never
// reached authorization for JWT users. DBRolesResolver now keys the lookup
// through identity.RolesKey.
//
// The role matrix below runs one engine per principal kind against the real
// RLS-enforced pool, twice: once with the pre-#2239 raw-key lookup (the
// baseline) and once with the shipped resolver. It asserts:
//   - password and OIDC users: after == derived roles UNION their grant,
//     exactly, so nobody gains beyond their user_roles rows;
//   - a JWT user with no user_roles row, a bound key, an unbound key and an
//     OAuth client: after == before on every route.
//
// Run with:  go test -tags=integration -race -run 2239 ./internal/authz/...

package authz_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/authz"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// rawKeyResolver is the pre-#2239 lookup, kept here only as the baseline:
// user_roles keyed by the raw principal string.
type rawKeyResolver struct{ pool *pgxpool.Pool }

func (r rawKeyResolver) RolesFor(ctx context.Context, tenantID, userID string) ([]authz.Role, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT role FROM user_roles WHERE tenant_id = $1::uuid AND user_id = $2`, tenantID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []authz.Role
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, authz.Role(s))
	}
	return out, rows.Err()
}

// route2239 is one request the matrix decides.
type route2239 struct{ method, path string }

var routes2239 = []route2239{
	{http.MethodGet, "/v1/controls"},
	{http.MethodGet, "/v1/policies"},
	{http.MethodPost, "/v1/risks"},
	{http.MethodPost, "/v1/controls"},
	{http.MethodPost, "/v1/evidence"},
	{http.MethodPost, "/v1/policies/p1/approve"},
	{http.MethodPost, "/v1/policies/p1/publish"},
	{http.MethodGet, "/v1/framework-scopes"},
	{http.MethodGet, "/v1/samples"},
	{http.MethodGet, "/v1/dashboard"},
	{http.MethodPost, "/v1/admin/users"},
	{http.MethodGet, "/v1/admin/audit-log/unified"},
}

// decide2239 builds the Input BuildInput would produce for cred on rt and
// returns the engine's allow bit.
func decide2239(t *testing.T, ctx context.Context, e *authz.Engine, cred credstore.Credential, rt route2239, roles []authz.Role) bool {
	t.Helper()
	req := httptest.NewRequest(rt.method, rt.path, nil)
	req = req.WithContext(authctx.WithCredential(ctx, cred))
	in := authz.BuildInput(req, nil)
	if roles != nil {
		in.User.Roles = roles
	}
	d, err := e.Decide(ctx, in)
	if err != nil {
		t.Fatalf("Decide %s %s: %v", rt.method, rt.path, err)
	}
	return d.Allow
}

func TestDecide2239_RoleMatrixPerPrincipalKind(t *testing.T) {
	t.Parallel()
	appPool := dbtest.NewAppPool(t)
	adminPool := dbtest.NewMigratePool(t)
	tenant := freshTenant(t, adminPool)
	ctx, err := tenancy.WithTenant(context.Background(), tenant)
	if err != nil {
		t.Fatalf("WithTenant: %v", err)
	}

	passwordUser := uuid.New()
	oidcUser := uuid.New()
	ungrantedUser := uuid.New()
	boundKeyUser := uuid.New()

	// Grants are stored the way role assignment stores them: the bare
	// users(id) for a user, the raw principal for a machine.
	if _, err := adminPool.Exec(context.Background(), `
		INSERT INTO user_roles (tenant_id, user_id, role, granted_by) VALUES
		  ($1, $2, 'admin',   'test'),
		  ($1, $3, 'auditor', 'test'),
		  ($1, $4, 'viewer',  'test'),
		  ($1, 'key_unbound2239', 'viewer', 'test'),
		  ($1, 'oauth_client:ci2239', 'viewer', 'test')
	`, tenant, passwordUser.String(), oidcUser.String(), boundKeyUser.String()); err != nil {
		t.Fatalf("seed user_roles: %v", err)
	}

	type principal struct {
		name    string
		cred    credstore.Credential
		grant   []authz.Role // user_roles rows that now apply
		changes bool         // whether #2239 changes this principal at all
	}
	// JWT users: jwtmw puts the token's role claim (the user_roles rows at
	// issue time) in OwnerRoles, which derives control_owner.
	principals := []principal{
		{"password user", credstore.Credential{ID: "jwt:a", TenantID: tenant, UserID: "user:" + passwordUser.String(), OwnerRoles: []string{"admin"}}, []authz.Role{authz.RoleAdmin}, true},
		{"OIDC user", credstore.Credential{ID: "jwt:b", TenantID: tenant, UserID: "user:" + oidcUser.String(), OwnerRoles: []string{"auditor"}}, []authz.Role{authz.RoleAuditor}, true},
		{"JWT user, no user_roles row", credstore.Credential{ID: "jwt:c", TenantID: tenant, UserID: "user:" + ungrantedUser.String()}, nil, false},
		{"bound key", credstore.Credential{ID: "key_bound2239", TenantID: tenant, UserID: boundKeyUser.String()}, nil, false},
		{"unbound key", credstore.Credential{ID: "key_unbound2239", TenantID: tenant, UserID: "key_unbound2239"}, nil, false},
		{"OAuth client", credstore.Credential{ID: "jwt:d", TenantID: tenant, UserID: "oauth_client:ci2239"}, nil, false},
	}

	before, err := authz.NewEngine(context.Background(), rawKeyResolver{pool: appPool})
	if err != nil {
		t.Fatalf("NewEngine(before): %v", err)
	}
	after, err := authz.NewEngine(context.Background(), authz.NewDBRolesResolver(appPool))
	if err != nil {
		t.Fatalf("NewEngine(after): %v", err)
	}
	// expect decides on a fixed role set with no DB hop.
	expect, err := authz.NewEngine(context.Background(), authz.NoopRolesResolver{})
	if err != nil {
		t.Fatalf("NewEngine(expect): %v", err)
	}

	for _, p := range principals {
		gained := 0
		for _, rt := range routes2239 {
			b := decide2239(t, ctx, before, p.cred, rt, nil)
			a := decide2239(t, ctx, after, p.cred, rt, nil)
			if !p.changes {
				if a != b {
					t.Errorf("%s: %s %s changed %v -> %v; #2239 must not touch this principal", p.name, rt.method, rt.path, b, a)
				}
				continue
			}
			// Exactly the derived role plus the grant: never more.
			derived := authz.BuildInput(httptest.NewRequest(rt.method, rt.path, nil).WithContext(authctx.WithCredential(ctx, p.cred)), nil).User.Roles
			want := decide2239(t, ctx, expect, p.cred, rt, append(append([]authz.Role{}, derived...), p.grant...))
			if a != want {
				t.Errorf("%s: %s %s allow=%v, want %v (derived %v + grant %v)", p.name, rt.method, rt.path, a, want, derived, p.grant)
			}
			if b && !a {
				t.Errorf("%s: %s %s lost access; the role merge is a union and must only add", p.name, rt.method, rt.path)
			}
			if a && !b {
				gained++
			}
			t.Logf("%s: %s %s before=%v after=%v", p.name, rt.method, rt.path, b, a)
		}
		if p.changes && gained == 0 {
			t.Errorf("%s: grant %v changed no decision; the user: subject still misses user_roles", p.name, p.grant)
		}
	}
}

// TestDBRolesResolver2239_SubjectShapes pins the lookup key per principal
// shape against real Postgres.
func TestDBRolesResolver2239_SubjectShapes(t *testing.T) {
	t.Parallel()
	appPool := dbtest.NewAppPool(t)
	adminPool := dbtest.NewMigratePool(t)
	tenant := freshTenant(t, adminPool)
	ctx, err := tenancy.WithTenant(context.Background(), tenant)
	if err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
	u := uuid.New()
	if _, err := adminPool.Exec(context.Background(), `
		INSERT INTO user_roles (tenant_id, user_id, role, granted_by) VALUES
		  ($1, $2, 'auditor', 'test'),
		  ($1, 'key_x2239', 'viewer', 'test'),
		  ($1, 'oauth_client:x2239', 'control_owner', 'test')
	`, tenant, u.String()); err != nil {
		t.Fatalf("seed user_roles: %v", err)
	}
	r := authz.NewDBRolesResolver(appPool)
	for subject, want := range map[string]authz.Role{
		"user:" + u.String():       authz.RoleAuditor,
		u.String():                 authz.RoleAuditor,
		"key_x2239":                authz.RoleViewer,
		"oauth_client:x2239":       authz.RoleControlOwner,
		"user:" + uuid.NewString(): "",
	} {
		got, err := r.RolesFor(ctx, tenant, subject)
		if err != nil {
			t.Fatalf("RolesFor(%q): %v", subject, err)
		}
		if want == "" {
			if len(got) != 0 {
				t.Errorf("RolesFor(%q) = %v; want none", subject, got)
			}
			continue
		}
		if len(got) != 1 || got[0] != want {
			t.Errorf("RolesFor(%q) = %v; want [%s]", subject, got, want)
		}
	}
}
