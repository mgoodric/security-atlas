//go:build integration

// Integration tests for slice 052 — risk hierarchy + theme taxonomy +
// Decision Log schema. Verifies:
//   - AC-9 cross-tenant RLS negative: a row inserted under tenant A is
//     invisible to tenant B on every new tenant-scoped table.
//   - AC-2/3/4/5/6 positive INSERTs work for org_units, org_themes,
//     risk_aggregations, decisions, and all four decision link tables.
//   - AC-10 default theme set is present after the seed migration and a
//     tenant-private theme of the same name does not collide.
//   - AC-1 risks gains `level`, `org_unit_id`, `themes` columns with the
//     right defaults (existing INSERTs continue to work; new INSERTs with
//     non-default values round-trip cleanly).
//
// Run via: just test-integration. Requires DATABASE_URL_APP pointed at the
// application-role pool (atlas_app — NOSUPERUSER, NOBYPASSRLS); the test
// helpers in integration_test.go (withTenantTx, mustInsertControl) are
// reused as-is.

package db_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ===== AC-9: cross-tenant RLS negatives =====
//
// One per new tenant-scoped table. Each follows the same shape: insert under
// tenant A, attempt to read under tenant B, expect zero rows. The shape is
// duplicated rather than parameterised because each table has subtly
// different columns and the test failure messages stay legible when one
// table-shape breaks.

func TestRLS_CrossTenant_HidesOrgUnit(t *testing.T) {
	tenantA := uuid.NewString()
	tenantB := uuid.NewString()
	orgUnitID := uuid.NewString()

	withTenantTx(t, tenantA, true, func(ctx context.Context, tx pgx.Tx) {
		mustInsertOrgUnit(ctx, t, tx, tenantA, orgUnitID, "AppSec", "team")
	})
	t.Cleanup(func() {
		withTenantTx(t, tenantA, true, func(ctx context.Context, tx pgx.Tx) {
			_, _ = tx.Exec(ctx, `DELETE FROM org_units WHERE id = $1`, orgUnitID)
		})
	})

	withTenantTx(t, tenantB, false, func(ctx context.Context, tx pgx.Tx) {
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM org_units WHERE id = $1`, orgUnitID).Scan(&n); err != nil {
			t.Fatalf("SELECT count: %v", err)
		}
		if n != 0 {
			t.Fatalf("tenant B saw %d rows of tenant A's org_unit; RLS bypassed", n)
		}
	})
}

func TestRLS_CrossTenant_HidesRiskAggregation(t *testing.T) {
	tenantA := uuid.NewString()
	tenantB := uuid.NewString()
	parentRisk := uuid.NewString()
	childRisk := uuid.NewString()

	withTenantTx(t, tenantA, true, func(ctx context.Context, tx pgx.Tx) {
		mustInsertRisk(ctx, t, tx, tenantA, parentRisk, "Parent risk", "operational")
		mustInsertRisk(ctx, t, tx, tenantA, childRisk, "Child risk", "operational")
		if _, err := tx.Exec(ctx, `
			INSERT INTO risk_aggregations (parent_risk_id, child_risk_id, rule_id, tenant_id)
			VALUES ($1, $2, NULL, $3)
		`, parentRisk, childRisk, tenantA); err != nil {
			t.Fatalf("INSERT risk_aggregations: %v", err)
		}
	})
	t.Cleanup(func() {
		withTenantTx(t, tenantA, true, func(ctx context.Context, tx pgx.Tx) {
			_, _ = tx.Exec(ctx, `DELETE FROM risk_aggregations WHERE parent_risk_id = $1`, parentRisk)
			_, _ = tx.Exec(ctx, `DELETE FROM risks WHERE id IN ($1, $2)`, parentRisk, childRisk)
		})
	})

	withTenantTx(t, tenantB, false, func(ctx context.Context, tx pgx.Tx) {
		var n int
		if err := tx.QueryRow(ctx, `
			SELECT count(*) FROM risk_aggregations WHERE parent_risk_id = $1 AND child_risk_id = $2
		`, parentRisk, childRisk).Scan(&n); err != nil {
			t.Fatalf("SELECT count: %v", err)
		}
		if n != 0 {
			t.Fatalf("tenant B saw %d rows of tenant A's risk_aggregations; RLS bypassed", n)
		}
	})
}

func TestRLS_CrossTenant_HidesDecision(t *testing.T) {
	tenantA := uuid.NewString()
	tenantB := uuid.NewString()
	decisionUUID := uuid.NewString()

	withTenantTx(t, tenantA, true, func(ctx context.Context, tx pgx.Tx) {
		mustInsertDecision(ctx, t, tx, tenantA, decisionUUID, "DL-2026-05-12")
	})
	t.Cleanup(func() {
		withTenantTx(t, tenantA, true, func(ctx context.Context, tx pgx.Tx) {
			_, _ = tx.Exec(ctx, `DELETE FROM decisions WHERE id = $1`, decisionUUID)
		})
	})

	withTenantTx(t, tenantB, false, func(ctx context.Context, tx pgx.Tx) {
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM decisions WHERE id = $1`, decisionUUID).Scan(&n); err != nil {
			t.Fatalf("SELECT count: %v", err)
		}
		if n != 0 {
			t.Fatalf("tenant B saw %d rows of tenant A's decision; RLS bypassed", n)
		}
	})
}

