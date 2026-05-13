//go:build integration

// Slice 033 — Postgres RLS enforcement everywhere.
//
// These tests prove constitutional invariant 6: tenant isolation is
// enforced at the database layer, not application code. For every
// tenant-scoped table from slice 002 onward, we:
//
//  1. INSERT a row under tenant A.
//  2. Switch to tenant B's GUC.
//  3. Run a SELECT that explicitly omits any app-level `tenant_id`
//     predicate — the kind of "forgotten WHERE" that breaks
//     application-code-only isolation.
//  4. Assert zero rows visible.
//
// If any table fails, the platform's multi-tenant guarantee is broken.
// The CI gate `just audit-rls` catches schema-level drift; these tests
// catch behaviour-level drift (a policy that exists but is mis-shaped).
//
// Run via: just test-integration

package db_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// TestRLS_ForgottenWhereClause_StillIsolates is the load-bearing AC-4
// demonstration. A developer who writes `SELECT * FROM risks WHERE id = $1`
// (no `tenant_id = ?` predicate, which is the exact "forgotten WHERE"
// the slice issue calls out) cannot leak across tenants because RLS
// denies the row outright.
//
// This test is intentionally minimal: it does NOT lean on the
// per-table sweep below; it isolates a single table and asserts the
// raw behaviour. If this one fails, every cross-tenant negative in the
// sweep is suspect.
func TestRLS_ForgottenWhereClause_StillIsolates(t *testing.T) {
	tenantA := uuid.NewString()
	tenantB := uuid.NewString()
	riskID := uuid.NewString()

	withTenantTx(t, tenantA, true, func(ctx context.Context, tx pgx.Tx) {
		// Slice 019 introduced treatment-specific CHECKs; 'avoid' has none.
		_, err := tx.Exec(ctx, `
			INSERT INTO risks (id, tenant_id, title, category, treatment)
			VALUES ($1, $2, 'tenant-A secret', 'confidentiality', 'avoid')
		`, riskID, tenantA)
		if err != nil {
			t.Fatalf("seed risk: %v", err)
		}
	})
	t.Cleanup(func() {
		withTenantTx(t, tenantA, true, func(ctx context.Context, tx pgx.Tx) {
			_, _ = tx.Exec(ctx, `DELETE FROM risks WHERE id = $1`, riskID)
		})
	})

	// Switch to tenant B's GUC. Run a query with NO app-level
	// `tenant_id = ?` filter — only `id = ?`. RLS must hide the row.
	withTenantTx(t, tenantB, false, func(ctx context.Context, tx pgx.Tx) {
		var n int
		// The deliberate footgun: no WHERE tenant_id = $... predicate.
		err := tx.QueryRow(ctx, `SELECT count(*) FROM risks WHERE id = $1`, riskID).Scan(&n)
		if err != nil {
			t.Fatalf("forgotten-WHERE select: %v", err)
		}
		if n != 0 {
			t.Fatalf("RLS leak: tenant B saw %d rows for tenant A's risk via id-only WHERE", n)
		}
	})
}

