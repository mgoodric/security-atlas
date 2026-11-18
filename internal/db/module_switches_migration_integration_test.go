//go:build integration

// #2352 — migration 20261130000000 (#2171's D5) against real rows. CI
// otherwise runs it on empty feature_flags, where its UPDATEs match
// nothing and prove nothing. Seeds two tenants with the four module
// switches nothing read before #2171 stored off, runs the migration as
// atlas_migrate, and asserts each switch is back on with one audit row.
//
// Lives in internal/db/ so the local `just test-integration` package set
// runs it as well as CI.

package db_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const moduleSwitchesUp = "../../migrations/sql/20261130000000_module_switches.sql"

// d5Keys are the switches migration 20261130000000 resets from off to on.
var d5Keys = map[string]string{
	"risk.enabled":       "risk",
	"vendor.enabled":     "vendor",
	"policy.enabled":     "policy",
	"exceptions.enabled": "controls",
}

func TestModuleSwitchesMigration_ResetsStoredOffSwitchesInEveryTenant(t *testing.T) {
	if adminPool == nil {
		t.Skip("DATABASE_URL not set; the migration test needs the migrate role")
	}
	up := readMigration(t, moduleSwitchesUp)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// An atlas_migrate transaction that always rolls back, so the schema and
	// data are untouched for later tests.
	tx, err := adminPool.Begin(ctx)
	if err != nil {
		t.Fatalf("admin Begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// feature_flags is FORCE ROW LEVEL SECURITY. Under a role RLS binds,
	// the migration's UPDATEs would see no tenant's rows and reset nothing
	// without an error, so the role that runs it must bypass RLS (the
	// #2333 guard's check).
	var role string
	var canSeeAll bool
	if err := tx.QueryRow(ctx,
		`SELECT current_user, rolsuper OR rolbypassrls FROM pg_roles WHERE rolname = current_user`,
	).Scan(&role, &canSeeAll); err != nil {
		t.Fatalf("read migrating role: %v", err)
	}
	if !canSeeAll {
		t.Fatalf("migrating role %s has neither SUPERUSER nor BYPASSRLS: the migration's UPDATEs would match no tenant's rows", role)
	}

	tenants := []string{uuid.NewString(), uuid.NewString()}
	for _, tid := range tenants {
		if _, err := tx.Exec(ctx,
			`INSERT INTO tenants (id, name, is_bootstrap_tenant) VALUES ($1, $2, false)`,
			tid, "mod-2352-"+tid[:8],
		); err != nil {
			t.Fatalf("INSERT tenants: %v", err)
		}
		for key, cat := range d5Keys {
			upsertFlag(ctx, t, tx, tid, key, cat, false)
		}
		// A module switch outside the D5 set stays as stored.
		upsertFlag(ctx, t, tx, tid, "questionnaires.enabled", "questionnaire", false)
	}
	// The renamed switch keeps its stored value under the new key.
	upsertFlag(ctx, t, tx, tenants[0], "oscal.export", "integrations", true)

	if _, err := tx.Exec(ctx, up); err != nil {
		t.Fatalf("run migration 20261130000000: %v", err)
	}

	for _, tid := range tenants {
		for key := range d5Keys {
			var enabled bool
			var changedBy *string
			if err := tx.QueryRow(ctx,
				`SELECT enabled, last_changed_by FROM feature_flags WHERE tenant_id = $1 AND flag_key = $2`,
				tid, key,
			).Scan(&enabled, &changedBy); err != nil {
				t.Fatalf("read %s for tenant %s: %v", key, tid, err)
			}
			if !enabled {
				t.Errorf("tenant %s %s: still off after the migration", tid, key)
			}
			if changedBy == nil || *changedBy != "migration:20261130000000" {
				t.Errorf("tenant %s %s: last_changed_by = %v; want migration:20261130000000", tid, key, changedBy)
			}

			var rows int
			if err := tx.QueryRow(ctx, `
				SELECT count(*) FROM feature_flag_audit_log
				 WHERE tenant_id = $1 AND flag_key = $2
				   AND from_enabled = false AND to_enabled = true
				   AND actor = 'migration:20261130000000'
				   AND reason <> ''`,
				tid, key,
			).Scan(&rows); err != nil {
				t.Fatalf("count audit rows: %v", err)
			}
			if rows != 1 {
				t.Errorf("tenant %s %s: %d migration audit rows; want 1", tid, key, rows)
			}
		}

		var qOn bool
		if err := tx.QueryRow(ctx,
			`SELECT enabled FROM feature_flags WHERE tenant_id = $1 AND flag_key = 'questionnaires.enabled'`,
			tid,
		).Scan(&qOn); err != nil {
			t.Fatalf("read questionnaires.enabled: %v", err)
		}
		if qOn {
			t.Errorf("tenant %s questionnaires.enabled: turned on; the migration resets only the D5 switches", tid)
		}
	}

	var claimsOn bool
	if err := tx.QueryRow(ctx,
		`SELECT enabled FROM feature_flags WHERE tenant_id = $1 AND flag_key = 'vendor.claims'`,
		tenants[0],
	).Scan(&claimsOn); err != nil {
		t.Fatalf("read vendor.claims: %v", err)
	}
	if !claimsOn {
		t.Error("vendor.claims: the stored oscal.export value (on) did not move to the new key")
	}
}

// upsertFlag stores one switch for a tenant, overwriting any row a tenant
// seed wrote.
func upsertFlag(ctx context.Context, t *testing.T, tx pgx.Tx, tenantID, key, category string, enabled bool) {
	t.Helper()
	if _, err := tx.Exec(ctx, `
		INSERT INTO feature_flags (tenant_id, flag_key, enabled, category)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (tenant_id, flag_key) DO UPDATE SET enabled = EXCLUDED.enabled`,
		tenantID, key, enabled, category,
	); err != nil {
		t.Fatalf("store %s for tenant %s: %v", key, tenantID, err)
	}
}
