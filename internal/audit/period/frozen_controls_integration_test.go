//go:build integration

// #3066: the freeze hash's control set is recorded at freeze
// (audit_period_frozen_controls) and every replay reads it, so creating or
// deleting a control after a freeze changes no revision's hash. Migration
// 20261311000000_audit_period_frozen_controls, which also backfills periods
// frozen before it.

package period_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/audit/period"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

const frozenControlsMigration = "../../../migrations/sql/20261311000000_audit_period_frozen_controls.sql"

// amendWith cuts a freeze revision of p adding one record observed inside
// the horizon but ingested after the freeze, through period.AmendInTx as
// atlas_app, and returns the revision.
func amendWith(t *testing.T, admin, app *pgxpool.Pool, tenant string, p period.Period, ctrlID uuid.UUID) period.Revision {
	t.Helper()
	recID := uuid.New()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO evidence_records (
			id, tenant_id, control_id, observed_at, ingested_at,
			provenance, result, payload, hash, control_ref
		)
		VALUES ($1, $2, $3, $4, now(), '{}'::jsonb, 'pass', '{}'::jsonb, $5, 'test')`,
		recID, tenant, ctrlID, p.FrozenAt.Add(-time.Minute), "hash-3066-"+recID.String()); err != nil {
		t.Fatalf("seed late record: %v", err)
	}
	ctx, err := tenancy.WithTenant(context.Background(), tenant)
	if err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
	var rev period.Revision
	err = pgx.BeginTxFunc(ctx, app, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := tenancy.ApplyTenant(ctx, tx); err != nil {
			return err
		}
		q := dbx.New(tx)
		row, err := q.GetAuditPeriodByID(ctx, dbx.GetAuditPeriodByIDParams{
			TenantID: pgtype.UUID{Bytes: uuid.MustParse(tenant), Valid: true},
			ID:       pgtype.UUID{Bytes: p.ID, Valid: true},
		})
		if err != nil {
			return err
		}
		rev, err = period.AmendInTx(ctx, q, row, period.AmendInput{
			ImportBatchID:     uuid.New(),
			Actor:             "key_test_3066",
			Reason:            "#3066 amendment",
			EvidenceRecordIDs: []uuid.UUID{recID},
		})
		return err
	})
	if err != nil {
		t.Fatalf("AmendInTx: %v", err)
	}
	return rev
}

// seedOtherControl adds a control in its own bundle, so it does not collide
// with seedControl's one active version per bundle.
func seedOtherControl(t *testing.T, admin *pgxpool.Pool, tenant string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO controls (id, tenant_id, title, control_family, implementation_type, bundle_id)
		VALUES ($1, $2, '#3066 test control', 'AAA', 'automated', $3)`,
		id, tenant, "test-bundle-3066-"+id.String()); err != nil {
		t.Fatalf("seed control: %v", err)
	}
	return id
}

// removeSnapshot deletes p's recorded control set as the owner, with the
// immutability trigger disabled for that transaction only: the state of a
// period frozen before the migration.
func removeSnapshot(t *testing.T, admin *pgxpool.Pool, id uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	err := pgx.BeginTxFunc(ctx, admin, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `ALTER TABLE audit_period_frozen_controls DISABLE TRIGGER audit_period_frozen_controls_immutable_trg`); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `DELETE FROM audit_period_frozen_controls WHERE audit_period_id = $1`, id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return errors.New("no snapshot row to remove")
		}
		_, err = tx.Exec(ctx, `ALTER TABLE audit_period_frozen_controls ENABLE TRIGGER audit_period_frozen_controls_immutable_trg`)
		return err
	})
	if err != nil {
		t.Fatalf("remove snapshot: %v", err)
	}
}

// runBackfill executes section 3 of the migration file verbatim as the
// migrate role.
func runBackfill(t *testing.T, admin *pgxpool.Pool) {
	t.Helper()
	b, err := os.ReadFile(frozenControlsMigration)
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	_, section, ok := strings.Cut(string(b), "-- ===== 3. backfill already-frozen periods =====")
	if !ok {
		t.Fatal("migration has no backfill section")
	}
	if _, err := admin.Exec(context.Background(), section); err != nil {
		t.Fatalf("run backfill: %v", err)
	}
}

