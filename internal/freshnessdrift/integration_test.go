//go:build integration

// Integration tests for slice 016: the freshness + drift read-model refresh
// background job. Real Postgres only — the cross-tenant sweep and the
// RLS-honest per-tenant refresh are only meaningful against a real database.
// The DB is never mocked.
//
// The NATS RefreshSubscriber is not exercised here (it needs a JetStream
// substrate); its handler delegates to the SAME Refresher.RefreshTenant the
// Scheduler uses, so the Scheduler sweep covers the shared refresh path. The
// subscriber's wiring is covered by cmd/atlas build + the platform smoke
// path.
//
// Run with: go test -tags=integration -race ./internal/freshnessdrift/...
//
// Required env:
//   DATABASE_URL      - migration role DSN (BYPASSRLS); the harness seeds
//                       fixtures AND the Scheduler enumerates tenants through
//                       it.
//   DATABASE_URL_APP  - application role DSN (NOSUPERUSER NOBYPASSRLS); each
//                       tenant's refresh runs through it so RLS is enforced.

package freshnessdrift_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/freshnessdrift"
)

// ----- harness -----
//
// Slice 435: the pool/DSN/tenant-seed boilerplate this file used to
// re-derive (appDSN/adminDSN/openPool/freshTenant) now lives in the shared
// internal/dbtest harness. dbtest.NewAppPool / NewMigratePool replace the
// two-pool open; dbtest.SeedTenant replaces freshTenant, taking the
// slice-016 cleanup tables (children before parents) so the append-only
// control_drift_snapshots + evidence_records are removed through the
// privileged pool exactly as before.

// freshTenant seeds a fresh tenant and registers cleanup of every slice-016
// + dependency table through the privileged (migrate) pool. The order is
// FK-safe (children before parents); control_drift_snapshots and
// evidence_records are append-only under RLS, so the migrate pool is what
// can DELETE them.
func freshTenant(t *testing.T, migrate *pgxpool.Pool) string {
	t.Helper()
	return dbtest.SeedTenant(t, migrate,
		"evidence_freshness",
		"control_drift_snapshots",
		"control_evaluations",
		"evidence_records",
		"controls",
	)
}

func seedControl(t *testing.T, admin *pgxpool.Pool, tenant, freshnessClass string) uuid.UUID {
	t.Helper()
	ctrlID := uuid.New()
	var fc *string
	if freshnessClass != "" {
		fc = &freshnessClass
	}
	bundleID := "test-bundle-016wk-" + ctrlID.String()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO controls (
			id, tenant_id, title, control_family, implementation_type,
			bundle_id, freshness_class, evidence_queries, applicability_expr
		)
		VALUES ($1, $2, 'slice 016 worker test control', 'AAA', 'automated',
		        $3, $4, '[]'::jsonb, 'true')
	`, ctrlID, tenant, bundleID, fc); err != nil {
		t.Fatalf("seed control: %v", err)
	}
	return ctrlID
}

func seedEvidence(t *testing.T, admin *pgxpool.Pool, tenant string, ctrlID uuid.UUID, observedAt time.Time) {
	t.Helper()
	id := uuid.New()
	controlRef := ctrlID.String()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO evidence_records (
			id, tenant_id, control_id, observed_at, ingested_at,
			provenance, result, payload, hash, control_ref
		)
		VALUES ($1, $2, $3, $4, now(), '{}'::jsonb, 'pass', '{}'::jsonb, $5, $6)
	`, id, tenant, ctrlID, observedAt, "hash-016wk-"+id.String()[:8], controlRef); err != nil {
		t.Fatalf("seed evidence: %v", err)
	}
}

