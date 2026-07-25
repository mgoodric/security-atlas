//go:build integration

// Integration coverage for the control-bundle test runner's SQL path (slice
// 496, AC-4). The Rego + JSON-path languages evaluate in-process and are
// covered by the unit tests with no DB (AC-9); SQL evaluation runs inside a
// read-only Postgres subtransaction, so it needs a real connection. This test
// drives a SQL bundle end to end through the SAME engine the live path uses,
// proving the runner gains SQL coverage automatically once slice 495 lands.
//
// The runner needs an outer transaction carrying the tenant GUC because the SQL
// evaluator opens a nested read-only subtransaction on it (internal/eval/sql.go).
// We open the application pool (NOSUPERUSER NOBYPASSRLS), begin a tx, apply the
// tenant GUC, and hand that tx to bundletest.Run via Options.Tx — mirroring how
// the live engine threads its own tenant-scoped tx into evalSQLQuery. NO
// evidence is written: the fixtures are passed in-memory; the tx exists only so
// the SQL CTE has a session to run in (invariant #2 / P0-496-2).
package bundletest

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/tenancy"
)

func TestRun_SQLBundle_EndToEnd(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL_APP")
	if dsn == "" {
		t.Skip("DATABASE_URL_APP not set; skipping SQL integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	defer pool.Close()

	// A throwaway tenant id — no rows are written, but the GUC must be set for
	// the SQL subtransaction to begin under RLS context. (The SQL query only
	// reads the in-memory `evidence` CTE, never a tenant table.)
	tenant := uuid.NewString()
	tctx, err := tenancy.WithTenant(ctx, tenant)
	if err != nil {
		t.Fatalf("WithTenant: %v", err)
	}

	tx, err := pool.Begin(tctx)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tenancy.ApplyTenant(tctx, tx); err != nil {
		t.Fatalf("ApplyTenant: %v", err)
	}

	rep, err := Run(tctx, "testdata/sql-bundle", Options{
		Now: mustTime(t, fixedNow),
		Tx:  tx,
	})
	if err != nil {
		t.Fatalf("Run(sql-bundle): %v", err)
	}
	if !rep.AllPassed() {
		t.Fatalf("AC-4: SQL bundle cases should all pass; report: %+v", rep)
	}
	byName := indexByName(rep)
	if got := byName["all-encrypted-pass"].ActualState; got != "pass" {
		t.Fatalf("all-encrypted SQL actual = %q, want pass", got)
	}
	if got := byName["one-unencrypted-fail"].ActualState; got != "fail" {
		t.Fatalf("one-unencrypted SQL actual = %q, want fail", got)
	}
}