func snapshotSource(t *testing.T, admin *pgxpool.Pool, id uuid.UUID) string {
	t.Helper()
	var src string
	if err := admin.QueryRow(context.Background(),
		`SELECT source FROM audit_period_frozen_controls WHERE audit_period_id = $1`, id).Scan(&src); err != nil {
		t.Fatalf("read snapshot source: %v", err)
	}
	return src
}

func replay(t *testing.T, store *period.Store, ctx context.Context, id uuid.UUID, rev int) []byte {
	t.Helper()
	h, err := store.ReplayHash(ctx, id, rev)
	if err != nil {
		t.Fatalf("ReplayHash(%d): %v", rev, err)
	}
	return h
}

// The acceptance test: freeze, cut a revision, then create and delete
// controls. Revision 1 and revision 2 both still replay.
func TestFrozenControls_CatalogChangesDoNotBreakReplay(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant, p, _ := frozenPeriod(t, admin, app)
	var ctrlID uuid.UUID
	if err := admin.QueryRow(context.Background(),
		`SELECT id FROM controls WHERE tenant_id = $1`, tenant).Scan(&ctrlID); err != nil {
		t.Fatalf("read control: %v", err)
	}
	// A second control that exists at freeze time and is deleted later.
	// frozenPeriod already froze, so seed it, reopen, and freeze again.
	doomed := seedOtherControl(t, admin, tenant)
	reopenFrozenPeriod(t, admin, p.ID)
	store := period.NewStore(app)
	ctx := dbtest.WithTenantCtx(t, tenant)
	p, err := store.Freeze(ctx, p.ID, "key_test_3066", *p.FrozenAt)
	if err != nil {
		t.Fatalf("re-Freeze: %v", err)
	}
	if src := snapshotSource(t, admin, p.ID); src != "freeze" {
		t.Fatalf("snapshot source = %q, want freeze", src)
	}
	rev := amendWith(t, admin, app, tenant, p, ctrlID)

	if h := replay(t, store, ctx, p.ID, 1); !bytes.Equal(h, p.FrozenHash) {
		t.Fatalf("before catalog change: ReplayHash(1) = %x, want %x", h, p.FrozenHash)
	}

	// Create a control and delete one that the freeze hashed.
	seedOtherControl(t, admin, tenant)
	if tag, err := admin.Exec(context.Background(), `DELETE FROM controls WHERE id = $1`, doomed); err != nil || tag.RowsAffected() != 1 {
		t.Fatalf("delete control: rows=%d err=%v", tag.RowsAffected(), err)
	}

	if h := replay(t, store, ctx, p.ID, 1); !bytes.Equal(h, p.FrozenHash) {
		t.Fatalf("after catalog change: ReplayHash(1) = %x, want frozen_hash %x", h, p.FrozenHash)
	}
	if h := replay(t, store, ctx, p.ID, 2); !bytes.Equal(h, rev.FrozenHash) {
		t.Fatalf("after catalog change: ReplayHash(2) = %x, want revision hash %x", h, rev.FrozenHash)
	}
	// A later revision cut after the change also hashes the recorded set,
	// so it chains on and replays.
	rev3 := amendWith(t, admin, app, tenant, p, ctrlID)
	if h := replay(t, store, ctx, p.ID, 3); !bytes.Equal(h, rev3.FrozenHash) {
		t.Fatalf("ReplayHash(3) = %x, want %x", h, rev3.FrozenHash)
	}
}

func TestFrozenControls_MissingSnapshotFailsClosed(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant, p, _ := frozenPeriod(t, admin, app)
	removeSnapshot(t, admin, p.ID)

	store := period.NewStore(app)
	ctx := dbtest.WithTenantCtx(t, tenant)
	if _, err := store.ReplayHash(ctx, p.ID, 1); !errors.Is(err, period.ErrNoControlSnapshot) {
		t.Fatalf("ReplayHash with no snapshot: err = %v, want ErrNoControlSnapshot", err)
	}
}