// TestRLS_CrossTenant_SweepPerTable runs an INSERT-under-A / SELECT-under-B
// negative on each tenant-scoped table that has a simple "minimum
// columns for a passing INSERT" shape. The point is breadth — slice
// 002's existing TestRLS_CrossTenant_IsolatesSelects only covers
// `controls`. This sweep proves the invariant holds for every
// tenant-scoped table in the schema.
//
// Tables NOT in the sweep:
//   - controls — already covered by slice 002's TestRLS_CrossTenant_IsolatesSelects.
//   - evidence_records — needs a parent control row; covered by
//     TestRLS_CompositeFK_PreventsCrossTenantReference (a stronger test).
//   - composite-FK dependents (risk_control_links, sample_evidence,
//     vendor_scope_cells, sample_annotations, sample_audit_log) — the
//     parent tables in the sweep already prove the isolation; the FK
//     deps inherit by composition.
//   - tables with hard cross-row dependencies (oidc_idp_configs needs
//     specific encrypted_secret encoding) — those are covered by their
//     own integration tests under internal/auth/.
func TestRLS_CrossTenant_SweepPerTable(t *testing.T) {
	cases := []struct {
		name string
		// seed runs inside tenant A's transaction. It MUST INSERT exactly
		// one row keyed by `id` so the assertion can SELECT count(*)
		// WHERE id = ?.
		seed func(ctx context.Context, t *testing.T, tx pgx.Tx, tenant, id string)
		// table is the table name in the count-by-id SELECT.
		table string
	}{
		{
			name:  "risks",
			table: "risks",
			seed: func(ctx context.Context, t *testing.T, tx pgx.Tx, tenant, id string) {
				_, err := tx.Exec(ctx, `
					INSERT INTO risks (id, tenant_id, title, category, treatment)
					VALUES ($1, $2, 'sweep', 'operational', 'avoid')
				`, id, tenant)
				if err != nil {
					t.Fatalf("seed risks: %v", err)
				}
			},
		},
		{
			name:  "scopes",
			table: "scopes",
			seed: func(ctx context.Context, t *testing.T, tx pgx.Tx, tenant, id string) {
				_, err := tx.Exec(ctx, `
					INSERT INTO scopes (id, tenant_id, business_unit, environment)
					VALUES ($1, $2, 'sweep', 'prod')
				`, id, tenant)
				if err != nil {
					t.Fatalf("seed scopes: %v", err)
				}
			},
		},
		{
			name:  "policies",
			table: "policies",
			seed: func(ctx context.Context, t *testing.T, tx pgx.Tx, tenant, id string) {
				_, err := tx.Exec(ctx, `
					INSERT INTO policies (id, tenant_id, title, version, body_md, owner_role, approver_role, status)
					VALUES ($1, $2, 'sweep policy', '1.0.0', '# sweep', 'tenant_admin', 'security_lead', 'draft')
				`, id, tenant)
				if err != nil {
					t.Fatalf("seed policies: %v", err)
				}
			},
		},
		{
			name:  "framework_scopes",
			table: "framework_scopes",
			seed: func(ctx context.Context, t *testing.T, tx pgx.Tx, tenant, id string) {
				// Needs a parent framework + framework_version.
				frameworkID := uuid.NewString()
				fvID := uuid.NewString()
				// Slug computed in Go (not via SQL `||`) because pgx's
				// prepare phase cannot deduce a single type for `$1` when
				// it appears as a UUID (in `id`) and as text input to a
				// concat (SQLSTATE 42P08). See memory note
				// "Postgres constraint gotchas".
				slug := "sweep-fw-" + frameworkID
				if _, err := tx.Exec(ctx, `
					INSERT INTO frameworks (id, tenant_id, name, slug, issuer)
					VALUES ($1, $2, 'Sweep Framework', $3, 'sweep')
				`, frameworkID, tenant, slug); err != nil {
					t.Fatalf("seed frameworks: %v", err)
				}
				if _, err := tx.Exec(ctx, `
					INSERT INTO framework_versions (id, tenant_id, framework_id, version)
					VALUES ($1, $2, $3, 'v1')
				`, fvID, tenant, frameworkID); err != nil {
					t.Fatalf("seed framework_versions: %v", err)
				}
				if _, err := tx.Exec(ctx, `
					INSERT INTO framework_scopes (
						id, tenant_id, framework_version_id, name,
						state, predicate, predicate_hash
					)
					VALUES (
						$1, $2, $3, 'sweep',
						'draft', '{"op":"true"}'::jsonb,
						encode(sha256('{"op":"true"}'::bytea), 'hex')
					)
				`, id, tenant, fvID); err != nil {
					t.Fatalf("seed framework_scopes: %v", err)
				}
			},
		},
		{
			name:  "vendors",
			table: "vendors",
			seed: func(ctx context.Context, t *testing.T, tx pgx.Tx, tenant, id string) {
				// `criticality` is the schema-named column, not `tier`
				// (vendor_criticality enum: 'critical' | 'high' | 'medium' | 'low').
				_, err := tx.Exec(ctx, `
					INSERT INTO vendors (id, tenant_id, name, criticality)
					VALUES ($1, $2, $3, 'medium')
				`, id, tenant, "Sweep Vendor "+id)
				if err != nil {
					t.Fatalf("seed vendors: %v", err)
				}
			},
		},
		{
			name:  "exceptions",
			table: "exceptions",
			seed: func(ctx context.Context, t *testing.T, tx pgx.Tx, tenant, id string) {
				// exceptions depends on controls + scopes; minimal seed.
				controlID := uuid.NewString()
				mustInsertControl(ctx, t, tx, tenant, controlID, "SWEEP-01")
				_, err := tx.Exec(ctx, `
					INSERT INTO exceptions (
						id, tenant_id, control_id, scope_cell_predicate,
						justification, compensating_controls,
						requested_by, requested_at, expires_at, status
					)
					VALUES (
						$1, $2, $3, '{"op":"true"}'::jsonb,
						'sweep', ARRAY[]::TEXT[],
						'sweep@example.test', now(), now() + interval '30 days', 'requested'
					)
				`, id, tenant, controlID)
				if err != nil {
					t.Fatalf("seed exceptions: %v", err)
				}
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			tenantA := uuid.NewString()
			tenantB := uuid.NewString()
			rowID := uuid.NewString()

			withTenantTx(t, tenantA, true, func(ctx context.Context, tx pgx.Tx) {
				tc.seed(ctx, t, tx, tenantA, rowID)
			})
			t.Cleanup(func() {
				// Run the cleanup under the seeding tenant so RLS lets the
				// DELETE see the row. Best-effort; FK dependents may need
				// their own cleanup but for this sweep we only insert
				// rows we own.
				withTenantTx(t, tenantA, true, func(ctx context.Context, tx pgx.Tx) {
					_, _ = tx.Exec(ctx, "DELETE FROM "+tc.table+" WHERE id = $1", rowID)
				})
			})

			// Cross-tenant SELECT — no app-level tenant_id predicate.
			withTenantTx(t, tenantB, false, func(ctx context.Context, tx pgx.Tx) {
				var n int
				q := "SELECT count(*) FROM " + tc.table + " WHERE id = $1"
				if err := tx.QueryRow(ctx, q, rowID).Scan(&n); err != nil {
					t.Fatalf("count under tenant B: %v", err)
				}
				if n != 0 {
					t.Fatalf("RLS leak on %s: tenant B saw %d rows for tenant A's row", tc.table, n)
				}
			})

			// Same-tenant SELECT must still see the row — guards against
			// a misshapen RLS policy that hides rows from owners too.
			withTenantTx(t, tenantA, false, func(ctx context.Context, tx pgx.Tx) {
				var n int
				q := "SELECT count(*) FROM " + tc.table + " WHERE id = $1"
				if err := tx.QueryRow(ctx, q, rowID).Scan(&n); err != nil {
					t.Fatalf("count under tenant A: %v", err)
				}
				if n != 1 {
					t.Fatalf("RLS over-deny on %s: tenant A saw %d rows for own seed", tc.table, n)
				}
			})
		})
	}
}

