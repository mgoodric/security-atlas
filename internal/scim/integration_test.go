//go:build integration

// Integration tests for the slice 508 SCIM provisioning store + credential
// store. Requires Postgres reachable via DATABASE_URL_APP (atlas_app, RLS
// enforced) and DATABASE_URL (atlas_migrate, BYPASSRLS) for the auth
// lookup-by-hash path + cross-tenant seeding.
//
// These are the security proofs:
//   - P0-508-1 / AC-4: deprovision DISABLES + revokes sessions, never deletes.
//   - P0-508-3 / AC-7: a SCIM Patch attempting to set a role is ignored.
//   - P0-508-4 / AC-6: a tenant-A credential cannot read/mutate tenant-B users.
//   - AC-3:           a revoked SCIM token is rejected.
//   - AC-5:           every mutation writes an append-only audit row that
//     survives deprovision (the actor's history endures).
package scim_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/auth/bearer"
	"github.com/mgoodric/security-atlas/internal/scim"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

var (
	appPool   *pgxpool.Pool // atlas_app — RLS enforced
	adminPool *pgxpool.Pool // atlas_migrate — BYPASSRLS (auth lookup + seeding)
)

const testHashKey = "test-bearer-hash-key-at-least-32-bytes-long!!"

func TestMain(m *testing.M) {
	appURL := os.Getenv("DATABASE_URL_APP")
	if appURL == "" {
		fmt.Fprintln(os.Stderr, "DATABASE_URL_APP not set; skipping scim integration tests")
		os.Exit(0)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	p, err := pgxpool.New(ctx, appURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pgxpool.New app: %v\n", err)
		os.Exit(1)
	}
	appPool = p
	if adminURL := os.Getenv("DATABASE_URL"); adminURL != "" {
		a, aerr := pgxpool.New(ctx, adminURL)
		if aerr != nil {
			fmt.Fprintf(os.Stderr, "pgxpool.New admin: %v\n", aerr)
			os.Exit(1)
		}
		adminPool = a
	}
	code := m.Run()
	p.Close()
	if adminPool != nil {
		adminPool.Close()
	}
	os.Exit(code)
}

func requireAdminPool(t *testing.T) {
	t.Helper()
	if adminPool == nil {
		t.Skip("DATABASE_URL (atlas_migrate) not set; skipping test that needs BYPASSRLS")
	}
}

func hasher(t *testing.T) *bearer.Hasher {
	t.Helper()
	h, err := bearer.NewHasher([]byte(testHashKey))
	if err != nil {
		t.Fatalf("hasher: %v", err)
	}
	return h
}

// seedTenant creates a tenant via the BYPASSRLS pool and registers cleanup of
// every table the SCIM tests touch.
func seedTenant(t *testing.T, name string) uuid.UUID {
	t.Helper()
	requireAdminPool(t)
	id := uuid.New()
	if _, err := adminPool.Exec(context.Background(),
		`INSERT INTO tenants (id, name) VALUES ($1, $2)`, id, name); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		for _, table := range []string{
			"scim_audit_log", "scim_credentials", "sessions", "users",
		} {
			_, _ = adminPool.Exec(ctx, fmt.Sprintf(`DELETE FROM %s WHERE tenant_id = $1`, table), id)
		}
		_, _ = adminPool.Exec(ctx, `DELETE FROM tenants WHERE id = $1`, id)
	})
	return id
}

// tenantCtx returns a context carrying the tenant GUC, as the SCIM auth
// middleware would set after authenticating a credential for that tenant.
func tenantCtx(t *testing.T, tenantID uuid.UUID) context.Context {
	t.Helper()
	ctx, err := tenancy.WithTenant(context.Background(), tenantID.String())
	if err != nil {
		t.Fatalf("withTenant: %v", err)
	}
	return ctx
}

// seedSession inserts an active session for a user via the BYPASSRLS pool so
// the deprovision test can assert it is revoked.
func seedSession(t *testing.T, tenantID, userID uuid.UUID) string {
	t.Helper()
	id := "sess_" + uuid.New().String()
	_, err := adminPool.Exec(context.Background(),
		`INSERT INTO sessions (id, tenant_id, user_id, expires_at) VALUES ($1, $2, $3, now() + interval '7 days')`,
		id, tenantID, userID)
	if err != nil {
		t.Fatalf("seed session: %v", err)
	}
	return id
}

func sessionRevoked(t *testing.T, sessionID string) bool {
	t.Helper()
	var revoked bool
	err := adminPool.QueryRow(context.Background(),
		`SELECT revoked_at IS NOT NULL FROM sessions WHERE id = $1`, sessionID).Scan(&revoked)
	if err != nil {
		t.Fatalf("session lookup: %v", err)
	}
	return revoked
}

// --- credential store ---

