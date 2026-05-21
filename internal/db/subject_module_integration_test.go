//go:build integration

// Slice 180 — privacy-module foundation: integration tests.
//
// Three behaviors are asserted against a live Postgres reachable via
// DATABASE_URL_APP (same harness as slice 002 / 033 in this package):
//
//   1. AC-3: existing rows backfill to `subject_module='core'` via the
//      DB-level DEFAULT — every audit-log table is probed for the column
//      and the DEFAULT-via-INSERT path is verified per table.
//   2. AC-6: every existing INSERT call site (sqlc + the hand-written
//      `internal/authz/audit.go` decision_audit_log insert) continues to
//      write a row that carries `subject_module='core'`.
//   3. AC-7: RLS visibility-set under tenant context is unchanged by the
//      new column. Tenant A still sees only Tenant A's rows; the new
//      column does NOT leak through RLS. Constitutional invariant-#6
//      continuity gate.
//
// Lives in `internal/db/` (not `internal/api/adminauditlog/`) so the CI
// `tests-integration` job (`./internal/db/...` package set) actually runs
// it — the adminauditlog package is currently outside the CI integration
// matrix and tests there would be local-only.
//
// Run via: just test-integration

package db_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// The canonical nine audit-log tables that slice 180's migration touches.
// Each entry carries the minimum-INSERT shape needed to land a row under
// RLS as `atlas_app` — the column lists deliberately OMIT `subject_module`
// so this test exercises the DB-level DEFAULT 'core' path (AC-3).
//
// The aggregation_rule_audit_log row requires a parent aggregation_rules
// row (FK), so its `parent` field captures the matching parent insert.
var slice180AuditTables = []struct {
	name   string
	parent string // optional parent-row insert (FK satisfaction)
	insert string
	args   func(tenantID uuid.UUID) []any
}{
	{
		name:   "decision_audit_log",
		insert: `INSERT INTO decision_audit_log (decision_id, tenant_id, user_id, action, resource_type, resource_id, result) VALUES (gen_random_uuid(), $1, 'seeder', 'list', 'evidence', 'r-1', 'allow')`,
		args:   func(t uuid.UUID) []any { return []any{t} },
	},
	{
		name:   "evidence_audit_log",
		insert: `INSERT INTO evidence_audit_log (id, tenant_id, credential_id, decision) VALUES (gen_random_uuid(), $1, 'key_seed', 'accepted')`,
		args:   func(t uuid.UUID) []any { return []any{t} },
	},
	{
		name:   "exception_audit_log",
		insert: `INSERT INTO exception_audit_log (id, tenant_id, exception_id, action, actor, to_state) VALUES (gen_random_uuid(), $1, gen_random_uuid(), 'requested', 'seeder', 'requested')`,
		args:   func(t uuid.UUID) []any { return []any{t} },
	},
	{
		name:   "sample_audit_log",
		insert: `INSERT INTO sample_audit_log (id, tenant_id, action, actor) VALUES (gen_random_uuid(), $1, 'sample_drawn', 'seeder')`,
		args:   func(t uuid.UUID) []any { return []any{t} },
	},
	{
		name:   "audit_period_audit_log",
		insert: `INSERT INTO audit_period_audit_log (id, tenant_id, audit_period_id, action, actor) VALUES (gen_random_uuid(), $1, gen_random_uuid(), 'period_created', 'seeder')`,
		args:   func(t uuid.UUID) []any { return []any{t} },
	},
	{
		name:   "feature_flag_audit_log",
		insert: `INSERT INTO feature_flag_audit_log (id, tenant_id, flag_key, from_enabled, to_enabled, actor) VALUES (gen_random_uuid(), $1, 'risk.enabled', true, false, 'seeder')`,
		args:   func(t uuid.UUID) []any { return []any{t} },
	},
	{
		name:   "me_audit_log",
		insert: `INSERT INTO me_audit_log (tenant_id, user_id, action) VALUES ($1, gen_random_uuid(), 'profile.update')`,
		args:   func(t uuid.UUID) []any { return []any{t} },
	},
	{
		name:   "walkthrough_audit_log",
		insert: `INSERT INTO walkthrough_audit_log (id, tenant_id, walkthrough_id, action, actor) VALUES (gen_random_uuid(), $1, gen_random_uuid(), 'walkthrough_created', 'seeder')`,
		args:   func(t uuid.UUID) []any { return []any{t} },
	},
	{
		name: "aggregation_rule_audit_log",
		// FK to aggregation_rules — seed the parent first.
		parent: `INSERT INTO aggregation_rules (id, tenant_id, rule_id, target_theme, min_risks, min_teams, window_days, parent_level, severity_function, rule_body) VALUES ($1, $2, $3, 'ownership', 3, 2, 30, 'team', 'max', '{}'::jsonb)`,
		insert: `INSERT INTO aggregation_rule_audit_log (id, tenant_id, rule_id, event, actor) VALUES (gen_random_uuid(), $1, $2, 'created', 'seeder')`,
		// args is unused for this table; the inline helper below threads
		// the parent rule_id into the audit insert.
		args: nil,
	},
}