func TestRLS_CrossTenant_HidesDecisionLinks(t *testing.T) {
	tenantA := uuid.NewString()
	tenantB := uuid.NewString()
	decisionUUID := uuid.NewString()
	riskUUID := uuid.NewString()

	withTenantTx(t, tenantA, true, func(ctx context.Context, tx pgx.Tx) {
		mustInsertDecision(ctx, t, tx, tenantA, decisionUUID, "DL-2026-05-13")
		mustInsertRisk(ctx, t, tx, tenantA, riskUUID, "Linked risk", "operational")
		if _, err := tx.Exec(ctx, `
			INSERT INTO decision_risks (decision_id, target_id, tenant_id)
			VALUES ($1, $2, $3)
		`, decisionUUID, riskUUID, tenantA); err != nil {
			t.Fatalf("INSERT decision_risks: %v", err)
		}
	})
	t.Cleanup(func() {
		withTenantTx(t, tenantA, true, func(ctx context.Context, tx pgx.Tx) {
			_, _ = tx.Exec(ctx, `DELETE FROM decision_risks WHERE decision_id = $1`, decisionUUID)
			_, _ = tx.Exec(ctx, `DELETE FROM risks WHERE id = $1`, riskUUID)
			_, _ = tx.Exec(ctx, `DELETE FROM decisions WHERE id = $1`, decisionUUID)
		})
	})

	withTenantTx(t, tenantB, false, func(ctx context.Context, tx pgx.Tx) {
		var n int
		if err := tx.QueryRow(ctx, `
			SELECT count(*) FROM decision_risks WHERE decision_id = $1 AND target_id = $2
		`, decisionUUID, riskUUID).Scan(&n); err != nil {
			t.Fatalf("SELECT count: %v", err)
		}
		if n != 0 {
			t.Fatalf("tenant B saw %d decision_risks rows of tenant A's link; RLS bypassed", n)
		}
	})
}

