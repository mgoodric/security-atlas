//go:build integration

// #3085: hardening of the #3066 frozen-controls guards from the #3077
// re-review. Migration 20261314000000_audit_period_frozen_controls_hardening.

package period_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/audit/period"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// N-a: the deferred commit check runs as the owner, so an atlas_app session
// that freezes a period without its control set and then clears or switches
// app.current_tenant before COMMIT is still refused.
func TestFrozenControls_CommitCheckIgnoresTenantSwitch(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	other := freshTenant(t, admin)

	for _, tc := range []struct {
		name string
		guc  func() string
	}{
		{"cleared", func() string { return "" }},
		{"switched", func() string { return other }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tenant, p := openPeriod(t, admin, app)
			ctx, err := tenancy.WithTenant(context.Background(), tenant)
			if err != nil {
				t.Fatalf("WithTenant: %v", err)
			}
			err = pgx.BeginTxFunc(ctx, app, pgx.TxOptions{}, func(tx pgx.Tx) error {
				if err := tenancy.ApplyTenant(ctx, tx); err != nil {
					return err
				}
				if _, err := tx.Exec(ctx,
					`UPDATE audit_periods
					 SET status = 'frozen', frozen_at = now(), frozen_hash = sha256('tenant-switch'::bytea),
					     frozen_by = 'key_test_3085', updated_at = now()
					 WHERE id = $1`, p.ID); err != nil {
					return err
				}
				_, err := tx.Exec(ctx, `SELECT set_config($1, $2, true)`, tenancy.GUCName, tc.guc())
				return err
			})
			wantCheckViolation(t, "freeze committed after the tenant GUC was "+tc.name, err)

			got, gerr := period.NewStore(app).Get(dbtest.WithTenantCtx(t, tenant), p.ID)
			if gerr != nil || got.Status != period.StatusOpen {
				t.Fatalf("period after refused freeze: status=%v err=%v, want open", got.Status, gerr)
			}
		})
	}
}

// grantServiceAccount gives atlas_service_account the table privileges the
// insert guard needs to be reached, for this test only. In production that
// role holds neither, which is what keeps the N-b gap latent; the test grants
// them to show the guard itself refuses. Only privileges it lacked are
// granted and later revoked.
func grantServiceAccount(t *testing.T, admin *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	for _, g := range []struct{ table, priv string }{
		{"audit_periods", "SELECT"},
		{"audit_period_frozen_controls", "INSERT"},
	} {
		var has bool
		if err := admin.QueryRow(ctx,
			`SELECT has_table_privilege('atlas_service_account', $1, $2)`, g.table, g.priv).Scan(&has); err != nil {
			t.Fatalf("has_table_privilege %s %s: %v", g.priv, g.table, err)
		}
		if has {
			continue
		}
		if _, err := admin.Exec(ctx, `GRANT `+g.priv+` ON `+g.table+` TO atlas_service_account`); err != nil {
			t.Fatalf("grant %s %s: %v", g.priv, g.table, err)
		}
		t.Cleanup(func() {
			if _, err := admin.Exec(context.Background(),
				`REVOKE `+g.priv+` ON `+g.table+` FROM atlas_service_account`); err != nil {
				t.Errorf("revoke %s %s: %v", g.priv, g.table, err)
			}
		})
	}
}

// N-b: an atlas_app session that switches to atlas_service_account
// (BYPASSRLS, and a role atlas_app is a member of) still may not record a
// source other than 'freeze': the guard checks session_user too.
func TestFrozenControls_InsertGuardRefusesRoleSwitch(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	grantServiceAccount(t, admin)

	tenant, frozen, _ := frozenPeriod(t, admin, app)
	removeSnapshot(t, admin, frozen.ID)

	for _, src := range []string{"seed", "migration_backfill"} {
		_, err := execAsApp(t, app, tenant,
			`SET LOCAL ROLE atlas_service_account;
			 INSERT INTO audit_period_frozen_controls (tenant_id, audit_period_id, control_ids, source)
			 VALUES ('`+tenant+`', '`+frozen.ID.String()+`', '{}', '`+src+`')`)
		// The message, not only the SQLSTATE: the not-frozen branch of the
		// same guard also raises 23514, and would pass a code-only check.
		wantCheckViolationMsg(t, "app as atlas_service_account records "+src, err,
			"reserved for the migration and the seed")
	}
}

// N-c: atlas_app may not execute audit_period_record_catalog_controls at
// all; only atlas_migrate (and superusers) may.
func TestFrozenControls_RecordFunctionNotPublic(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant, frozen, _ := frozenPeriod(t, admin, app)
	removeSnapshot(t, admin, frozen.ID)

	_, err := execAsApp(t, app, tenant,
		`SELECT audit_period_record_catalog_controls($1, $2, 'freeze')`, tenant, frozen.ID)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "42501" {
		t.Fatalf("app executes the record function: want insufficient_privilege (42501), got %v", err)
	}

	if _, err := admin.Exec(context.Background(),
		`SELECT audit_period_record_catalog_controls($1, $2, 'seed')`, tenant, frozen.ID); err != nil {
		t.Fatalf("migrate role executes the record function: %v", err)
	}
}

// #3089: the SECURITY DEFINER commit check holds no PUBLIC EXECUTE, so
// atlas_app has none; the owner keeps it. Firing the trigger does not check
// EXECUTE, which the freeze tests above (an app-role freeze that commits)
// already exercise.
func TestFrozenControls_CommitCheckNotPublic(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	for role, want := range map[string]bool{"atlas_app": false, "atlas_migrate": true} {
		var got bool
		if err := admin.QueryRow(context.Background(),
			`SELECT has_function_privilege($1, 'audit_periods_frozen_has_controls()', 'EXECUTE')`, role,
		).Scan(&got); err != nil {
			t.Fatalf("has_function_privilege(%s): %v", role, err)
		}
		if got != want {
			t.Errorf("%s EXECUTE on audit_periods_frozen_has_controls() = %v, want %v", role, got, want)
		}
	}
}
