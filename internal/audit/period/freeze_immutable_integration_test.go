//go:build integration

// #3050: the original freeze (frozen_at, frozen_hash, frozen_by, and the row
// fields the hash covers) is immutable in the database, populations.frozen_at
// is write-once, and freeze revisions form an unbroken hash chain back to the
// original. Migration 20261307000000_audit_period_freeze_immutable.

package period_test

import (
	"context"
	"errors"
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

// insertRevisionSQL appends a freeze revision directly, bypassing
// period.AmendInTx, so a test can offer the chain trigger a bad link.
// $1 id, $2 tenant, $3 period, $4 revision, $5 previous_hash,
// $6 import batch, $7 one evidence record.
const insertRevisionSQL = `INSERT INTO audit_period_freeze_revisions (
    id, tenant_id, audit_period_id, revision, frozen_hash, previous_hash, frozen_at,
    reason, justification, actor, import_batch_id, evidence_record_ids)
  VALUES ($1, $2, $3, $4, decode('ab', 'hex'), $5, now(),
    'amended_by_import', 'chain test', 'key_test_3050', $6, ARRAY[$7::uuid])`

// reopenFrozenPeriod resets a frozen period to open as the table owner, the
// recovery path the AC-7 tests use (docs/runbooks/audit-period-reopen.md).
// The freeze-immutability triggers refuse it for every role, so the owner
// disables them for its own transaction only. The recorded control set goes
// with the freeze (#3066), so the next Freeze records its own.
func reopenFrozenPeriod(t *testing.T, admin *pgxpool.Pool, id uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	err := pgx.BeginTxFunc(ctx, admin, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `ALTER TABLE audit_periods DISABLE TRIGGER audit_periods_freeze_immutable_trg`); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `ALTER TABLE audit_period_frozen_controls DISABLE TRIGGER audit_period_frozen_controls_immutable_trg`); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM audit_period_frozen_controls WHERE audit_period_id = $1`, id); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `ALTER TABLE audit_period_frozen_controls ENABLE TRIGGER audit_period_frozen_controls_immutable_trg`); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx,
			`UPDATE audit_periods
			 SET status = 'open', frozen_at = NULL, frozen_hash = NULL, frozen_by = NULL, updated_at = now()
			 WHERE id = $1`, id); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `ALTER TABLE audit_periods ENABLE TRIGGER audit_periods_freeze_immutable_trg`)
		return err
	})
	if err != nil {
		t.Fatalf("owner reopen: %v", err)
	}
}

// execAsApp runs sql as atlas_app inside one transaction carrying tenant's
// GUC, and returns the rows it affected.
func execAsApp(t *testing.T, app *pgxpool.Pool, tenant, sql string, args ...any) (int64, error) {
	t.Helper()
	ctx, err := tenancy.WithTenant(context.Background(), tenant)
	if err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
	var n int64
	err = pgx.BeginTxFunc(ctx, app, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := tenancy.ApplyTenant(ctx, tx); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, sql, args...)
		n = tag.RowsAffected()
		return err
	})
	return n, err
}

func wantCheckViolation(t *testing.T, what string, err error) {
	t.Helper()
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23514" {
		t.Fatalf("%s: want check_violation (23514), got %v", what, err)
	}
}

// cleanupRevisions deletes tenant's freeze revisions before freshTenant's
// cleanup deletes its periods (t.Cleanup runs last-registered first), since
// the revision FK is ON DELETE RESTRICT.
func cleanupRevisions(t *testing.T, admin *pgxpool.Pool, tenant string) {
	t.Helper()
	t.Cleanup(func() {
		if _, err := admin.Exec(context.Background(),
			`DELETE FROM audit_period_freeze_revisions WHERE tenant_id = $1`, tenant); err != nil {
			t.Logf("cleanup freeze revisions: %v", err)
		}
	})
}

// frozenPeriod creates a period in a fresh tenant and freezes it through
// period.Store as atlas_app. It returns the frozen period and one evidence
// record inside its horizon.
func frozenPeriod(t *testing.T, admin, app *pgxpool.Pool) (string, period.Period, uuid.UUID) {
	t.Helper()
	tenant := freshTenant(t, admin)
	cleanupRevisions(t, admin, tenant)
	fwID := seedFrameworkVersion(t, admin)
	ctrlID := seedControl(t, admin, tenant)
	windowStart := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	records := seedEvidence(t, admin, tenant, ctrlID, 3, windowStart)

	store := period.NewStore(app)
	ctx := dbtest.WithTenantCtx(t, tenant)
	p, err := store.Create(ctx, period.CreateInput{
		Name:               "#3050 period",
		FrameworkVersionID: fwID,
		PeriodStart:        windowStart,
		PeriodEnd:          windowStart.Add(72 * time.Hour),
		CreatedBy:          "key_test_3050",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// The first freeze is the one NULL-to-value transition the trigger allows.
	frozen, err := store.Freeze(ctx, p.ID, "key_test_3050", windowStart.Add(10*time.Hour))
	if err != nil {
		t.Fatalf("first Freeze: %v", err)
	}
	if frozen.Status != period.StatusFrozen || frozen.FrozenAt == nil || len(frozen.FrozenHash) == 0 {
		t.Fatalf("first Freeze did not stamp the freeze: %+v", frozen)
	}
	return tenant, frozen, records[0]
}

func TestFreezeImmutable_AppCannotChangeOriginalFreeze(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant, p, _ := frozenPeriod(t, admin, app)
	otherFW := seedFrameworkVersion(t, admin)

	// name is not a hash input and stays editable. This also shows the app
	// role can see and write this row: the refusals below are the trigger,
	// not RLS matching zero rows.
	n, err := execAsApp(t, app, tenant,
		`UPDATE audit_periods SET name = '#3050 renamed', updated_at = now() WHERE id = $1`, p.ID)
	if err != nil || n != 1 {
		t.Fatalf("rename frozen period: rows=%d err=%v, want 1 row", n, err)
	}

	for _, c := range []struct {
		what, sql string
		args      []any
	}{
		{"rewrite frozen_at", `UPDATE audit_periods SET frozen_at = frozen_at + interval '1 day' WHERE id = $1`, nil},
		{"rewrite frozen_hash", `UPDATE audit_periods SET frozen_hash = decode('00', 'hex') WHERE id = $1`, nil},
		{"rewrite frozen_by", `UPDATE audit_periods SET frozen_by = 'someone-else' WHERE id = $1`, nil},
		{"reopen", `UPDATE audit_periods
		            SET status = 'open', frozen_at = NULL, frozen_hash = NULL, frozen_by = NULL
		            WHERE id = $1`, nil},
		// The ADR 0003 hash inputs read from the row (B1).
		{"move period_start", `UPDATE audit_periods SET period_start = period_start - 1 WHERE id = $1`, nil},
		{"move period_end", `UPDATE audit_periods SET period_end = period_end + 1 WHERE id = $1`, nil},
		{"swap framework_version_id", `UPDATE audit_periods SET framework_version_id = $2 WHERE id = $1`, []any{otherFW}},
		{"change id", `UPDATE audit_periods SET id = $2 WHERE id = $1`, []any{uuid.New()}},
	} {
		_, err := execAsApp(t, app, tenant, c.sql, append([]any{p.ID}, c.args...)...)
		wantCheckViolation(t, c.what, err)
	}

	// The stored freeze is still what the first freeze wrote, and it still
	// replays.
	store := period.NewStore(app)
	ctx := dbtest.WithTenantCtx(t, tenant)
	got, err := store.Get(ctx, p.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !got.FrozenAt.Equal(*p.FrozenAt) || string(got.FrozenHash) != string(p.FrozenHash) || got.FrozenBy != p.FrozenBy ||
		!got.PeriodStart.Equal(p.PeriodStart) || !got.PeriodEnd.Equal(p.PeriodEnd) || got.FrameworkVersionID != p.FrameworkVersionID {
		t.Fatalf("frozen row changed: got %+v, want %+v", got, p)
	}
	replay, err := store.ReplayHash(ctx, p.ID, 1)
	if err != nil {
		t.Fatalf("ReplayHash: %v", err)
	}
	if string(replay) != string(p.FrozenHash) {
		t.Fatalf("ReplayHash(1) = %x, want frozen_hash %x", replay, p.FrozenHash)
	}
}

func TestFreezeImmutable_PopulationFrozenAtIsWriteOnce(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	ctrlID := seedControl(t, admin, tenant)
	popID := uuid.New()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO populations (id, tenant_id, control_id, scope_predicate, time_window_start, time_window_end, created_by, row_count)
		VALUES ($1, $2, $3, '{}'::jsonb, '2026-04-01', '2026-04-04', 'key_test_3050', 0)
	`, popID, tenant, ctrlID); err != nil {
		t.Fatalf("seed population: %v", err)
	}

	// NULL to a value is the stamp SetPopulationFrozenAt makes.
	if n, err := execAsApp(t, app, tenant,
		`UPDATE populations SET frozen_at = '2026-04-02T00:00:00Z' WHERE id = $1`, popID); err != nil || n != 1 {
		t.Fatalf("first stamp of frozen_at: rows=%d err=%v, want 1 row", n, err)
	}
	for _, c := range []struct{ what, sql string }{
		{"rewrite population frozen_at", `UPDATE populations SET frozen_at = frozen_at + interval '1 day' WHERE id = $1`},
		{"reset population frozen_at", `UPDATE populations SET frozen_at = NULL WHERE id = $1`},
	} {
		_, err := execAsApp(t, app, tenant, c.sql, popID)
		wantCheckViolation(t, c.what, err)
	}
	// Other columns of a frozen population still update.
	if n, err := execAsApp(t, app, tenant,
		`UPDATE populations SET row_count = 1 WHERE id = $1`, popID); err != nil || n != 1 {
		t.Fatalf("update row_count of frozen population: rows=%d err=%v, want 1 row", n, err)
	}
}