// Upgrade, undrifted: a period frozen before the migration whose catalog has
// not changed since. The backfill reproduces the freeze's set, replay
// matches, and a later catalog change no longer breaks it.
func TestFrozenControls_BackfillUndrifted(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant, p, _ := frozenPeriod(t, admin, app)
	removeSnapshot(t, admin, p.ID)
	runBackfill(t, admin)
	if src := snapshotSource(t, admin, p.ID); src != "migration_backfill" {
		t.Fatalf("snapshot source = %q, want migration_backfill", src)
	}

	store := period.NewStore(app)
	ctx := dbtest.WithTenantCtx(t, tenant)
	if h := replay(t, store, ctx, p.ID, 1); !bytes.Equal(h, p.FrozenHash) {
		t.Fatalf("after backfill: ReplayHash(1) = %x, want %x", h, p.FrozenHash)
	}
	seedOtherControl(t, admin, tenant)
	if h := replay(t, store, ctx, p.ID, 1); !bytes.Equal(h, p.FrozenHash) {
		t.Fatalf("after backfill and a new control: ReplayHash(1) = %x, want %x", h, p.FrozenHash)
	}
	// Idempotent: a second run leaves the row alone.
	runBackfill(t, admin)
}

// Upgrade, drifted: the catalog changed between the freeze and the
// migration. Replay already mismatched before the upgrade and still does;
// the backfill does not hide it, and its source says it is not freeze-time.
func TestFrozenControls_BackfillDrifted(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant, p, _ := frozenPeriod(t, admin, app)
	removeSnapshot(t, admin, p.ID)
	seedOtherControl(t, admin, tenant)
	runBackfill(t, admin)
	if src := snapshotSource(t, admin, p.ID); src != "migration_backfill" {
		t.Fatalf("snapshot source = %q, want migration_backfill", src)
	}

	store := period.NewStore(app)
	ctx := dbtest.WithTenantCtx(t, tenant)
	if h := replay(t, store, ctx, p.ID, 1); bytes.Equal(h, p.FrozenHash) {
		t.Fatalf("drifted backfill: ReplayHash(1) matched frozen_hash %x; want a mismatch", p.FrozenHash)
	}
}

func TestFrozenControls_WriteOnce(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant, p, _ := frozenPeriod(t, admin, app)

	// atlas_app holds no UPDATE or DELETE grant.
	for _, sql := range []string{
		`UPDATE audit_period_frozen_controls SET control_ids = '{}' WHERE audit_period_id = $1`,
		`DELETE FROM audit_period_frozen_controls WHERE audit_period_id = $1`,
	} {
		_, err := execAsApp(t, app, tenant, sql, p.ID)
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "42501" {
			t.Fatalf("app %q: want insufficient_privilege (42501), got %v", sql, err)
		}
	}
	// A second snapshot for the same period is refused by the key.
	_, err := execAsApp(t, app, tenant,
		`INSERT INTO audit_period_frozen_controls (tenant_id, audit_period_id, control_ids, source)
		 VALUES ($1, $2, '{}', 'freeze')`, tenant, p.ID)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
		t.Fatalf("second snapshot: want unique_violation (23505), got %v", err)
	}

	// The trigger refuses the owner too, while the period exists.
	ctx := context.Background()
	_, err = admin.Exec(ctx, `UPDATE audit_period_frozen_controls SET control_ids = '{}' WHERE audit_period_id = $1`, p.ID)
	wantCheckViolation(t, "owner update", err)
	_, err = admin.Exec(ctx, `DELETE FROM audit_period_frozen_controls WHERE audit_period_id = $1`, p.ID)
	wantCheckViolation(t, "owner delete", err)

	// Deleting the period cascades to its snapshot.
	if _, err := admin.Exec(ctx, `DELETE FROM audit_period_audit_log WHERE audit_period_id = $1`, p.ID); err != nil {
		t.Fatalf("delete period log: %v", err)
	}
	if _, err := admin.Exec(ctx, `UPDATE populations SET audit_period_id = NULL WHERE audit_period_id = $1`, p.ID); err != nil {
		t.Fatalf("detach populations: %v", err)
	}
	if _, err := admin.Exec(ctx, `DELETE FROM audit_periods WHERE id = $1`, p.ID); err != nil {
		t.Fatalf("delete period: %v", err)
	}
	var n int
	if err := admin.QueryRow(ctx, `SELECT count(*) FROM audit_period_frozen_controls WHERE audit_period_id = $1`, p.ID).Scan(&n); err != nil || n != 0 {
		t.Fatalf("snapshot after period delete: n=%d err=%v, want 0", n, err)
	}
}
