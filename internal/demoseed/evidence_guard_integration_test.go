//go:build integration

// #1830: writeEvidence is the one production raw INSERT into
// evidence_records outside ingest (backdated demo data). Its guard,
// assertFreshDemoTenant, must refuse any tenant the current transaction
// did not create and demo-mark.

package demoseed

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mgoodric/security-atlas/internal/dbtest"
)

func TestAssertFreshDemoTenant(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	ctx := context.Background()

	inTx := func(fn func(tx pgx.Tx) error) error {
		t.Helper()
		tx, err := admin.Begin(ctx)
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		defer func() { _ = tx.Rollback(ctx) }()
		return fn(tx)
	}
	newTenant := func(tx pgx.Tx) tenantRow {
		t.Helper()
		id := uuid.New()
		tr := tenantRow{ID: id, Name: "Guard probe", Slug: "guard-" + id.String()[:8]}
		if err := writeTenantRow(ctx, tx, tr, uuid.Nil); err != nil {
			t.Fatalf("writeTenantRow: %v", err)
		}
		return tr
	}
	mark := func(tx pgx.Tx, tr tenantRow) {
		t.Helper()
		if err := writeAuditLogRow(ctx, tx, "demo_seed_apply", uuid.Nil, uuid.Nil, tr.ID,
			map[string]any{"slug": tr.Slug, "demo_seed_v": DemoSeedVersion}); err != nil {
			t.Fatalf("writeAuditLogRow: %v", err)
		}
	}

	t.Run("tenant created and marked in this tx passes", func(t *testing.T) {
		err := inTx(func(tx pgx.Tx) error {
			tr := newTenant(tx)
			mark(tx, tr)
			return assertFreshDemoTenant(ctx, tx, tr.ID)
		})
		if err != nil {
			t.Fatalf("assertFreshDemoTenant: %v", err)
		}
	})

	t.Run("unmarked tenant is refused", func(t *testing.T) {
		err := inTx(func(tx pgx.Tx) error {
			return assertFreshDemoTenant(ctx, tx, newTenant(tx).ID)
		})
		if !errors.Is(err, ErrNotDemoSeedContext) {
			t.Fatalf("got %v, want ErrNotDemoSeedContext", err)
		}
	})

	t.Run("existing tenant is refused", func(t *testing.T) {
		existing := dbtest.SeedTenant(t, admin)
		err := inTx(func(tx pgx.Tx) error {
			return assertFreshDemoTenant(ctx, tx, uuid.MustParse(existing))
		})
		if !errors.Is(err, ErrNotDemoSeedContext) {
			t.Fatalf("got %v, want ErrNotDemoSeedContext", err)
		}
	})
}