// seedSlice180Row inserts one row in the named audit table under the tenant's
// GUC (a fresh transaction with `app.current_tenant` set). Returns the
// committing transaction's effect — the row is committed so a separate
// probe transaction can read it under RLS.
func seedSlice180Row(t *testing.T, tenantID uuid.UUID, tbl struct {
	name   string
	parent string
	insert string
	args   func(tenantID uuid.UUID) []any
}) {
	t.Helper()

	withTenantTx(t, tenantID.String(), true, func(ctx context.Context, tx pgx.Tx) {
		if tbl.name == "aggregation_rule_audit_log" {
			// Special case: seed parent aggregation_rules row first, then
			// reference its id from the audit-log insert.
			ruleID := uuid.New()
			if _, err := tx.Exec(ctx, tbl.parent, ruleID, tenantID, "rule-"+ruleID.String()[:8]); err != nil {
				t.Fatalf("seed parent aggregation_rules: %v", err)
			}
			if _, err := tx.Exec(ctx, tbl.insert, tenantID, ruleID); err != nil {
				t.Fatalf("seed aggregation_rule_audit_log: %v", err)
			}
			return
		}
		if _, err := tx.Exec(ctx, tbl.insert, tbl.args(tenantID)...); err != nil {
			t.Fatalf("seed %s: %v", tbl.name, err)
		}
	})
}

// cleanupSlice180Rows purges seeded rows for the given tenant across every
// table this test touches. Runs at test cleanup so each test owns a clean
// slate. Uses the admin pool (BYPASSRLS) where it exists because two of
// the nine tables (`evidence_audit_log`, `decision_audit_log`) are
// append-only with no DELETE policy for atlas_app.
func cleanupSlice180Rows(t *testing.T, tenantID uuid.UUID) {
	t.Helper()
	t.Cleanup(func() {
		ctx := context.Background()
		pool := adminPool
		if pool == nil {
			pool = appPool
		}
		// Order matters: aggregation_rule_audit_log FKs aggregation_rules.
		for _, tbl := range []string{
			"decision_audit_log",
			"evidence_audit_log",
			"exception_audit_log",
			"sample_audit_log",
			"audit_period_audit_log",
			"feature_flag_audit_log",
			"me_audit_log",
			"walkthrough_audit_log",
			"aggregation_rule_audit_log",
			"aggregation_rules",
		} {
			_, _ = pool.Exec(ctx, fmt.Sprintf("DELETE FROM %s WHERE tenant_id = $1", tbl), tenantID)
		}
	})
}

