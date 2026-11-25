//go:build integration

// #3094: TRUNCATE cannot remove a frozen audit's rows, directly or through
// a CASCADE from far away. Migration
// 20261316000000_audit_period_frozen_no_truncate.
//
// Every TRUNCATE here runs in a transaction that is rolled back, so the
// shared database is never truncated, even when the guard lets one through.

package period_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/dbtest"
)

const noTruncateTrigger = "audit_period_frozen_no_truncate_trg"

// guardedTables are the tables the TRUNCATE guard sits on.
var guardedTables = []string{
	"audit_periods", "populations", "audit_period_freeze_revisions", "audit_period_frozen_controls",
}

// inRolledBackTx runs fn as the owner in a transaction that is always
// rolled back.
func inRolledBackTx(t *testing.T, admin *pgxpool.Pool, fn func(tx pgx.Tx)) {
	t.Helper()
	ctx := context.Background()
	tx, err := admin.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	fn(tx)
}

// attachFrozenPopulation adds a population with frozen_at set to the
// frozen period p, against one of tenant's controls.
func attachFrozenPopulation(t *testing.T, admin *pgxpool.Pool, tenant string, periodID uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	var controlID uuid.UUID
	if err := admin.QueryRow(ctx, `SELECT id FROM controls WHERE tenant_id = $1 LIMIT 1`, tenant).Scan(&controlID); err != nil {
		t.Fatalf("find control: %v", err)
	}
	start := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	if _, err := admin.Exec(ctx, `
		INSERT INTO populations (id, tenant_id, control_id, scope_predicate, time_window_start, time_window_end,
		                         frozen_at, created_by, audit_period_id)
		VALUES ($1, $2, $3, '{}'::jsonb, $4, $5, now(), 'key_test_3094', $6)`,
		uuid.New(), tenant, controlID, start, start.Add(72*time.Hour), periodID); err != nil {
		t.Fatalf("attach population: %v", err)
	}
}

func TestFrozenNoTruncate_TriggerOnEveryGuardedTable(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	for _, table := range guardedTables {
		var n int
		if err := admin.QueryRow(context.Background(), `
			SELECT count(*) FROM pg_trigger
			 WHERE tgrelid = $1::regclass AND tgname = $2 AND tgenabled = 'O'
			   AND (tgtype & 1) = 0   -- STATEMENT
			   AND (tgtype & 2) <> 0  -- BEFORE
			   AND (tgtype & 32) <> 0 -- TRUNCATE`,
			table, noTruncateTrigger).Scan(&n); err != nil {
			t.Fatalf("read %s triggers: %v", table, err)
		}
		if n != 1 {
			t.Errorf("%s has %d enabled BEFORE TRUNCATE statement triggers named %s, want 1", table, n, noTruncateTrigger)
		}
	}
}

// With a frozen period in the database, a TRUNCATE that reaches any guarded
// table is refused, whether it names the table or reaches it by CASCADE.
func TestFrozenNoTruncate_Refused(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant, p, _ := frozenPeriod(t, admin, app)
	attachFrozenPopulation(t, admin, tenant, p.ID)
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), `DELETE FROM populations WHERE tenant_id = $1`, tenant)
	})

	for _, stmt := range []string{
		`TRUNCATE audit_periods CASCADE`,
		`TRUNCATE populations CASCADE`,
		`TRUNCATE audit_period_frozen_controls`,
		// From far away: the paths the integration helpers use.
		`TRUNCATE controls RESTART IDENTITY CASCADE`,
		`TRUNCATE framework_versions CASCADE`,
		`TRUNCATE controls, scf_anchors, fw_to_scf_edges, framework_requirements, framework_versions, frameworks CASCADE`,
	} {
		t.Run(stmt, func(t *testing.T) {
			inRolledBackTx(t, admin, func(tx pgx.Tx) {
				_, err := tx.Exec(context.Background(), stmt)
				wantCheckViolationMsg(t, stmt, err, "TRUNCATE is refused")
			})
		})
	}
	if periods, snapshots := periodRows(t, admin, p.ID); periods != 1 || snapshots != 1 {
		t.Fatalf("after refused truncates: periods=%d snapshots=%d, want 1 and 1", periods, snapshots)
	}
}