func TestFreezeRevisionChain(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant, p, record := frozenPeriod(t, admin, app)

	// Adding a revision through the importer's path still succeeds.
	amendPeriod(t, app, tenant, p.ID, record)
	revs, err := period.NewStore(app).Revisions(dbtest.WithTenantCtx(t, tenant), p.ID)
	if err != nil {
		t.Fatalf("Revisions: %v", err)
	}
	if len(revs) != 2 || revs[1].Revision != 2 {
		t.Fatalf("want revisions 1 and 2, got %+v", revs)
	}
	rev2Hash := revs[1].FrozenHash

	for _, c := range []struct {
		what     string
		revision int
		previous []byte
	}{
		{"gap: revision 4 after 2", 4, rev2Hash},
		{"repeat: revision 2 again", 2, p.FrozenHash},
		{"broken link: previous_hash is not revision 2's hash", 3, []byte{0xff}},
	} {
		_, err := execAsApp(t, app, tenant, insertRevisionSQL,
			uuid.New(), tenant, p.ID, c.revision, c.previous, uuid.New(), record)
		wantCheckViolation(t, c.what, err)
	}

	// The correct next link is accepted.
	if _, err := execAsApp(t, app, tenant, insertRevisionSQL,
		uuid.New(), tenant, p.ID, 3, rev2Hash, uuid.New(), record); err != nil {
		t.Fatalf("revision 3 linked to revision 2: %v", err)
	}
}

