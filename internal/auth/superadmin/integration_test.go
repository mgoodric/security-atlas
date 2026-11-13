//go:build integration

// integration_test.go — #1850: DBChecker against a real Postgres.
//
// The checker runs on the BYPASSRLS admin pool (DATABASE_URL), as
// cmd/atlas wires it, so the cross-tenant identity match can read
// `users` in every tenant. Skips when DATABASE_URL is unset.

package superadmin_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/auth/superadmin"
)

func openAdminPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL (BYPASSRLS admin pool) not set; skipping super_admins checker test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func exec(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("exec %q: %v", sql, err)
	}
}

// seedUser inserts a tenant and one active user in it with the given IdP
// identity.
func seedUser(t *testing.T, pool *pgxpool.Pool, idpIssuer, idpSubject string) uuid.UUID {
	t.Helper()
	tenantID, userID := uuid.New(), uuid.New()
	exec(t, pool, `INSERT INTO tenants (id, name, slug) VALUES ($1, $2, $3)`,
		tenantID, "T-"+tenantID.String()[:8], "t-"+tenantID.String()[:8])
	exec(t, pool, `INSERT INTO users (id, tenant_id, email, status, idp_issuer, idp_subject)
		VALUES ($1, $2, $3, 'active', $4, $5)`,
		userID, tenantID, userID.String()[:8]+"@example.test", idpIssuer, idpSubject)
	return userID
}

func grant(t *testing.T, pool *pgxpool.Pool, userID uuid.UUID) {
	t.Helper()
	exec(t, pool, `INSERT INTO super_admins (user_id, granted_via) VALUES ($1, 'manual_grant')`, userID)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM super_admins WHERE user_id = $1`, userID)
	})
}

func isSuperAdmin(t *testing.T, c *superadmin.DBChecker, id uuid.UUID) bool {
	t.Helper()
	ok, err := c.IsSuperAdmin(context.Background(), id)
	if err != nil {
		t.Fatalf("IsSuperAdmin: %v", err)
	}
	return ok
}

// TestIntegrationDBChecker_DemoteTakesEffect: a granted user reads true;
// after the row is deleted (what Demote does) the same user reads false.
func TestIntegrationDBChecker_DemoteTakesEffect(t *testing.T) {
	pool := openAdminPool(t)
	c := superadmin.NewDBChecker(pool)
	userID := uuid.New() // super_admins has no FK; a bare id is the direct-match arm

	if isSuperAdmin(t, c, userID) {
		t.Fatal("ungranted user reads as super_admin")
	}
	grant(t, pool, userID)
	if !isSuperAdmin(t, c, userID) {
		t.Fatal("granted user does not read as super_admin")
	}
	exec(t, pool, `DELETE FROM super_admins WHERE user_id = $1`, userID)
	if isSuperAdmin(t, c, userID) {
		t.Fatal("demoted user still reads as super_admin")
	}
}

// TestIntegrationDBChecker_CrossTenantIdentity: the super_admins row is
// keyed on the user's row in tenant A, and a token minted in tenant B
// carries tenant B's users.id. The checker must match through the shared
// (idp_issuer, idp_subject), as DBUserResolver does when it sets the
// claim, and must not match a different identity.
func TestIntegrationDBChecker_CrossTenantIdentity(t *testing.T) {
	pool := openAdminPool(t)
	c := superadmin.NewDBChecker(pool)

	issuer := "https://idp.example.test"
	subject := "sub-" + uuid.NewString()
	inA := seedUser(t, pool, issuer, subject)
	inB := seedUser(t, pool, issuer, subject)
	stranger := seedUser(t, pool, issuer, "sub-"+uuid.NewString())
	grant(t, pool, inA)

	if !isSuperAdmin(t, c, inB) {
		t.Error("same identity in another tenant does not read as super_admin")
	}
	if isSuperAdmin(t, c, stranger) {
		t.Error("a different identity reads as super_admin")
	}

	// A disabled membership row does not carry the grant across.
	exec(t, pool, `UPDATE users SET status = 'disabled' WHERE id = $1`, inA)
	if isSuperAdmin(t, c, inB) {
		t.Error("grant on a disabled membership row still carries across tenants")
	}
}

// TestIntegrationDBChecker_LocalUserNoIdentityMatch: users with an empty
// IdP identity (local mode) match only their own row, never each other.
func TestIntegrationDBChecker_LocalUserNoIdentityMatch(t *testing.T) {
	pool := openAdminPool(t)
	c := superadmin.NewDBChecker(pool)

	admin := seedUser(t, pool, "", "")
	other := seedUser(t, pool, "", "")
	grant(t, pool, admin)

	if !isSuperAdmin(t, c, admin) {
		t.Error("local super_admin does not read as super_admin")
	}
	if isSuperAdmin(t, c, other) {
		t.Error("another local user reads as super_admin via the empty identity")
	}
}
