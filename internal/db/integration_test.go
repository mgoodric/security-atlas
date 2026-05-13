//go:build integration

// Integration tests for the slice 002 schema. Verifies:
//   - Cross-tenant SELECT under RLS returns zero rows.
//   - Querying without SET LOCAL app.current_tenant returns zero rows
//     (the no-default-allow invariant).
//   - tenancy.ApplyTenant sets the GUC observably.
//
// Run via: just test-integration  (sets DATABASE_URL_APP and invokes
// `go test -tags=integration ./internal/db/...`).

package db_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// Postgres SQLSTATE code used in assertions.
const pgErrForeignKeyViolation = "23503"

var (
	appPool   *pgxpool.Pool
	adminPool *pgxpool.Pool
)

// TestMain opens both the application-role pool (for RLS-bound queries) and
// the admin-role pool (atlas_migrate, BYPASSRLS) for cleanup of append-only
// tables that the application role intentionally cannot delete from
// (evidence_records, evidence_audit_log — slice 013).
func TestMain(m *testing.M) {
	url := os.Getenv("DATABASE_URL_APP")
	if url == "" {
		fmt.Fprintln(os.Stderr, "DATABASE_URL_APP not set; skipping integration tests")
		os.Exit(0)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pgxpool.New: %v\n", err)
		os.Exit(1)
	}
	appPool = pool
	if adminURL := os.Getenv("DATABASE_URL"); adminURL != "" {
		ap, err := pgxpool.New(ctx, adminURL)
		if err != nil {
			fmt.Fprintf(os.Stderr, "pgxpool.New(admin): %v\n", err)
			os.Exit(1)
		}
		adminPool = ap
	}
	code := m.Run()
	pool.Close()
	if adminPool != nil {
		adminPool.Close()
	}
	os.Exit(code)
}

// withTenantTx runs fn inside a transaction with the tenant GUC applied.
// If commit is true, the tx commits (for seeding); otherwise it rolls back.
func withTenantTx(t *testing.T, tenant string, commit bool, fn func(context.Context, pgx.Tx)) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ctx, err := tenancy.WithTenant(ctx, tenant)
	if err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
	tx, err := appPool.Begin(ctx)
	if err != nil {
		t.Fatalf("pool.Begin: %v", err)
	}
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("ApplyTenant: %v", err)
	}

	fn(ctx, tx)

	if commit {
		if err := tx.Commit(ctx); err != nil {
			t.Fatalf("Commit: %v", err)
		}
	} else {
		_ = tx.Rollback(ctx)
	}
}

// TestRLS_CrossTenant_IsolatesSelects is the load-bearing test for slice 002:
// inserting under tenant A must never be visible under tenant B.
func TestRLS_CrossTenant_IsolatesSelects(t *testing.T) {
	tenantA := uuid.NewString()
	tenantB := uuid.NewString()
	controlID := uuid.NewString()

	withTenantTx(t, tenantA, true, func(ctx context.Context, tx pgx.Tx) {
		mustInsertControl(ctx, t, tx, tenantA, controlID, "IAC-01")
	})
	t.Cleanup(func() {
		withTenantTx(t, tenantA, true, func(ctx context.Context, tx pgx.Tx) {
			_, _ = tx.Exec(ctx, `DELETE FROM controls WHERE id = $1`, controlID)
		})
	})

	withTenantTx(t, tenantB, false, func(ctx context.Context, tx pgx.Tx) {
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM controls WHERE id = $1`, controlID).Scan(&n); err != nil {
			t.Fatalf("SELECT count: %v", err)
		}
		if n != 0 {
			t.Fatalf("tenant B saw %d rows for tenant A's control; RLS bypassed", n)
		}
	})

	withTenantTx(t, tenantA, false, func(ctx context.Context, tx pgx.Tx) {
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM controls WHERE id = $1`, controlID).Scan(&n); err != nil {
			t.Fatalf("SELECT count: %v", err)
		}
		if n != 1 {
			t.Fatalf("tenant A saw %d rows for own control; expected 1", n)
		}
	})
}

