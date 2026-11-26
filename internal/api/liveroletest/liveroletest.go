//go:build integration

// Package liveroletest authenticates integration tests the way production
// does (#2507). A role claim alone is not a grant: authzmw resolves the
// caller's live user_roles and stamps them on the credential
// (cred.TenantRoles), and the handler gates read those. A test that mints
// testjwt.OwnerFor(tenant, roles) on a server with no authz engine has a
// role claim and no live roles, a shape production never has.
//
// The fixture is two calls:
//
//	srv := api.New(api.Config{})
//	srv.AttachDB(app)
//	liveroletest.Attach(t, srv, app)         // before HTTPHandlerForTests
//	bearer := liveroletest.Bearer(t, srv, tenant, "grc_engineer")
//
// Bearer seeds a users row and its user_roles rows and returns a sign-in
// JWT whose Subject is "user:<uuid>", so the DB roles resolver finds them.
// Roles must be canonical (authz.CanonicalRoles); user_roles' CHECK
// refuses anything else.
package liveroletest

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api"
	"github.com/mgoodric/security-atlas/internal/api/testjwt"
	"github.com/mgoodric/security-atlas/internal/authz"
	"github.com/mgoodric/security-atlas/internal/dbtest"
)

// Attach attaches the real OPA bundle with the DB roles resolver over app,
// and the decision audit writer cmd/atlas attaches, so every decision
// writes its decision_audit_log row as in production (#3016). Call it
// before srv.HTTPHandlerForTests: the router reads the engine when it is
// built.
func Attach(t *testing.T, srv *api.Server, app *pgxpool.Pool) {
	t.Helper()
	engine, err := authz.NewEngine(context.Background(), authz.NewDBRolesResolver(app))
	if err != nil {
		t.Fatalf("authz.NewEngine: %v", err)
	}
	srv.AttachAuthz(engine, authz.NewAuditWriter(app))
}

// Bearer seeds a user holding roles in tenant and returns a sign-in JWT
// for it. The token's role claim carries the same roles, as a real
// sign-in's does; the gates count the live rows, not the claim.
func Bearer(t *testing.T, srv *api.Server, tenant string, roles ...string) string {
	t.Helper()
	_, bearer := Person(t, srv, tenant, roles...)
	return bearer
}

// Person is Bearer for a test that also needs the seeded user's id, to
// assert on an actor column or to name the user in a request.
func Person(t *testing.T, srv *api.Server, tenant string, roles ...string) (uuid.UUID, string) {
	t.Helper()
	user := uuid.New()
	return user, PersonAs(t, srv, tenant, user, roles...)
}

// PersonAs is Person for a caller that picks the user id, to name the same
// person across several sessions. It seeds user, so call it once per id;
// mint each further session with Session.
func PersonAs(t *testing.T, srv *api.Server, tenant string, user uuid.UUID, roles ...string) string {
	t.Helper()
	seedUser(t, tenant, user, roles...)
	return Session(t, srv, tenant, user, roles...)
}

// Session mints another sign-in JWT for a user PersonAs or Person already
// seeded. It seeds nothing.
func Session(t *testing.T, srv *api.Server, tenant string, user uuid.UUID, roles ...string) string {
	t.Helper()
	claims := testjwt.OwnerFor(uuid.MustParse(tenant), roles)
	claims.Subject = "user:" + user.String()
	return srv.IssueTestJWT(t, claims)
}

// Seed seeds a user holding roles in tenant and returns its id, minting
// nothing: for a harness that signs its own tokens (its own
// AttachJWTValidator signer). The token's Subject is "user:<id>".
func Seed(t *testing.T, tenant string, roles ...string) uuid.UUID {
	t.Helper()
	user := uuid.New()
	seedUser(t, tenant, user, roles...)
	return user
}

// seedUser inserts an active users row user in tenant holding roles,
// removed at cleanup.
func seedUser(t *testing.T, tenant string, user uuid.UUID, roles ...string) {
	t.Helper()
	for _, r := range roles {
		if !authz.IsCanonical(authz.Role(r)) {
			t.Fatalf("liveroletest: %q is not a canonical role", r)
		}
	}
	ctx := context.Background()
	admin := dbtest.NewMigratePool(t)
	if _, err := admin.Exec(ctx,
		`INSERT INTO users (id, tenant_id, email, display_name, status) VALUES ($1, $2, $3, 'liveroletest', 'active')`,
		user, tenant, "liveroletest-"+user.String()+"@example.invalid"); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	for _, r := range roles {
		if _, err := admin.Exec(ctx,
			`INSERT INTO user_roles (tenant_id, user_id, role, granted_by) VALUES ($1, $2, $3, 'liveroletest')`,
			tenant, user.String(), r); err != nil {
			t.Fatalf("seed user_roles %s: %v", r, err)
		}
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(ctx, `DELETE FROM user_roles WHERE tenant_id = $1 AND user_id = $2`, tenant, user.String())
		_, _ = admin.Exec(ctx, `DELETE FROM users WHERE id = $1`, user)
	})
}
