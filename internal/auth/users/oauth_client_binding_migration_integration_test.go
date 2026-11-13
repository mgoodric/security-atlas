//go:build integration

// Integration tests for security-atlas#1739's upgrade migration: unbound
// OAuth clients are bound to the tenant on a single-tenant install, and
// left unbound on an install with no tenant or several.
//
// Lives beside the #1759 single-tenant upgrade test because both need the
// same harness: cleanupBootstrap empties tenants, so the migration's
// tenant count is the test's to set. Shares TestMain + the helpers in
// bootstrap_integration_test.go and bootstrap_single_tenant_integration_test.go.
package users_test

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
)

const oauthClientBindingUp = "../../../migrations/sql/20260811000000_oauth_client_tenant_binding.sql"

func applyOAuthClientBinding(t *testing.T) {
	t.Helper()
	up, err := os.ReadFile(oauthClientBindingUp)
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	if _, err := bootstrapAdminPool.Exec(context.Background(), string(up)); err != nil {
		t.Fatalf("apply migration: %v", err)
	}
}

// insertUnboundClient writes an oauth_clients row with no tenant, as every
// client issued before #1739 was, and removes it when the test ends.
func insertUnboundClient(t *testing.T) uuid.UUID {
	t.Helper()
	id := uuid.New()
	ctx := context.Background()
	if _, err := bootstrapAdminPool.Exec(ctx,
		`INSERT INTO oauth_clients (id, client_id, client_secret_hash, name)
		 VALUES ($1, $2, $3, $4)`,
		id, uuid.NewString(), "$argon2id$v=19$m=65536,t=1,p=4$placeholder$placeholder", "it-1739-mig-"+id.String()[:8],
	); err != nil {
		t.Fatalf("insert oauth client: %v", err)
	}
	t.Cleanup(func() {
		_, _ = bootstrapAdminPool.Exec(context.Background(), `DELETE FROM oauth_clients WHERE id = $1`, id)
	})
	return id
}

func clientTenant(t *testing.T, id uuid.UUID) *uuid.UUID {
	t.Helper()
	var tenant *uuid.UUID
	if err := bootstrapAdminPool.QueryRow(context.Background(),
		`SELECT tenant_id FROM oauth_clients WHERE id = $1`, id,
	).Scan(&tenant); err != nil {
		t.Fatalf("read client tenant: %v", err)
	}
	return tenant
}

func TestOAuthClientBinding_SingleTenantBindsUnboundClients(t *testing.T) {
	cleanupBootstrap(t)
	t.Cleanup(func() { cleanupBootstrap(t) })

	only := uuid.New()
	insertTenant(t, only, "Only Org", "", true)
	client := insertUnboundClient(t)

	applyOAuthClientBinding(t)

	got := clientTenant(t, client)
	if got == nil || *got != only {
		t.Fatalf("client tenant = %v, want the single tenant %s", got, only)
	}

	// Re-applying is a no-op for a bound client.
	applyOAuthClientBinding(t)
	if got := clientTenant(t, client); got == nil || *got != only {
		t.Fatalf("after re-apply client tenant = %v, want still %s", got, only)
	}
}

func TestOAuthClientBinding_TwoTenantsLeavesClientsUnbound(t *testing.T) {
	cleanupBootstrap(t)
	t.Cleanup(func() { cleanupBootstrap(t) })

	insertTenant(t, uuid.New(), "Org A", "org-a-1739", true)
	insertTenant(t, uuid.New(), "Org B", "org-b-1739", false)
	client := insertUnboundClient(t)

	applyOAuthClientBinding(t)

	if got := clientTenant(t, client); got != nil {
		t.Fatalf("client tenant = %s on a two-tenant install, want unbound", *got)
	}
}

func TestOAuthClientBinding_AllZerosTenantIsNotATenant(t *testing.T) {
	cleanupBootstrap(t)
	t.Cleanup(func() { cleanupBootstrap(t) })

	// The all-zeros row alone is not a single-tenant install.
	insertTenant(t, uuid.Nil, "nil tenant", "", false)
	client := insertUnboundClient(t)

	applyOAuthClientBinding(t)

	if got := clientTenant(t, client); got != nil {
		t.Fatalf("client bound to %s with only the all-zeros row present, want unbound", *got)
	}
}