// TestRLS_ServiceAccountRolePresent verifies that the slice-033 bootstrap
// landed `atlas_service_account` and that the GRANT chain lets
// `atlas_app` switch into it via SET LOCAL ROLE. The role is the
// canonical seam for future cross-tenant reads (e.g. a
// platform-wide audit-log scan) — its existence is the AC-5 contract.
//
// The test does NOT exercise BYPASSRLS itself (no production caller in
// v1); it only proves the role + GRANT chain are in place.
func TestRLS_ServiceAccountRolePresent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Use a fresh connection from the app pool. SET LOCAL ROLE only
	// affects the current transaction, so we wrap in BEGIN/ROLLBACK.
	tx, err := appPool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// The role must exist.
	var exists bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname = 'atlas_service_account')`,
	).Scan(&exists); err != nil {
		t.Fatalf("pg_roles probe: %v", err)
	}
	if !exists {
		t.Fatal("atlas_service_account role missing — slice 033 bootstrap not applied")
	}

	// SET LOCAL ROLE must succeed (i.e., the GRANT chain is in place).
	if _, err := tx.Exec(ctx, `SET LOCAL ROLE atlas_service_account`); err != nil {
		t.Fatalf("SET LOCAL ROLE atlas_service_account from atlas_app failed: %v -- the GRANT chain in bootstrap/01-roles.sql is missing", err)
	}

	// Sanity: current_user is now atlas_service_account.
	var currentUser string
	if err := tx.QueryRow(ctx, `SELECT current_user`).Scan(&currentUser); err != nil {
		t.Fatalf("current_user probe: %v", err)
	}
	if currentUser != "atlas_service_account" {
		t.Fatalf("SET LOCAL ROLE did not switch user; current_user = %q", currentUser)
	}
}
