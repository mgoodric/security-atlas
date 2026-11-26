//go:build integration

// #3094: a teardown that cannot get the audit_periods lock within its
// timeout fails with ErrTeardownBusy, so the caller can retry, and leaves
// every row of the tenant as it was.

package demoseed_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/demoseed"
)

// holdAuditPeriodsWriteLock takes ROW EXCLUSIVE on audit_periods, the lock
// any INSERT, UPDATE or DELETE on it takes, in a transaction of its own,
// until the returned release is called (or the test ends).
func holdAuditPeriodsWriteLock(t *testing.T) (release func()) {
	t.Helper()
	ctx := context.Background()
	holder, err := adminPool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin holder: %v", err)
	}
	if _, err := holder.Exec(ctx, `LOCK TABLE audit_periods IN ROW EXCLUSIVE MODE`); err != nil {
		_ = holder.Rollback(ctx)
		t.Fatalf("holder lock: %v", err)
	}
	release = func() { _ = holder.Rollback(ctx) }
	t.Cleanup(release)
	return release
}

func TestTeardown_BusyAuditPeriods_FailsRetryablyAndKeepsEveryRow(t *testing.T) {
	ctx := context.Background()
	for name, setup := range map[string]func(t *testing.T) (uuid.UUID, func() error){
		"demo tenant": func(t *testing.T) (uuid.UUID, func() error) {
			const slug = "demo-it-td-busy"
			cleanupTenant(t, slug)
			seeder, err := demoseed.NewSeeder(adminPool, demoseed.DefaultScale)
			if err != nil {
				t.Fatalf("NewSeeder: %v", err)
			}
			res, err := seeder.Apply(ctx, demoseed.ApplyInput{Slug: slug})
			if err != nil {
				t.Fatalf("Apply: %v", err)
			}
			return res.TenantID, func() error { return seeder.Teardown(ctx, slug, uuid.Nil, uuid.Nil) }
		},
		"in place": func(t *testing.T) (uuid.UUID, func() error) {
			ref := freshInstallTenant(t)
			seeder := soleTenantSeeder(t, ref)
			if _, err := seeder.Seed(ctx, demoseed.SeedInput{}); err != nil {
				t.Fatalf("Seed: %v", err)
			}
			return ref.ID, func() error {
				_, err := seeder.TeardownImplicit(ctx, uuid.Nil, uuid.Nil)
				return err
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			tenantID, teardown := setup(t)
			seeded := tenantRows(t, tenantID)
			if len(seeded["audit_periods"]) == 0 {
				t.Fatal("seed wrote no audit period; the teardown would never take the lock")
			}

			release := holdAuditPeriodsWriteLock(t)
			err := teardown()
			if !errors.Is(err, demoseed.ErrTeardownBusy) {
				t.Fatalf("teardown under a held audit_periods lock: want ErrTeardownBusy, got %v", err)
			}
			if d := diffRows(seeded, tenantRows(t, tenantID)); len(d) > 0 {
				t.Fatalf("busy teardown changed %d rows; first: %s", len(d), d[0])
			}

			// The retry the error asks for works once the writer is gone.
			release()
			if err := teardown(); err != nil {
				t.Fatalf("teardown after the lock is released: %v", err)
			}
		})
	}
}
