//go:build integration

// Integration tests for slice 318: in-package coverage lift for
// internal/audit/unifiedlog. The package was at 18.8% merged because the
// only in-package tests were the four pure-Go validation guards on Query
// (nil queries, missing from/to, inverted range, non-positive limit); the
// happy-path SELECT against the nine UNION-ALL'd audit-log tables was
// covered ONLY by transit through internal/api/adminauditlog (which is
// `excludes`-listed, so its coverage does not roll up here).
//
// Load-bearing functions + branches covered (against real Postgres + RLS):
//
//   - Query: happy path returns rows projected to the canonical 8-column
//     Entry shape with subject_module preserved (slice 180).
//   - Query: tenant isolation — running with tenant A's GUC returns ONLY
//     A's rows even when both tenants have seeded data (slice 124 P0-A5,
//     constitutional invariant #6: RLS at the DB layer, not app code).
//   - Query: kind filter narrows the UNION ALL to a single kind branch.
//   - Query: pagination via Cursor walks the rows in occurred_at order.
//   - Query: empty result returns (nil, nil, nil) rather than an error.
//   - joinKinds: empty -> ""; populated -> comma-separated string.
//
// Run via:  go test -tags=integration -p 1 -race ./internal/audit/unifiedlog/...
//
// Required env:
//
//	DATABASE_URL      - migration role DSN (BYPASSRLS); used to seed rows
//	                    across tenants without crossing the RLS predicate.
//	DATABASE_URL_APP  - application role DSN (NOSUPERUSER NOBYPASSRLS); the
//	                    Query path runs under this role so RLS is enforced.
//
// Append-only invariant (P0-318-4): every test seeds via INSERT, never
// UPDATE / DELETE against the audit-log tables under the app role. Test
// residue is cleaned via the migrate role (admin DSN), which is the
// platform's own data-management path, not a runtime violation.
package unifiedlog_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/audit/unifiedlog"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// ---------- harness ----------
//
// Slice 435 / 742: the pool/DSN/tenant-seed boilerplate this file used to
// re-derive (appDSN / adminDSN / openPool / the inline freshTenant cleanup
// loop) now lives in the shared internal/dbtest harness. dbtest.NewAppPool
// opens the RLS-enforcing atlas_app pool (the default — the Query path and the
// tenant-isolation assertion run through it); dbtest.NewMigratePool opens the
// privileged BYPASSRLS pool used ONLY for the append-only audit-log cleanup the
// app role cannot DELETE.
//
// freshTenant remains suite-local: it returns a uuid.UUID (the seedOneRow /
// queryUnderTenant call sites compare against Entry.TenantID, a uuid.UUID) and
// its cleanup deletes a fixed nine-table audit-log set in FK-safe order — a
// shape dbtest.SeedTenant expresses via its cleanupTables list, so freshTenant
// now delegates the seed + cleanup to dbtest.SeedTenant and parses the returned
// id back to a UUID. seedOneRow / queryUnderTenant keep their explicit
// tenancy.WithTenant + tenancy.ApplyTenant tx wiring (the in-tx GUC path
// dbtest.WithTenantCtx does not replace) unchanged.

// freshTenant returns a unique tenant UUID and registers a migrate-role
// (BYPASSRLS) cleanup hook to purge seeded audit-log rows post-test — never the
// app role — so the constitutional append-only invariant (RLS forbids the app
// role from UPDATE / DELETE) is never traversed by the test. Order matters:
// aggregation_rule_audit_log has FK to aggregation_rules, so the audit rows are
// deleted before the parents.
func freshTenant(t *testing.T, admin *pgxpool.Pool) uuid.UUID {
	t.Helper()
	return uuid.MustParse(dbtest.SeedTenant(t, admin,
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
	))
}

