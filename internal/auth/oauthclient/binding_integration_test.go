//go:build integration

// Integration tests for security-atlas#1739: an OAuth client is bound to
// the tenant its client_credentials tokens act in.
//
//   - IssueForTenant persists the tenant; Verify and Lookup return it.
//   - IssueForTenant refuses the all-zeros tenant.
//   - Bind binds an unbound client, is a no-op re-binding to the same
//     tenant, and refuses a client bound elsewhere (ErrAlreadyBound), a
//     disabled or missing client (ErrUnknownClient) and uuid.Nil.
//
// oauth_clients.bound_tenant_id is a foreign key to tenants, so each test seeds
// real tenant rows through the BYPASSRLS migrate pool.
package oauthclient_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/auth/oauthclient"
	"github.com/mgoodric/security-atlas/internal/dbtest"
)

// seedTenantRow inserts a tenants row and removes it (and any client still
// bound to it) when the test ends.
func seedTenantRow(t *testing.T, migrate *pgxpool.Pool) uuid.UUID {
	t.Helper()
	id := uuid.New()
	ctx := context.Background()
	if _, err := migrate.Exec(ctx,
		`INSERT INTO tenants (id, name) VALUES ($1, $2)`, id, "oauthclient-1739-"+id.String()[:8],
	); err != nil {
		t.Fatalf("insert tenant: %v", err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = migrate.Exec(ctx, `DELETE FROM oauth_clients WHERE bound_tenant_id = $1`, id)
		_, _ = migrate.Exec(ctx, `DELETE FROM tenants WHERE id = $1`, id)
	})
	return id
}

func TestIssueForTenant_PersistsTenant(t *testing.T) {
	pool := dbtest.NewAppPool(t)
	tenant := seedTenantRow(t, dbtest.NewMigratePool(t))
	s := oauthclient.New(pool)
	ctx := context.Background()

	c, secret, err := s.IssueForTenant(ctx, uniqueName("ut-1739-issue"), tenant)
	if err != nil {
		t.Fatalf("IssueForTenant: %v", err)
	}
	if c.TenantID == nil || *c.TenantID != tenant {
		t.Fatalf("issued TenantID = %v, want %s", c.TenantID, tenant)
	}
	got, err := s.Verify(ctx, c.ClientID, secret)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if got.TenantID == nil || *got.TenantID != tenant {
		t.Errorf("Verify TenantID = %v, want %s", got.TenantID, tenant)
	}
	looked, err := s.Lookup(ctx, c.ClientID)
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if looked.TenantID == nil || *looked.TenantID != tenant {
		t.Errorf("Lookup TenantID = %v, want %s", looked.TenantID, tenant)
	}
}

func TestIssue_UnboundHasNoTenant(t *testing.T) {
	pool := dbtest.NewAppPool(t)
	s := oauthclient.New(pool)
	ctx := context.Background()

	c, secret, err := s.Issue(ctx, uniqueName("ut-1739-unbound"))
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	got, err := s.Verify(ctx, c.ClientID, secret)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if got.TenantID != nil {
		t.Errorf("unbound client TenantID = %s, want nil", *got.TenantID)
	}
}

func TestIssueForTenant_RefusesAllZeros(t *testing.T) {
	s := oauthclient.New(dbtest.NewAppPool(t))
	_, _, err := s.IssueForTenant(context.Background(), uniqueName("ut-1739-nil"), uuid.Nil)
	if !errors.Is(err, oauthclient.ErrNilTenant) {
		t.Fatalf("err = %v, want ErrNilTenant", err)
	}
}

func TestBind_UnboundThenIdempotentThenRefusesOtherTenant(t *testing.T) {
	pool := dbtest.NewAppPool(t)
	migrate := dbtest.NewMigratePool(t)
	tenantA := seedTenantRow(t, migrate)
	tenantB := seedTenantRow(t, migrate)
	s := oauthclient.New(pool)
	ctx := context.Background()

	c, secret, err := s.Issue(ctx, uniqueName("ut-1739-bind"))
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	bound, err := s.Bind(ctx, c.ClientID, tenantA)
	if err != nil {
		t.Fatalf("Bind unbound -> A: %v", err)
	}
	if bound.TenantID == nil || *bound.TenantID != tenantA {
		t.Fatalf("Bind TenantID = %v, want %s", bound.TenantID, tenantA)
	}
	if _, err := s.Bind(ctx, c.ClientID, tenantA); err != nil {
		t.Fatalf("Bind A -> A (idempotent): %v", err)
	}
	if _, err := s.Bind(ctx, c.ClientID, tenantB); !errors.Is(err, oauthclient.ErrAlreadyBound) {
		t.Fatalf("Bind A -> B: err = %v, want ErrAlreadyBound", err)
	}
	got, err := s.Verify(ctx, c.ClientID, secret)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if got.TenantID == nil || *got.TenantID != tenantA {
		t.Errorf("after refused rebind TenantID = %v, want still %s", got.TenantID, tenantA)
	}
}

func TestBind_RefusesUnknownDisabledAndNil(t *testing.T) {
	pool := dbtest.NewAppPool(t)
	tenant := seedTenantRow(t, dbtest.NewMigratePool(t))
	s := oauthclient.New(pool)
	ctx := context.Background()

	if _, err := s.Bind(ctx, uuid.NewString(), tenant); !errors.Is(err, oauthclient.ErrUnknownClient) {
		t.Errorf("Bind(unknown): err = %v, want ErrUnknownClient", err)
	}
	if _, err := s.Bind(ctx, "", tenant); !errors.Is(err, oauthclient.ErrUnknownClient) {
		t.Errorf("Bind(empty): err = %v, want ErrUnknownClient", err)
	}

	c, _, err := s.Issue(ctx, uniqueName("ut-1739-bind-disabled"))
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if _, err := s.Bind(ctx, c.ClientID, uuid.Nil); !errors.Is(err, oauthclient.ErrNilTenant) {
		t.Errorf("Bind(nil tenant): err = %v, want ErrNilTenant", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE oauth_clients SET disabled_at = now() WHERE id = $1`, c.ID); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if _, err := s.Bind(ctx, c.ClientID, tenant); !errors.Is(err, oauthclient.ErrUnknownClient) {
		t.Errorf("Bind(disabled): err = %v, want ErrUnknownClient", err)
	}
}