func TestFreezeRevisionChain_RefusesOpenPeriod(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	cleanupRevisions(t, admin, tenant)
	fwID := seedFrameworkVersion(t, admin)
	p, err := period.NewStore(app).Create(dbtest.WithTenantCtx(t, tenant), period.CreateInput{
		Name:               "#3050 open period",
		FrameworkVersionID: fwID,
		PeriodStart:        time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
		PeriodEnd:          time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC),
		CreatedBy:          "key_test_3050",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	_, err = execAsApp(t, app, tenant, insertRevisionSQL,
		uuid.New(), tenant, p.ID, 2, []byte{0xcd}, uuid.New(), uuid.New())
	wantCheckViolation(t, "revision on an open period", err)
}

// amendPeriod cuts the next freeze revision of periodID, adding recordID,
// through period.AmendInTx in its own committed transaction as atlas_app.
func amendPeriod(t *testing.T, app *pgxpool.Pool, tenant string, periodID, recordID uuid.UUID) {
	t.Helper()
	ctx, err := tenancy.WithTenant(context.Background(), tenant)
	if err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
	err = pgx.BeginTxFunc(ctx, app, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := tenancy.ApplyTenant(ctx, tx); err != nil {
			return err
		}
		q := dbx.New(tx)
		p, err := q.GetAuditPeriodByID(ctx, dbx.GetAuditPeriodByIDParams{
			TenantID: pgtype.UUID{Bytes: uuid.MustParse(tenant), Valid: true},
			ID:       pgtype.UUID{Bytes: periodID, Valid: true},
		})
		if err != nil {
			return err
		}
		_, err = period.AmendInTx(ctx, q, p, period.AmendInput{
			ImportBatchID: uuid.New(), Actor: "key_test_3050", Reason: "#3050 chain test",
			EvidenceRecordIDs: []uuid.UUID{recordID},
		})
		return err
	})
	if err != nil {
		t.Fatalf("amend: %v", err)
	}
}