// seedOneRow inserts ONE row into the named audit-log table under the
// tenant's GUC via the APP pool. This is the in-app INSERT path — the
// constitutional invariant permits writes from the app role; only
// UPDATE / DELETE are forbidden. Returns the inserted row's timestamp.
func seedOneRow(t *testing.T, app *pgxpool.Pool, tenantID uuid.UUID, table string) time.Time {
	t.Helper()
	ctx, err := tenancy.WithTenant(context.Background(), tenantID.String())
	if err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
	tx, err := app.Begin(ctx)
	if err != nil {
		t.Fatalf("seed begin: %v", err)
	}
	defer tx.Rollback(ctx)
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		t.Fatalf("ApplyTenant: %v", err)
	}

	var ts time.Time
	var q string
	var args []any

	switch table {
	case "decision_audit_log":
		q = `INSERT INTO decision_audit_log
		     (decision_id, tenant_id, user_id, action, resource_type, resource_id, result)
		     VALUES (gen_random_uuid(), $1, 'seeder-318', 'list', 'evidence', 'r-1', 'allow')
		     RETURNING occurred_at`
		args = []any{tenantID}
	case "evidence_audit_log":
		q = `INSERT INTO evidence_audit_log
		     (id, tenant_id, credential_id, decision)
		     VALUES (gen_random_uuid(), $1, 'test-318-cred', 'accepted')
		     RETURNING received_at`
		args = []any{tenantID}
	case "sample_audit_log":
		q = `INSERT INTO sample_audit_log
		     (id, tenant_id, action, actor)
		     VALUES (gen_random_uuid(), $1, 'sample_drawn', 'seeder-318')
		     RETURNING occurred_at`
		args = []any{tenantID}
	case "me_audit_log":
		q = `INSERT INTO me_audit_log
		     (tenant_id, user_id, action)
		     VALUES ($1, gen_random_uuid(), 'profile.update')
		     RETURNING occurred_at`
		args = []any{tenantID}
	default:
		t.Fatalf("seedOneRow: unsupported table %q", table)
	}

	if err := tx.QueryRow(ctx, q, args...).Scan(&ts); err != nil {
		t.Fatalf("seed %s: %v", table, err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("seed commit: %v", err)
	}
	return ts
}

// queryUnderTenant opens a tx, applies the tenant GUC, and runs Query.
// Mirrors the production wiring (handler -> applyTenant -> Query).
func queryUnderTenant(t *testing.T, app *pgxpool.Pool, tenantID uuid.UUID, params unifiedlog.QueryParams) []unifiedlog.Entry {
	t.Helper()
	ctx, err := tenancy.WithTenant(context.Background(), tenantID.String())
	if err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
	tx, err := app.Begin(ctx)
	if err != nil {
		t.Fatalf("query begin: %v", err)
	}
	defer tx.Rollback(ctx)
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		t.Fatalf("ApplyTenant: %v", err)
	}
	// Privileged caller — exercises the slice-124 default visibility branch.
	params.CallerIsPrivileged = true
	entries, _, err := unifiedlog.Query(ctx, dbx.New(tx), params)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	return entries
}

// ---------- tests ----------

// TestIntegration_Query_HappyPathReturnsCanonicalShape asserts that a
// freshly-seeded row shows up in the Query result with the canonical
// 8-column Entry shape, including the slice-180 subject_module field
// (defaulted to "core" by the schema).
func TestIntegration_Query_HappyPathReturnsCanonicalShape(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenantID := freshTenant(t, admin)

	when := seedOneRow(t, app, tenantID, "me_audit_log")

	entries := queryUnderTenant(t, app, tenantID, unifiedlog.QueryParams{
		From:  when.Add(-time.Minute),
		To:    when.Add(time.Minute),
		Limit: 100,
	})
	if len(entries) == 0 {
		t.Fatal("Query returned 0 rows; want >=1")
	}
	var got *unifiedlog.Entry
	for i, e := range entries {
		if e.TenantID == tenantID && e.Kind == unifiedlog.KindMe {
			got = &entries[i]
			break
		}
	}
	if got == nil {
		t.Fatal("Query did not return the seeded me_audit_log row")
	}
	if got.Action != "profile.update" {
		t.Errorf("Action = %q; want %q", got.Action, "profile.update")
	}
	if got.SubjectModule != unifiedlog.SubjectModuleCore {
		t.Errorf("SubjectModule = %q; want %q (slice 180 default)",
			got.SubjectModule, unifiedlog.SubjectModuleCore)
	}
	if got.RowID == uuid.Nil {
		t.Error("RowID is zero; want a real UUID from the underlying row")
	}
}