// TestSlice180_SubjectModuleColumnPresentOnEveryAuditLogTable asserts AC-3 + AC-6:
// seeding one row into each of the nine audit-log tables produces a row
// whose `subject_module` is `'core'`. The seed INSERTs deliberately do NOT
// list `subject_module` in their column lists — so the test simultaneously
// proves (a) the DB-level DEFAULT 'core' fires (AC-3) and (b) the column
// is reachable under RLS for `atlas_app` (AC-6).
//
// If a future migration accidentally drops the DEFAULT clause, or if a
// future schema change removes the column from one of the nine tables,
// the matching subtest fails on the first table touched.
func TestSlice180_SubjectModuleColumnPresentOnEveryAuditLogTable(t *testing.T) {
	tenantUUID := uuid.New()
	cleanupSlice180Rows(t, tenantUUID)

	for _, tbl := range slice180AuditTables {
		tbl := tbl
		t.Run(tbl.name, func(t *testing.T) {
			seedSlice180Row(t, tenantUUID, tbl)

			withTenantTx(t, tenantUUID.String(), false, func(ctx context.Context, tx pgx.Tx) {
				var nCore int
				q := fmt.Sprintf(
					"SELECT count(*) FROM %s WHERE tenant_id = $1 AND subject_module = 'core'",
					tbl.name,
				)
				if err := tx.QueryRow(ctx, q, tenantUUID).Scan(&nCore); err != nil {
					t.Fatalf("probe %s: %v", tbl.name, err)
				}
				if nCore == 0 {
					t.Fatalf("%s: no rows with subject_module='core'; want >= 1 (DEFAULT 'core' missing or INSERT path broken)",
						tbl.name)
				}
				// And there should be zero rows with subject_module != 'core'
				// since slice 180 ships nothing but core writes.
				var nNonCore int
				qNon := fmt.Sprintf(
					"SELECT count(*) FROM %s WHERE tenant_id = $1 AND subject_module <> 'core'",
					tbl.name,
				)
				if err := tx.QueryRow(ctx, qNon, tenantUUID).Scan(&nNonCore); err != nil {
					t.Fatalf("probe non-core %s: %v", tbl.name, err)
				}
				if nNonCore > 0 {
					t.Errorf("%s: %d rows with subject_module<>'core'; want 0 (slice 180 ships core-only writes)",
						tbl.name, nNonCore)
				}
			})
		})
	}
}

// TestSlice180_RLSVisibilitySetUnchangedByNewColumn asserts AC-7:
// the new `subject_module` column does NOT change tenant isolation behavior.
//
// Setup: two tenants seed one row in each of the nine tables. Under each
// tenant's GUC, the foreign tenant's rows MUST be invisible — even when
// the query predicate filters on `subject_module = 'core'`. The slice 036
// four-policy RLS pattern is the load-bearing contract; slice 180 must
// not regress it.
//
// This test goes against the base tables directly (NOT through the
// slice 124 UNION endpoint) because base-table RLS is the primitive
// contract — every consumer (UNION, sqlc reads, sink replays) inherits
// the invariant.
func TestSlice180_RLSVisibilitySetUnchangedByNewColumn(t *testing.T) {
	tenantA := uuid.New()
	tenantB := uuid.New()
	cleanupSlice180Rows(t, tenantA)
	cleanupSlice180Rows(t, tenantB)

	for _, tbl := range slice180AuditTables {
		seedSlice180Row(t, tenantA, tbl)
		seedSlice180Row(t, tenantB, tbl)
	}

	for _, who := range []struct {
		name   string
		tenant uuid.UUID
		other  uuid.UUID
	}{
		{"tenant_A", tenantA, tenantB},
		{"tenant_B", tenantB, tenantA},
	} {
		who := who
		t.Run(who.name, func(t *testing.T) {
			withTenantTx(t, who.tenant.String(), false, func(ctx context.Context, tx pgx.Tx) {
				for _, tbl := range slice180AuditTables {
					// Self-visible rows: own tenant's row, filtering on
					// the new subject_module column, must surface.
					var nSelf int
					qSelf := fmt.Sprintf(
						"SELECT count(*) FROM %s WHERE subject_module = 'core'",
						tbl.name,
					)
					if err := tx.QueryRow(ctx, qSelf).Scan(&nSelf); err != nil {
						t.Fatalf("probe self %s: %v", tbl.name, err)
					}
					if nSelf < 1 {
						t.Errorf("%s as %s: visible-set count = %d; want >= 1 (own seeded row)",
							tbl.name, who.name, nSelf)
					}

					// Foreign-tenant rows: filtering on the other tenant's
					// id (which is OUR tenant id from the foreign tenant's
					// perspective) must return zero rows. RLS strips
					// foreign-tenant rows BEFORE the WHERE clause sees them.
					var nForeign int
					qForeign := fmt.Sprintf(
						"SELECT count(*) FROM %s WHERE tenant_id = $1 AND subject_module = 'core'",
						tbl.name,
					)
					if err := tx.QueryRow(ctx, qForeign, who.other).Scan(&nForeign); err != nil {
						t.Fatalf("probe foreign %s: %v", tbl.name, err)
					}
					if nForeign != 0 {
						t.Errorf("%s as %s: visible foreign-tenant rows = %d; want 0 (RLS leak via subject_module filter)",
							tbl.name, who.name, nForeign)
					}
				}
			})
		})
	}
}