func TestRLS_CrossTenant_HidesTenantPrivateTheme(t *testing.T) {
	tenantA := uuid.NewString()
	tenantB := uuid.NewString()
	themeUUID := uuid.NewString()

	withTenantTx(t, tenantA, true, func(ctx context.Context, tx pgx.Tx) {
		if _, err := tx.Exec(ctx, `
			INSERT INTO org_themes (id, tenant_id, theme_name, description)
			VALUES ($1, $2, 'tenant-only-theme', 'tenant A private theme')
		`, themeUUID, tenantA); err != nil {
			t.Fatalf("INSERT org_themes: %v", err)
		}
	})
	t.Cleanup(func() {
		withTenantTx(t, tenantA, true, func(ctx context.Context, tx pgx.Tx) {
			_, _ = tx.Exec(ctx, `DELETE FROM org_themes WHERE id = $1`, themeUUID)
		})
	})

	withTenantTx(t, tenantB, false, func(ctx context.Context, tx pgx.Tx) {
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM org_themes WHERE id = $1`, themeUUID).Scan(&n); err != nil {
			t.Fatalf("SELECT count: %v", err)
		}
		if n != 0 {
			t.Fatalf("tenant B saw %d rows of tenant A's private theme; RLS bypassed", n)
		}
	})
}

// ===== Positive smoke INSERT on every new table =====

func TestSchema_RiskHierarchyTablesAcceptInserts(t *testing.T) {
	tenant := uuid.NewString()
	orgUnitID := uuid.NewString()
	parentRisk := uuid.NewString()
	childRisk := uuid.NewString()
	decisionUUID := uuid.NewString()
	themeUUID := uuid.NewString()
	controlID := uuid.NewString()
	exceptionID := uuid.NewString()
	frameworkScopeID := uuid.NewString()
	frameworkID := uuid.NewString()
	frameworkVersionID := uuid.NewString()

	t.Cleanup(func() {
		withTenantTx(t, tenant, true, func(ctx context.Context, tx pgx.Tx) {
			for _, stmt := range []string{
				`DELETE FROM decision_scope_predicates WHERE tenant_id = $1`,
				`DELETE FROM decision_exceptions WHERE tenant_id = $1`,
				`DELETE FROM decision_controls WHERE tenant_id = $1`,
				`DELETE FROM decision_risks WHERE tenant_id = $1`,
				`DELETE FROM decisions WHERE tenant_id = $1`,
				`DELETE FROM risk_aggregations WHERE tenant_id = $1`,
				`DELETE FROM org_themes WHERE tenant_id = $1`,
				`DELETE FROM exceptions WHERE tenant_id = $1`,
				`DELETE FROM framework_scopes WHERE tenant_id = $1`,
				`DELETE FROM risks WHERE tenant_id = $1`,
				`DELETE FROM controls WHERE tenant_id = $1`,
				`DELETE FROM org_units WHERE tenant_id = $1`,
				`DELETE FROM framework_versions WHERE tenant_id = $1`,
				`DELETE FROM frameworks WHERE tenant_id = $1`,
			} {
				if _, err := tx.Exec(ctx, stmt, tenant); err != nil {
					t.Logf("cleanup %s: %v", stmt, err)
				}
			}
		})
	})

	withTenantTx(t, tenant, true, func(ctx context.Context, tx pgx.Tx) {
		// Pre-reqs: a control, a framework + version + scope, an exception,
		// and an org_unit + two risks. Each is needed as an FK target for the
		// link-table inserts below.
		mustInsertControl(ctx, t, tx, tenant, controlID, "ORG-01")
		mustInsertOrgUnit(ctx, t, tx, tenant, orgUnitID, "Platform", "org")
		mustInsertRisk(ctx, t, tx, tenant, parentRisk, "Parent meta-risk", "operational")
		mustInsertRisk(ctx, t, tx, tenant, childRisk, "Child instance risk", "operational")

		if _, err := tx.Exec(ctx, `
			INSERT INTO frameworks (id, tenant_id, name, slug, issuer)
			VALUES ($1, $2, 'Acme Internal', 'acme-internal', 'Acme')
		`, frameworkID, tenant); err != nil {
			t.Fatalf("INSERT frameworks: %v", err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO framework_versions (id, tenant_id, framework_id, version)
			VALUES ($1, $2, $3, '2026.1')
		`, frameworkVersionID, tenant, frameworkID); err != nil {
			t.Fatalf("INSERT framework_versions: %v", err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO framework_scopes (
				id, tenant_id, framework_version_id, name,
				state, predicate, predicate_hash
			)
			VALUES (
				$1, $2, $3, 'Slice 052 test scope',
				'draft', '{"op":"true"}'::jsonb,
				encode(sha256('{"op":"true"}'::bytea), 'hex')
			)
		`, frameworkScopeID, tenant, frameworkVersionID); err != nil {
			t.Fatalf("INSERT framework_scopes: %v", err)
		}

		// Exception requires a bound control + a scope predicate; slice 011's
		// table accepts a free-form predicate JSON.
		if _, err := tx.Exec(ctx, `
			INSERT INTO exceptions (
				id, tenant_id, control_id, scope_cell_predicate,
				justification, requested_by, expires_at, status
			)
			VALUES (
				$1, $2, $3, '{"op":"true"}'::jsonb,
				'slice 052 smoke test', 'tester@example',
				now() + interval '30 days', 'requested'
			)
		`, exceptionID, tenant, controlID); err != nil {
			t.Fatalf("INSERT exceptions: %v", err)
		}

		// org_themes (tenant-private)
		if _, err := tx.Exec(ctx, `
			INSERT INTO org_themes (id, tenant_id, theme_name, description)
			VALUES ($1, $2, 'slice-052-test-theme', 'slice 052 smoke')
		`, themeUUID, tenant); err != nil {
			t.Fatalf("INSERT org_themes: %v", err)
		}

		// risk_aggregations (manual — rule_id NULL)
		if _, err := tx.Exec(ctx, `
			INSERT INTO risk_aggregations (parent_risk_id, child_risk_id, rule_id, tenant_id)
			VALUES ($1, $2, NULL, $3)
		`, parentRisk, childRisk, tenant); err != nil {
			t.Fatalf("INSERT risk_aggregations: %v", err)
		}

		// decisions
		mustInsertDecision(ctx, t, tx, tenant, decisionUUID, "DL-2026-05-12-smoke")

		// All four decision link tables
		for _, stmt := range []struct {
			name   string
			sql    string
			target string
		}{
			{"decision_risks", `INSERT INTO decision_risks (decision_id, target_id, tenant_id) VALUES ($1, $2, $3)`, parentRisk},
			{"decision_controls", `INSERT INTO decision_controls (decision_id, target_id, tenant_id) VALUES ($1, $2, $3)`, controlID},
			{"decision_exceptions", `INSERT INTO decision_exceptions (decision_id, target_id, tenant_id) VALUES ($1, $2, $3)`, exceptionID},
			{"decision_scope_predicates", `INSERT INTO decision_scope_predicates (decision_id, target_id, tenant_id) VALUES ($1, $2, $3)`, frameworkScopeID},
		} {
			if _, err := tx.Exec(ctx, stmt.sql, decisionUUID, stmt.target, tenant); err != nil {
				t.Fatalf("INSERT %s: %v", stmt.name, err)
			}
		}
	})
}

// ===== AC-1: risks columns round-trip =====

func TestRisks_NewColumnsRoundTrip(t *testing.T) {
	tenant := uuid.NewString()
	orgUnitID := uuid.NewString()
	riskID := uuid.NewString()

	t.Cleanup(func() {
		withTenantTx(t, tenant, true, func(ctx context.Context, tx pgx.Tx) {
			_, _ = tx.Exec(ctx, `DELETE FROM risks WHERE tenant_id = $1`, tenant)
			_, _ = tx.Exec(ctx, `DELETE FROM org_units WHERE tenant_id = $1`, tenant)
		})
	})

	withTenantTx(t, tenant, true, func(ctx context.Context, tx pgx.Tx) {
		mustInsertOrgUnit(ctx, t, tx, tenant, orgUnitID, "AppSec", "team")

		// INSERT a risk with non-default level / org_unit / themes values to
		// prove the new columns accept writes (not just the default-only
		// path the slice-002 helper exercises).
		if _, err := tx.Exec(ctx, `
			INSERT INTO risks (
				id, tenant_id, title, category, treatment,
				level, org_unit_id, themes
			)
			VALUES ($1, $2, 'Themed company risk', 'operational', 'avoid',
				'company', $3, ARRAY['ownership','tech-debt']::text[])
		`, riskID, tenant, orgUnitID); err != nil {
			t.Fatalf("INSERT risks with new cols: %v", err)
		}

		var level string
		var orgUnit *string
		var themes []string
		if err := tx.QueryRow(ctx, `
			SELECT level::text, org_unit_id::text, themes
			FROM risks WHERE id = $1
		`, riskID).Scan(&level, &orgUnit, &themes); err != nil {
			t.Fatalf("SELECT risk: %v", err)
		}
		if level != "company" {
			t.Errorf("level = %q, want company", level)
		}
		if orgUnit == nil || *orgUnit != orgUnitID {
			t.Errorf("org_unit_id mismatch: got %v, want %s", orgUnit, orgUnitID)
		}
		if len(themes) != 2 || themes[0] != "ownership" || themes[1] != "tech-debt" {
			t.Errorf("themes = %v, want [ownership tech-debt]", themes)
		}
	})
}

// ===== AC-10: default theme seed presence + tenant-private no-collision =====

func TestThemes_DefaultsSeededAndDoNotCollide(t *testing.T) {
	defaults := []string{
		"ownership", "tech-debt", "access-control", "key-management",
		"data-protection", "availability", "monitoring", "supply-chain",
		"vendor-risk", "human-process",
	}

	// Defaults are visible to any tenant via the tenant_or_catalog_read policy.
	tenant := uuid.NewString()
	withTenantTx(t, tenant, false, func(ctx context.Context, tx pgx.Tx) {
		for _, name := range defaults {
			var count int
			if err := tx.QueryRow(ctx, `
				SELECT count(*) FROM org_themes WHERE tenant_id IS NULL AND theme_name = $1
			`, name).Scan(&count); err != nil {
				t.Fatalf("SELECT default theme %s: %v", name, err)
			}
			if count != 1 {
				t.Errorf("default theme %q present count = %d, want 1", name, count)
			}
		}
	})

	// A tenant creating its own theme with the same name as a default must
	// succeed — partial unique indexes scope uniqueness separately.
	themeUUID := uuid.NewString()
	t.Cleanup(func() {
		withTenantTx(t, tenant, true, func(ctx context.Context, tx pgx.Tx) {
			_, _ = tx.Exec(ctx, `DELETE FROM org_themes WHERE id = $1`, themeUUID)
		})
	})

	withTenantTx(t, tenant, true, func(ctx context.Context, tx pgx.Tx) {
		if _, err := tx.Exec(ctx, `
			INSERT INTO org_themes (id, tenant_id, theme_name, description)
			VALUES ($1, $2, 'ownership', 'tenant override of default')
		`, themeUUID, tenant); err != nil {
			t.Fatalf("tenant-private theme with default name collided: %v", err)
		}
	})

	// And the same tenant cannot create a second "ownership" themselves.
	dupeUUID := uuid.NewString()
	withTenantTx(t, tenant, false, func(ctx context.Context, tx pgx.Tx) {
		_, err := tx.Exec(ctx, `
			INSERT INTO org_themes (id, tenant_id, theme_name, description)
			VALUES ($1, $2, 'ownership', 'duplicate within tenant')
		`, dupeUUID, tenant)
		if err == nil {
			t.Fatalf("expected unique violation on duplicate tenant-private theme name; got nil")
		}
	})
}

// ===== Test helpers (slice 052) =====

// mustInsertOrgUnit seeds a single org_unit under the active tenant.
func mustInsertOrgUnit(ctx context.Context, t *testing.T, tx pgx.Tx, tenant, orgUnitID, name, level string) {
	t.Helper()
	if _, err := tx.Exec(ctx, `
		INSERT INTO org_units (id, tenant_id, name, level)
		VALUES ($1, $2, $3, $4::risk_level)
	`, orgUnitID, tenant, name, level); err != nil {
		t.Fatalf("INSERT org_units: %v", err)
	}
}

// mustInsertRisk seeds a single risk under the active tenant. Treatment
// defaults to 'avoid' to dodge the slice-019 CHECK constraints that require
// accepted_until/accepter for 'accept' and instrument_reference for
// 'transfer'.
func mustInsertRisk(ctx context.Context, t *testing.T, tx pgx.Tx, tenant, riskID, title, category string) {
	t.Helper()
	if _, err := tx.Exec(ctx, `
		INSERT INTO risks (id, tenant_id, title, category, treatment)
		VALUES ($1, $2, $3, $4::risk_category, 'avoid')
	`, riskID, tenant, title, category); err != nil {
		t.Fatalf("INSERT risks: %v", err)
	}
}

// mustInsertDecision seeds a single Decision Log entry under the active
// tenant. Defaults match canvas §6.7 with status='active' and an empty
// constraints[]; the test caller can override by following up with an UPDATE.
func mustInsertDecision(ctx context.Context, t *testing.T, tx pgx.Tx, tenant, decisionUUID, humanID string) {
	t.Helper()
	if _, err := tx.Exec(ctx, `
		INSERT INTO decisions (
			id, tenant_id, decision_id, title, narrative, decision_maker, decided_at
		)
		VALUES (
			$1, $2, $3, 'slice 052 smoke decision', 'narrative goes here',
			'tester@example', now()
		)
	`, decisionUUID, tenant, humanID); err != nil {
		t.Fatalf("INSERT decisions: %v", err)
	}
}