// TestCredential_IssueAuthenticateRevoke proves issue → authenticate → revoke
// → reject (AC-3). Tenant comes back FROM authentication.
func TestCredential_IssueAuthenticateRevoke(t *testing.T) {
	requireAdminPool(t)
	tenant := seedTenant(t, "scim-cred")
	store := scim.NewCredentialStore(appPool, adminPool, hasher(t))
	store.SetPrefix(bearer.PrefixTest)
	ctx := tenantCtx(t, tenant)

	cred, plain, err := store.Issue(ctx, tenant.String(), "Okta prod", nil)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if cred.TenantID != tenant {
		t.Fatalf("cred tenant = %s; want %s", cred.TenantID, tenant)
	}

	// Authenticate runs WITHOUT a tenant context (BYPASSRLS lookup-by-hash).
	got, err := store.Authenticate(context.Background(), plain)
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	if got.ID != cred.ID || got.TenantID != tenant {
		t.Fatalf("authenticated cred mismatch: %+v", got)
	}

	// Revoke, then re-authenticate must reject (AC-3).
	if err := store.Revoke(ctx, tenant.String(), cred.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if _, err := store.Authenticate(context.Background(), plain); err != scim.ErrUnknownCredential {
		t.Fatalf("revoked token authenticate err = %v; want ErrUnknownCredential", err)
	}
}

// --- provisioning store ---

func newProvStore() *scim.Store { return scim.NewStore(appPool) }

// TestProvision_GetList exercises the basic CRUD + the userName filter.
func TestProvision_GetList(t *testing.T) {
	requireAdminPool(t)
	tenant := seedTenant(t, "scim-prov")
	store := newProvStore()
	ctx := tenantCtx(t, tenant)
	actor := uuid.New()

	u, err := store.Provision(ctx, actor, tenant.String(), scim.ProvisionInput{
		UserName: "alice@example.com", DisplayName: "Alice", ExternalID: "ext-alice", Active: true,
	})
	if err != nil {
		t.Fatalf("provision: %v", err)
	}
	if !u.Active || u.Email != "alice@example.com" || u.ExternalID != "ext-alice" {
		t.Fatalf("provisioned user wrong: %+v", u)
	}

	got, err := store.GetByID(ctx, tenant.String(), u.ID)
	if err != nil || got.ID != u.ID {
		t.Fatalf("get: %v (%+v)", err, got)
	}

	// Duplicate userName → ErrConflict.
	if _, err := store.Provision(ctx, actor, tenant.String(), scim.ProvisionInput{
		UserName: "alice@example.com", Active: true,
	}); err != scim.ErrConflict {
		t.Fatalf("dup provision err = %v; want ErrConflict", err)
	}

	// Filter by userName.
	found, err := store.FindByUserName(ctx, tenant.String(), "ALICE@EXAMPLE.COM")
	if err != nil || len(found) != 1 || found[0].ID != u.ID {
		t.Fatalf("filter: %v (%d found)", err, len(found))
	}

	// List returns the user + total.
	list, total, err := store.List(ctx, tenant.String(), 50, 0)
	if err != nil || total != 1 || len(list) != 1 {
		t.Fatalf("list: %v total=%d len=%d", err, total, len(list))
	}
}

// TestDeprovision_DisablesAndRevokesSessions is the P0-508-1 / AC-4 proof:
// active=false disables + revokes sessions but does NOT hard-delete; the user
// row + its audit history survive.
func TestDeprovision_DisablesAndRevokesSessions(t *testing.T) {
	requireAdminPool(t)
	tenant := seedTenant(t, "scim-deprov")
	store := newProvStore()
	ctx := tenantCtx(t, tenant)
	actor := uuid.New()

	u, err := store.Provision(ctx, actor, tenant.String(), scim.ProvisionInput{
		UserName: "bob@example.com", DisplayName: "Bob", ExternalID: "ext-bob", Active: true,
	})
	if err != nil {
		t.Fatalf("provision: %v", err)
	}
	sess := seedSession(t, tenant, u.ID)

	// Deprovision via Patch active=false.
	patched, err := store.Patch(ctx, actor, tenant.String(), u.ID, []scim.PatchOperation{
		{Op: "replace", Path: "active", Value: []byte(`false`)},
	})
	if err != nil {
		t.Fatalf("patch deprovision: %v", err)
	}
	if patched.Active {
		t.Fatal("user should be inactive after deprovision")
	}

	// The user row STILL EXISTS (not hard-deleted) — P0-508-1.
	got, err := store.GetByID(ctx, tenant.String(), u.ID)
	if err != nil {
		t.Fatalf("user must survive deprovision: %v", err)
	}
	if got.Active {
		t.Fatal("survived user should be inactive")
	}

	// The session is revoked (AC-4).
	if !sessionRevoked(t, sess) {
		t.Fatal("session must be revoked on deprovision")
	}

	// The status text column mirrors the boolean.
	var status string
	if err := adminPool.QueryRow(context.Background(),
		`SELECT status FROM users WHERE id = $1`, u.ID).Scan(&status); err != nil {
		t.Fatalf("status read: %v", err)
	}
	if status != "disabled" {
		t.Fatalf("status = %q; want disabled", status)
	}

	// Audit rows survive + record provision + deprovision (AC-5).
	rows := auditActions(t, tenant)
	if !contains(rows, scim.ActionProvision) || !contains(rows, scim.ActionDeprovision) {
		t.Fatalf("audit log missing provision/deprovision: %v", rows)
	}

	// DELETE also soft-disables (no hard delete) — re-enable then DELETE.
	if _, err := store.Patch(ctx, actor, tenant.String(), u.ID, []scim.PatchOperation{
		{Op: "replace", Path: "active", Value: []byte(`true`)},
	}); err != nil {
		t.Fatalf("reprovision: %v", err)
	}
	if err := store.Delete(ctx, actor, tenant.String(), u.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := store.GetByID(ctx, tenant.String(), u.ID); err != nil {
		t.Fatalf("DELETE must NOT hard-delete the row: %v", err)
	}
}

// TestPatch_NoRoleEscalation is the P0-508-3 / AC-7 integration proof: a SCIM
// Patch carrying a `roles` op leaves user_roles UNTOUCHED. We seed zero roles
// and assert still zero after the patch.
func TestPatch_NoRoleEscalation(t *testing.T) {
	requireAdminPool(t)
	tenant := seedTenant(t, "scim-noesc")
	store := newProvStore()
	ctx := tenantCtx(t, tenant)
	actor := uuid.New()

	u, err := store.Provision(ctx, actor, tenant.String(), scim.ProvisionInput{
		UserName: "carol@example.com", Active: true,
	})
	if err != nil {
		t.Fatalf("provision: %v", err)
	}

	// Patch tries to set roles AND a legit displayName.
	if _, err := store.Patch(ctx, actor, tenant.String(), u.ID, []scim.PatchOperation{
		{Op: "replace", Path: "roles", Value: []byte(`["admin"]`)},
		{Op: "replace", Path: "displayName", Value: []byte(`"Carol C"`)},
	}); err != nil {
		t.Fatalf("patch: %v", err)
	}

	// user_roles must have ZERO rows for this user (roles were dropped).
	var count int
	if err := adminPool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM user_roles WHERE tenant_id = $1 AND user_id = $2`,
		tenant, u.ID.String()).Scan(&count); err != nil {
		t.Fatalf("role count: %v", err)
	}
	if count != 0 {
		t.Fatalf("SCIM patch escalated roles: user_roles count = %d", count)
	}

	// The legit displayName DID apply (proving the patch ran, roles were the
	// only thing dropped).
	got, _ := store.GetByID(ctx, tenant.String(), u.ID)
	if got.DisplayName != "Carol C" {
		t.Fatalf("displayName not applied: %q", got.DisplayName)
	}
}

// TestCrossTenant_RLS is the P0-508-4 / AC-6 proof: a tenant-A context cannot
// read or mutate a tenant-B user. RLS denies the cross-tenant query, so a Get
// returns ErrUserNotFound (no oracle) and a Patch returns ErrUserNotFound.
func TestCrossTenant_RLS(t *testing.T) {
	requireAdminPool(t)
	tenantA := seedTenant(t, "scim-tenantA")
	tenantB := seedTenant(t, "scim-tenantB")
	store := newProvStore()
	actor := uuid.New()

	// Provision a user in tenant B.
	ctxB := tenantCtx(t, tenantB)
	bUser, err := store.Provision(ctxB, actor, tenantB.String(), scim.ProvisionInput{
		UserName: "dave@example.com", ExternalID: "ext-dave", Active: true,
	})
	if err != nil {
		t.Fatalf("provision B: %v", err)
	}

	// Under tenant A's context, tenant B's user is invisible (P0-508-4).
	ctxA := tenantCtx(t, tenantA)
	if _, err := store.GetByID(ctxA, tenantA.String(), bUser.ID); err != scim.ErrUserNotFound {
		t.Fatalf("cross-tenant Get err = %v; want ErrUserNotFound", err)
	}
	// A cross-tenant deprovision must also fail to find the row.
	if _, err := store.Patch(ctxA, actor, tenantA.String(), bUser.ID, []scim.PatchOperation{
		{Op: "replace", Path: "active", Value: []byte(`false`)},
	}); err != scim.ErrUserNotFound {
		t.Fatalf("cross-tenant Patch err = %v; want ErrUserNotFound", err)
	}
	// Tenant A's list does not include tenant B's user.
	list, total, err := store.List(ctxA, tenantA.String(), 50, 0)
	if err != nil {
		t.Fatalf("list A: %v", err)
	}
	if total != 0 || len(list) != 0 {
		t.Fatalf("tenant A sees %d users; want 0 (cross-tenant leak)", total)
	}

	// The tenant B user is still active (tenant A's failed patch was a no-op).
	gotB, err := store.GetByID(ctxB, tenantB.String(), bUser.ID)
	if err != nil || !gotB.Active {
		t.Fatalf("tenant B user should be untouched: %v (%+v)", err, gotB)
	}
}

// --- helpers ---

func auditActions(t *testing.T, tenant uuid.UUID) []string {
	t.Helper()
	rows, err := adminPool.Query(context.Background(),
		`SELECT action FROM scim_audit_log WHERE tenant_id = $1 ORDER BY occurred_at ASC`, tenant)
	if err != nil {
		t.Fatalf("audit query: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out = append(out, a)
	}
	return out
}

func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}