// TestRLS_NoTenantSet_DeniesByDefault verifies the no-default-allow invariant:
// without app.current_tenant set, SELECT returns zero rows.
func TestRLS_NoTenantSet_DeniesByDefault(t *testing.T) {
	tenant := uuid.NewString()
	controlID := uuid.NewString()

	withTenantTx(t, tenant, true, func(ctx context.Context, tx pgx.Tx) {
		mustInsertControl(ctx, t, tx, tenant, controlID, "IAC-02")
	})
	t.Cleanup(func() {
		withTenantTx(t, tenant, true, func(ctx context.Context, tx pgx.Tx) {
			_, _ = tx.Exec(ctx, `DELETE FROM controls WHERE id = $1`, controlID)
		})
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	tx, err := appPool.Begin(ctx)
	if err != nil {
		t.Fatalf("pool.Begin: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })

	// GUC must be unset for this test to prove anything.
	var setting *string
	if err := tx.QueryRow(ctx, `SELECT current_setting($1, true)`, tenancy.GUCName).Scan(&setting); err != nil {
		t.Fatalf("current_setting: %v", err)
	}
	if setting != nil && *setting != "" {
		t.Fatalf("GUC was %q; test cannot prove RLS", *setting)
	}

	var n int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM controls WHERE id = $1`, controlID).Scan(&n); err != nil {
		t.Fatalf("SELECT count: %v", err)
	}
	if n != 0 {
		t.Fatalf("expected 0 rows without tenant; got %d", n)
	}
}

// TestApplyTenant_SetsGUC sanity-checks that ApplyTenant actually changes the
// observable GUC, so the cross-tenant test doesn't pass for the wrong reason.
func TestApplyTenant_SetsGUC(t *testing.T) {
	tenant := uuid.NewString()
	withTenantTx(t, tenant, false, func(ctx context.Context, tx pgx.Tx) {
		var got string
		if err := tx.QueryRow(ctx, `SELECT current_setting($1, true)`, tenancy.GUCName).Scan(&got); err != nil {
			t.Fatalf("current_setting: %v", err)
		}
		if got != tenant {
			t.Fatalf("GUC = %q, want %q", got, tenant)
		}
	})
}

// TestRLS_CompositeFK_PreventsCrossTenantReference verifies decision D3: the
// composite FK on evidence_records(tenant_id, control_id) → controls(tenant_id, id)
// rejects an insert that references a control owned by a different tenant,
// even when the caller knows the target UUID. RLS hides the row on reads;
// the composite FK is what enforces the invariant on writes.
func TestRLS_CompositeFK_PreventsCrossTenantReference(t *testing.T) {
	tenantA := uuid.NewString()
	tenantB := uuid.NewString()
	controlA := uuid.NewString()
	controlB := uuid.NewString()

	// Seed: tenant A owns controlA; tenant B owns controlB.
	withTenantTx(t, tenantA, true, func(ctx context.Context, tx pgx.Tx) {
		mustInsertControl(ctx, t, tx, tenantA, controlA, "IAC-10")
	})
	withTenantTx(t, tenantB, true, func(ctx context.Context, tx pgx.Tx) {
		mustInsertControl(ctx, t, tx, tenantB, controlB, "IAC-11")
	})
	t.Cleanup(func() {
		withTenantTx(t, tenantA, true, func(ctx context.Context, tx pgx.Tx) {
			_, _ = tx.Exec(ctx, `DELETE FROM controls WHERE id = $1`, controlA)
		})
		withTenantTx(t, tenantB, true, func(ctx context.Context, tx pgx.Tx) {
			_, _ = tx.Exec(ctx, `DELETE FROM controls WHERE id = $1`, controlB)
		})
	})

	// As tenant A, attempt to attach evidence referencing controlB. Composite
	// FK lookup is (tenantA, controlB.id) → controls — no match, so the FK
	// rejects the insert.
	withTenantTx(t, tenantA, false, func(ctx context.Context, tx pgx.Tx) {
		// Slice 013 added a NOT NULL `control_ref` (free-form anchor text)
		// alongside the optional UUID `control_id`. The composite FK still
		// fires when control_id is set to a UUID owned by a different
		// tenant — that's the invariant this test validates.
		_, err := tx.Exec(ctx, `
			INSERT INTO evidence_records
				(id, tenant_id, control_id, control_ref, observed_at, provenance, result, hash)
			VALUES
				($1, $2, $3, $4, now(), '{"connector_id":"test"}'::jsonb, 'pass', 'abc123')
		`, uuid.NewString(), tenantA, controlB, "scf:IAC-11")
		if err == nil {
			t.Fatal("expected FK violation referencing other tenant's control; got nil error")
		}
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != pgErrForeignKeyViolation {
			t.Fatalf("expected SQLSTATE %s, got %v", pgErrForeignKeyViolation, err)
		}
	})
}

// TestSchema_TenantScopedTablesAcceptInserts is a smoke test that exercises
// one positive INSERT per tenant-scoped table to catch column-name typos that
// the cross-tenant test (which only touches `controls`) would miss.
func TestSchema_TenantScopedTablesAcceptInserts(t *testing.T) {
	tenant := uuid.NewString()
	controlID := uuid.NewString()
	scopeID := uuid.NewString()
	frameworkID := uuid.NewString()
	frameworkVersionID := uuid.NewString()

	t.Cleanup(func() {
		// Slice 013's append-only RLS shape means atlas_app can no longer
		// DELETE from evidence_records. Clean those rows via the admin
		// pool (BYPASSRLS) so we can subsequently DELETE the parent
		// controls. The remaining tenant-scoped rows still go through
		// the app pool so the RLS surface stays exercised.
		if adminPool != nil {
			ctxA, cancelA := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancelA()
			_, _ = adminPool.Exec(ctxA, `DELETE FROM evidence_audit_log WHERE tenant_id = $1`, tenant)
			_, _ = adminPool.Exec(ctxA, `DELETE FROM evidence_records WHERE tenant_id = $1`, tenant)
		}
		withTenantTx(t, tenant, true, func(ctx context.Context, tx pgx.Tx) {
			for _, stmt := range []string{
				`DELETE FROM framework_scopes WHERE tenant_id = $1`,
				`DELETE FROM policies WHERE tenant_id = $1`,
				`DELETE FROM scopes WHERE tenant_id = $1`,
				`DELETE FROM risks WHERE tenant_id = $1`,
				`DELETE FROM controls WHERE tenant_id = $1`,
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
		// Parent rows first (FK dependencies).
		mustInsertControl(ctx, t, tx, tenant, controlID, "AAA-01")

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

		// Now exercise every remaining tenant-scoped table.
		// Slice 019 added treatment-specific CHECK constraints. The slice-002
		// default treatment='accept' requires accepted_until + accepter; the
		// simplest survivor is treatment='avoid', which has no extra fields.
		if _, err := tx.Exec(ctx, `
			INSERT INTO risks (id, tenant_id, title, category, treatment)
			VALUES ($1, $2, 'Unauthorized access to PHI', 'confidentiality', 'avoid')
		`, uuid.NewString(), tenant); err != nil {
			t.Fatalf("INSERT risks: %v", err)
		}

		if _, err := tx.Exec(ctx, `
			INSERT INTO scopes (id, tenant_id, business_unit, environment)
			VALUES ($1, $2, 'platform', 'prod')
		`, scopeID, tenant); err != nil {
			t.Fatalf("INSERT scopes: %v", err)
		}

		// Slice 013: control_ref (free-form anchor text) is required;
		// control_id (UUID) remains optional. This smoke test sets both
		// to keep the composite-FK path exercised.
		if _, err := tx.Exec(ctx, `
			INSERT INTO evidence_records
				(id, tenant_id, control_id, control_ref, scope_id, observed_at, provenance, result, hash)
			VALUES
				($1, $2, $3, $4, $5, now(), '{"connector_id":"aws"}'::jsonb, 'pass', 'sha-abc')
		`, uuid.NewString(), tenant, controlID, "scf:AAA-01", scopeID); err != nil {
			t.Fatalf("INSERT evidence_records: %v", err)
		}

		if _, err := tx.Exec(ctx, `
			INSERT INTO policies (id, tenant_id, title, version, body_md, owner_role, approver_role, status)
			VALUES ($1, $2, 'Access Control Policy', '1.0.0', '# Access Control Policy', 'tenant_admin', 'security_lead', 'draft')
		`, uuid.NewString(), tenant); err != nil {
			t.Fatalf("INSERT policies: %v", err)
		}

		// Slice 018 reshaped framework_scopes: state, predicate (jsonb),
		// predicate_hash are now NOT NULL with no default. Supply the
		// canonical-true predicate and its sha256 hash so the row inserts
		// cleanly under the slice-014 four-policy RLS.
		if _, err := tx.Exec(ctx, `
			INSERT INTO framework_scopes (
				id, tenant_id, framework_version_id, name,
				state, predicate, predicate_hash
			)
			VALUES (
				$1, $2, $3, 'Q3 2026 audit boundary',
				'draft', '{"op":"true"}'::jsonb,
				encode(sha256('{"op":"true"}'::bytea), 'hex')
			)
		`, uuid.NewString(), tenant, frameworkVersionID); err != nil {
			t.Fatalf("INSERT framework_scopes: %v", err)
		}
	})
}

// mustInsertControl seeds a single control under the active tenant. Used by
// multiple tests; failure is fatal because subsequent assertions depend on it.
// Slice 009 added a NOT NULL `bundle_id` column; this helper synthesises a
// `legacy_<uuid>` value matching the slice-009 migration's backfill pattern
// so slice-002's existing tests continue to pass after the schema change.
// The bundle_id is computed in Go (not via SQL concat) because pgx's prepare
// phase cannot deduce a single type for `$1` when it appears both as a UUID
// (in `id`) and as text input to a concat (SQLSTATE 42P08).
func mustInsertControl(ctx context.Context, t *testing.T, tx pgx.Tx, tenant, controlID, scfID string) {
	t.Helper()
	bundleID := "legacy_" + controlID
	_, err := tx.Exec(ctx, `
		INSERT INTO controls (id, tenant_id, scf_id, title, control_family, implementation_type, bundle_id)
		VALUES ($1, $2, $3, 'test control', 'IAC', 'automated', $4)
	`, controlID, tenant, scfID, bundleID)
	if err != nil {
		t.Fatalf("INSERT controls: %v", err)
	}
}