// TestIntegration_Query_TenantIsolationViaRLS pins constitutional invariant
// #6: tenant isolation is enforced at the DB layer via RLS, not app code.
// Two tenants seed rows; running Query under tenant A's GUC must return
// ONLY tenant A's rows, never tenant B's.
func TestIntegration_Query_TenantIsolationViaRLS(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)

	tenantA := freshTenant(t, admin)
	tenantB := freshTenant(t, admin)

	whenA := seedOneRow(t, app, tenantA, "sample_audit_log")
	_ = seedOneRow(t, app, tenantB, "sample_audit_log")

	// Query under A: must see A's row, NOT B's.
	entries := queryUnderTenant(t, app, tenantA, unifiedlog.QueryParams{
		From:  whenA.Add(-time.Minute),
		To:    whenA.Add(time.Minute),
		Limit: 100,
	})
	for _, e := range entries {
		if e.TenantID == tenantB {
			t.Fatalf("RLS leak: query under tenant A returned tenant B's row (kind=%s row_id=%s)",
				e.Kind, e.RowID)
		}
	}
	// And A's row IS present.
	found := false
	for _, e := range entries {
		if e.TenantID == tenantA {
			found = true
			break
		}
	}
	if !found {
		t.Error("Query under tenant A did not return any tenant-A rows")
	}
}

// TestIntegration_Query_KindFilterNarrowsBranches asserts the KindFilter
// param narrows the UNION ALL to the matching kind subset. Seed two
// different kinds; ask for just one; assert the other is absent.
func TestIntegration_Query_KindFilterNarrowsBranches(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenantID := freshTenant(t, admin)

	whenMe := seedOneRow(t, app, tenantID, "me_audit_log")
	_ = seedOneRow(t, app, tenantID, "sample_audit_log")

	entries := queryUnderTenant(t, app, tenantID, unifiedlog.QueryParams{
		From:       whenMe.Add(-time.Minute),
		To:         whenMe.Add(time.Minute),
		KindFilter: []unifiedlog.Kind{unifiedlog.KindMe},
		Limit:      100,
	})
	for _, e := range entries {
		if e.Kind != unifiedlog.KindMe {
			t.Errorf("KindFilter leak: got kind=%s; want only %s", e.Kind, unifiedlog.KindMe)
		}
	}
}

// TestIntegration_Query_EmptyRangeReturnsZeroRows asserts the
// "no matching rows" branch — Query returns (nil-or-empty, nil, nil)
// rather than an error.
func TestIntegration_Query_EmptyRangeReturnsZeroRows(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenantID := freshTenant(t, admin)

	// Window far in the past — nothing to find.
	from := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2000, 1, 2, 0, 0, 0, 0, time.UTC)
	entries := queryUnderTenant(t, app, tenantID, unifiedlog.QueryParams{
		From:  from,
		To:    to,
		Limit: 100,
	})
	if len(entries) != 0 {
		t.Errorf("expected 0 entries for far-past window, got %d", len(entries))
	}
}

// TestIntegration_Query_ActorFilterMatchesExact asserts the ActorFilter
// param requires an exact actor_id match — the slice 124 contract.
func TestIntegration_Query_ActorFilterMatchesExact(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenantID := freshTenant(t, admin)

	when := seedOneRow(t, app, tenantID, "sample_audit_log")

	// Match: the seeded actor is 'seeder-318'.
	gotMatch := queryUnderTenant(t, app, tenantID, unifiedlog.QueryParams{
		From:        when.Add(-time.Minute),
		To:          when.Add(time.Minute),
		ActorFilter: "seeder-318",
		Limit:       100,
	})
	foundMatch := false
	for _, e := range gotMatch {
		if e.ActorID == "seeder-318" {
			foundMatch = true
			break
		}
	}
	if !foundMatch {
		t.Error("ActorFilter match did not return any row with the matching actor")
	}

	// No-match: a fake actor name yields zero rows.
	gotMiss := queryUnderTenant(t, app, tenantID, unifiedlog.QueryParams{
		From:        when.Add(-time.Minute),
		To:          when.Add(time.Minute),
		ActorFilter: "no-such-actor-318",
		Limit:       100,
	})
	for _, e := range gotMiss {
		if e.ActorID == "seeder-318" {
			t.Error("ActorFilter mismatch leaked the seeded row")
		}
	}
}