// The owner path: with the guards disabled inside its own transaction the
// owner can truncate. Once no frozen audit's rows are left the guards let a
// TRUNCATE through, even with an open period in the table; a population
// with frozen_at set still refuses it.
func TestFrozenNoTruncate_OwnerPathAndOpenRowsOnly(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	_, _, _ = frozenPeriod(t, admin, app)
	fwID := seedFrameworkVersion(t, admin)
	tenant := freshTenant(t, admin)
	controlID := seedControl(t, admin, tenant)
	ctx := context.Background()

	inRolledBackTx(t, admin, func(tx pgx.Tx) {
		exec := func(sql string, args ...any) {
			t.Helper()
			if _, err := tx.Exec(ctx, sql, args...); err != nil {
				t.Fatalf("%s: %v", sql, err)
			}
		}
		for _, table := range guardedTables {
			exec(`ALTER TABLE ` + table + ` DISABLE TRIGGER ` + noTruncateTrigger)
		}
		exec(`TRUNCATE audit_periods CASCADE`)
		for _, table := range guardedTables {
			exec(`ALTER TABLE ` + table + ` ENABLE TRIGGER ` + noTruncateTrigger)
		}

		start := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
		periodID := uuid.New()
		exec(`INSERT INTO audit_periods (id, tenant_id, name, framework_version_id, period_start, period_end, status, created_by)
		      VALUES ($1, $2, '#3094 open period', $3, $4, $5, 'open', 'key_test_3094')`,
			periodID, tenant, fwID, start, start.Add(72*time.Hour))
		exec(`INSERT INTO populations (id, tenant_id, control_id, scope_predicate, time_window_start, time_window_end,
		                               created_by, audit_period_id)
		      VALUES ($1, $2, $3, '{}'::jsonb, $4, $5, 'key_test_3094', $6)`,
			uuid.New(), tenant, controlID, start, start.Add(72*time.Hour), periodID)
		// Run the deferred commit check now: TRUNCATE refuses a table with
		// pending trigger events (55006).
		exec(`SET CONSTRAINTS ALL IMMEDIATE`)

		sp, err := tx.Begin(ctx)
		if err != nil {
			t.Fatalf("savepoint: %v", err)
		}
		if _, err := sp.Exec(ctx, `TRUNCATE audit_periods CASCADE`); err != nil {
			t.Fatalf("TRUNCATE with only an open period and its unfrozen population: %v", err)
		}
		_ = sp.Rollback(ctx)

		exec(`UPDATE populations SET frozen_at = now() WHERE audit_period_id = $1`, periodID)
		_, err = tx.Exec(ctx, `TRUNCATE populations CASCADE`)
		wantCheckViolationMsg(t, "TRUNCATE of a population with frozen_at set", err, "populations holds a frozen audit's rows")
	})
}

// PUBLIC holds no EXECUTE on the SECURITY DEFINER guard; the owner does.
func TestFrozenNoTruncate_FunctionNotPublic(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	for role, want := range map[string]bool{"atlas_app": false, "atlas_migrate": true} {
		var got bool
		if err := admin.QueryRow(context.Background(),
			`SELECT has_function_privilege($1, 'audit_period_frozen_no_truncate()', 'EXECUTE')`, role,
		).Scan(&got); err != nil {
			t.Fatalf("has_function_privilege(%s): %v", role, err)
		}
		if got != want {
			t.Errorf("%s EXECUTE on audit_period_frozen_no_truncate() = %v, want %v", role, got, want)
		}
	}
}

// #3096: both SECURITY DEFINER guards carry row_security = off, so if their
// owner ever lacks BYPASSRLS a query inside them raises instead of seeing
// only the session tenant's rows (or none) under FORCE RLS and passing.
func TestFrozenGuards_RowSecurityOff(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	for _, fn := range []string{"audit_periods_frozen_has_controls()", "audit_period_frozen_no_truncate()"} {
		var config []string
		var definer bool
		if err := admin.QueryRow(context.Background(),
			`SELECT coalesce(proconfig, '{}'), prosecdef FROM pg_proc WHERE oid = $1::regprocedure`, fn,
		).Scan(&config, &definer); err != nil {
			t.Fatalf("read %s: %v", fn, err)
		}
		if !definer {
			t.Errorf("%s is not SECURITY DEFINER", fn)
		}
		if !slices.Contains(config, "row_security=off") {
			t.Errorf("%s proconfig = %v, want row_security=off", fn, config)
		}
	}
}

// #3096: TRUNCATE audit_period_freeze_revisions runs the revisions branch
// of the guard. Whether a revision row exists depends on what else is in the
// shared database, so either outcome is accepted: success (no revisions) or
// the guard's refusal. Any other error fails, so a broken branch cannot hide.
func TestFrozenNoTruncate_RevisionsBranch(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	const stmt = `TRUNCATE audit_period_freeze_revisions`
	inRolledBackTx(t, admin, func(tx pgx.Tx) {
		_, err := tx.Exec(context.Background(), stmt)
		if err == nil {
			return
		}
		wantCheckViolationMsg(t, stmt, err, "audit_period_freeze_revisions holds a frozen audit's rows")
	})
}
