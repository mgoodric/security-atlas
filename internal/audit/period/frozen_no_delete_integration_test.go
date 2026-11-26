//go:build integration

// #3081: a frozen audit period cannot be deleted, by atlas_app or by the
// owner, so its frozen_hash and the control set its freeze recorded
// (audit_period_frozen_controls, which cascades from the period) outlive any
// DELETE. An open period can still be deleted. Migration
// 20261313000000_audit_period_frozen_no_delete.

package period_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/dbtest"
)

// periodRows returns how many audit_periods and audit_period_frozen_controls
// rows id has, read as the owner.
func periodRows(t *testing.T, admin *pgxpool.Pool, id uuid.UUID) (periods, snapshots int) {
	t.Helper()
	if err := admin.QueryRow(context.Background(), `
		SELECT (SELECT count(*) FROM audit_periods WHERE id = $1),
		       (SELECT count(*) FROM audit_period_frozen_controls WHERE audit_period_id = $1)`,
		id).Scan(&periods, &snapshots); err != nil {
		t.Fatalf("count period rows: %v", err)
	}
	return periods, snapshots
}

func TestFrozenPeriod_DeleteRefused(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant, p, _ := frozenPeriod(t, admin, app)
	ctx := context.Background()

	// Detach populations, the period's only ON DELETE RESTRICT child here,
	// so nothing but the trigger stands between the app and the DELETE.
	if _, err := admin.Exec(ctx, `UPDATE populations SET audit_period_id = NULL WHERE audit_period_id = $1`, p.ID); err != nil {
		t.Fatalf("detach populations: %v", err)
	}

	n, err := execAsApp(t, app, tenant, `DELETE FROM audit_periods WHERE id = $1`, p.ID)
	wantCheckViolation(t, "app delete of a frozen period", err)
	if n != 0 {
		t.Fatalf("app delete affected %d rows, want 0", n)
	}
	_, err = admin.Exec(ctx, `DELETE FROM audit_periods WHERE id = $1`, p.ID)
	wantCheckViolation(t, "owner delete of a frozen period", err)

	if periods, snapshots := periodRows(t, admin, p.ID); periods != 1 || snapshots != 1 {
		t.Fatalf("after refused deletes: periods=%d snapshots=%d, want 1 and 1", periods, snapshots)
	}
}

func TestOpenPeriod_AppCanDelete(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant, p := openPeriod(t, admin, app)

	n, err := execAsApp(t, app, tenant, `DELETE FROM audit_periods WHERE id = $1`, p.ID)
	if err != nil || n != 1 {
		t.Fatalf("app delete of an open period: n=%d err=%v, want 1 row", n, err)
	}
	if periods, _ := periodRows(t, admin, p.ID); periods != 0 {
		t.Fatalf("open period still present after delete")
	}
}

// The owner teardown path: dbtest.DeleteAuditPeriods disables the trigger for
// its own transaction only, the delete cascades to the snapshot, and the
// trigger is enabled again afterwards.
func TestFrozenPeriod_OwnerTeardown(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	_, p, _ := frozenPeriod(t, admin, app)
	ctx := context.Background()

	if _, err := admin.Exec(ctx, `UPDATE populations SET audit_period_id = NULL WHERE audit_period_id = $1`, p.ID); err != nil {
		t.Fatalf("detach populations: %v", err)
	}
	if err := dbtest.DeleteAuditPeriods(ctx, admin, `id = $1`, p.ID); err != nil {
		t.Fatalf("owner teardown: %v", err)
	}
	if periods, snapshots := periodRows(t, admin, p.ID); periods != 0 || snapshots != 0 {
		t.Fatalf("after teardown: periods=%d snapshots=%d, want 0 and 0", periods, snapshots)
	}

	var enabled string
	if err := admin.QueryRow(ctx,
		`SELECT tgenabled FROM pg_trigger WHERE tgname = 'audit_periods_frozen_no_delete_trg'`).Scan(&enabled); err != nil {
		t.Fatalf("read trigger state: %v", err)
	}
	if enabled != "O" {
		t.Fatalf("audit_periods_frozen_no_delete_trg tgenabled = %q after teardown, want O", enabled)
	}
}