func seedEvaluation(t *testing.T, admin *pgxpool.Pool, tenant string, ctrlID uuid.UUID, result, freshnessStatus string, evaluatedAt time.Time) {
	t.Helper()
	id := uuid.New()
	runID := uuid.New()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO control_evaluations (
			id, tenant_id, control_id, scope_cell_id, eval_run_id,
			evaluated_at, result, freshness_status,
			evidence_count_in_window, trigger
		)
		VALUES ($1, $2, $3, NULL, $4, $5, $6, $7, 1, 'manual')
	`, id, tenant, ctrlID, runID, evaluatedAt, result, freshnessStatus); err != nil {
		t.Fatalf("seed evaluation: %v", err)
	}
}

func countFreshnessRows(t *testing.T, admin *pgxpool.Pool, tenant string) int {
	t.Helper()
	var n int
	if err := admin.QueryRow(context.Background(),
		`SELECT count(*) FROM evidence_freshness WHERE tenant_id = $1`, tenant).Scan(&n); err != nil {
		t.Fatalf("count freshness: %v", err)
	}
	return n
}

func countDriftSnapshots(t *testing.T, admin *pgxpool.Pool, tenant string) int {
	t.Helper()
	var n int
	if err := admin.QueryRow(context.Background(),
		`SELECT count(*) FROM control_drift_snapshots WHERE tenant_id = $1`, tenant).Scan(&n); err != nil {
		t.Fatalf("count drift snapshots: %v", err)
	}
	return n
}

// ===== AC-4: the daily scheduler sweep refreshes the freshness read model
// AND captures a drift snapshot, for every tenant =====

func TestSchedulerSweep_RefreshesFreshnessAndCapturesDrift(t *testing.T) {
	migrate := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, migrate)

	ctrlID := seedControl(t, migrate, tenant, "weekly")
	seedEvidence(t, migrate, tenant, ctrlID, time.Now().UTC().Add(-2*24*time.Hour))
	seedEvaluation(t, migrate, tenant, ctrlID, "pass", "fresh", time.Now().UTC().Add(-1*time.Hour))

	// Before the sweep: no read-model rows.
	if n := countFreshnessRows(t, migrate, tenant); n != 0 {
		t.Fatalf("pre-sweep freshness rows = %d, want 0", n)
	}
	if n := countDriftSnapshots(t, migrate, tenant); n != 0 {
		t.Fatalf("pre-sweep drift snapshots = %d, want 0", n)
	}

	// The scheduler runs as the migrator role (it enumerates tenants); each
	// tenant's refresh runs through an app-role Refresher.
	scheduler := freshnessdrift.NewScheduler(migrate, freshnessdrift.NewRefresherFactory(app), nil)
	swept, err := scheduler.SweepOnce(context.Background())
	if err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	if swept < 1 {
		t.Fatalf("SweepOnce swept %d tenants, want >= 1 (our tenant has an active control)", swept)
	}

	// After the sweep: the freshness read model is populated and a drift
	// snapshot was captured for our tenant.
	if n := countFreshnessRows(t, migrate, tenant); n != 1 {
		t.Errorf("post-sweep freshness rows = %d, want 1", n)
	}
	if n := countDriftSnapshots(t, migrate, tenant); n != 1 {
		t.Errorf("post-sweep drift snapshots = %d, want 1", n)
	}
}

// ===== AC-4: a second sweep UPSERTs the freshness row (no duplicate) and
// APPENDS another drift snapshot (the append-only ledger keeps history) =====

func TestSchedulerSweep_IsRepeatable(t *testing.T) {
	migrate := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, migrate)

	ctrlID := seedControl(t, migrate, tenant, "monthly")
	seedEvidence(t, migrate, tenant, ctrlID, time.Now().UTC().Add(-1*24*time.Hour))
	seedEvaluation(t, migrate, tenant, ctrlID, "pass", "fresh", time.Now().UTC().Add(-1*time.Hour))

	scheduler := freshnessdrift.NewScheduler(migrate, freshnessdrift.NewRefresherFactory(app), nil)
	if _, err := scheduler.SweepOnce(context.Background()); err != nil {
		t.Fatalf("SweepOnce #1: %v", err)
	}
	if _, err := scheduler.SweepOnce(context.Background()); err != nil {
		t.Fatalf("SweepOnce #2: %v", err)
	}

	// freshness is an UPSERTed current-state table -> still one row.
	if n := countFreshnessRows(t, migrate, tenant); n != 1 {
		t.Errorf("freshness rows after two sweeps = %d, want 1 (UPSERT, not duplicate)", n)
	}
	// drift snapshots are an append-only ledger -> two rows (latest-wins on
	// read). This is what makes the day-over-day diff and the audit trail
	// possible.
	if n := countDriftSnapshots(t, migrate, tenant); n != 2 {
		t.Errorf("drift snapshots after two sweeps = %d, want 2 (append-only ledger)", n)
	}
}
