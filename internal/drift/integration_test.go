//go:build integration

// Integration tests for slice 016: control drift read model. Real Postgres
// only — RLS, the append-only control_drift_snapshots ledger, and the
// day-over-day delta computation are only meaningful against a real database.
// The DB is never mocked.
//
// Run with: go test -tags=integration -race ./internal/drift/...
//
// Required env:
//   DATABASE_URL      - migration role DSN (BYPASSRLS); the harness seeds
//                       controls + evaluations + drift snapshots outside the
//                       GUC.
//   DATABASE_URL_APP  - application role DSN (NOSUPERUSER NOBYPASSRLS); the
//                       drift.Store runs against this so RLS is enforced.

package drift_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/drift"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// ----- harness -----

func appDSN(t *testing.T) string {
	t.Helper()
	v := os.Getenv("DATABASE_URL_APP")
	if v == "" {
		t.Skip("DATABASE_URL_APP not set; skipping integration test")
	}
	return v
}

func adminDSN(t *testing.T) string {
	t.Helper()
	v := os.Getenv("DATABASE_URL")
	if v == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}
	return v
}

func openPool(t *testing.T, dsn string) *pgxpool.Pool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	return pool
}

// freshTenant cleans every slice-016 + dependency table for the tenant after
// the test. control_drift_snapshots + control_evaluations drop before
// controls (FK order).
func freshTenant(t *testing.T, admin *pgxpool.Pool) string {
	t.Helper()
	tenant := uuid.NewString()
	t.Cleanup(func() {
		ctx := context.Background()
		for _, stmt := range []string{
			`DELETE FROM control_drift_snapshots WHERE tenant_id = $1`,
			`DELETE FROM control_evaluations WHERE tenant_id = $1`,
			`DELETE FROM scope_cells WHERE tenant_id = $1`,
			`DELETE FROM controls WHERE tenant_id = $1`,
		} {
			if _, err := admin.Exec(ctx, stmt, tenant); err != nil {
				t.Logf("cleanup %s: %v", stmt, err)
			}
		}
	})
	return tenant
}

func ctxFor(t *testing.T, tenant string) context.Context {
	t.Helper()
	ctx, err := tenancy.WithTenant(context.Background(), tenant)
	if err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
	return ctx
}

// seedControl inserts one control row.
func seedControl(t *testing.T, admin *pgxpool.Pool, tenant string) uuid.UUID {
	t.Helper()
	ctrlID := uuid.New()
	bundleID := "test-bundle-016dr-" + ctrlID.String()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO controls (
			id, tenant_id, title, control_family, implementation_type,
			bundle_id, freshness_class, evidence_queries, applicability_expr
		)
		VALUES ($1, $2, 'slice 016 drift test control', 'AAA', 'automated',
		        $3, 'monthly', '[]'::jsonb, 'true')
	`, ctrlID, tenant, bundleID); err != nil {
		t.Fatalf("seed control: %v", err)
	}
	return ctrlID
}

// seedEvaluation appends one control_evaluations row for a control at a
// whole-tenant (NULL scope_cell) grain — the drift snapshot reads these.
// `result` is pass|fail|na|inconclusive; `freshnessStatus` is
// fresh|stale|no_evidence; `evaluatedAt` pins the row in time.
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

// seedSnapshot appends one control_drift_snapshots row directly — used to set
// up "yesterday" without time-travelling the Store's clock.
func seedSnapshot(t *testing.T, admin *pgxpool.Pool, tenant string, day time.Time, passing []uuid.UUID) {
	t.Helper()
	id := uuid.New()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO control_drift_snapshots (
			id, tenant_id, snapshot_date, controls_passing,
			passing_control_ids, trigger
		)
		VALUES ($1, $2, $3, $4, $5, 'scheduled')
	`, id, tenant, day, len(passing), passing); err != nil {
		t.Fatalf("seed snapshot: %v", err)
	}
}

func dayOf(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

func containsControl(rows []drift.DriftRow, id uuid.UUID) bool {
	for _, r := range rows {
		if r.ControlID == id {
			return true
		}
	}
	return false
}

// ===== tracer bullet: CaptureSnapshot records the worst-cell passing set =====

func TestCaptureSnapshot_RecordsPassingControls(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	app := openPool(t, appDSN(t))
	defer app.Close()
	tenant := freshTenant(t, admin)
	ctx := ctxFor(t, tenant)

	now := time.Now().UTC()
	// A control whose latest evaluation is pass + fresh -> passing.
	passingCtrl := seedControl(t, admin, tenant)
	seedEvaluation(t, admin, tenant, passingCtrl, "pass", "fresh", now.Add(-1*time.Hour))
	// A control whose latest evaluation is fail -> not passing.
	failingCtrl := seedControl(t, admin, tenant)
	seedEvaluation(t, admin, tenant, failingCtrl, "fail", "fresh", now.Add(-1*time.Hour))
	// A control whose latest evaluation is pass but STALE -> not passing
	// (canvas §2.3: stale evidence drives the drift signal).
	stalePassCtrl := seedControl(t, admin, tenant)
	seedEvaluation(t, admin, tenant, stalePassCtrl, "pass", "stale", now.Add(-1*time.Hour))

	store := drift.NewStore(app)
	snap, err := store.CaptureSnapshot(ctx, drift.TriggerManual)
	if err != nil {
		t.Fatalf("CaptureSnapshot: %v", err)
	}
	if snap.ControlsPassing != 1 {
		t.Errorf("ControlsPassing = %d, want 1 — only the pass+fresh control counts", snap.ControlsPassing)
	}
	if len(snap.PassingControlIDs) != 1 || snap.PassingControlIDs[0] != passingCtrl {
		t.Errorf("PassingControlIDs = %v, want [%s]", snap.PassingControlIDs, passingCtrl)
	}
}

// ===== worst-cell rollup: a control with one fresh-pass cell AND one
// fresh-fail cell is NOT passing =====

func TestCaptureSnapshot_WorstCellRollup(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	app := openPool(t, appDSN(t))
	defer app.Close()
	tenant := freshTenant(t, admin)
	ctx := ctxFor(t, tenant)

	now := time.Now().UTC()
	ctrlID := seedControl(t, admin, tenant)
	// Two scope cells for the same control. The snapshot query keys latest
	// state per (control_id, scope_cell_id) — but both seeded rows here use
	// NULL scope_cell_id, so to exercise the worst-cell rollup we seed two
	// DIFFERENT cells. Insert raw cell rows + per-cell evaluations.
	cellA := uuid.New()
	cellB := uuid.New()
	for _, c := range []uuid.UUID{cellA, cellB} {
		if _, err := admin.Exec(context.Background(), `
			INSERT INTO scope_cells (id, tenant_id, label, dimensions, dimensions_hash)
			VALUES ($1, $2, $3, '{"environment":"prod"}'::jsonb, $4)
		`, c, tenant, "cell-"+c.String()[:8], "hash-"+c.String()[:8]); err != nil {
			t.Fatalf("seed cell: %v", err)
		}
	}
	// cellA: pass+fresh. cellB: fail+fresh. Worst-cell -> control not passing.
	seedEvaluationWithCell(t, admin, tenant, ctrlID, cellA, "pass", "fresh", now.Add(-1*time.Hour))
	seedEvaluationWithCell(t, admin, tenant, ctrlID, cellB, "fail", "fresh", now.Add(-1*time.Hour))

	store := drift.NewStore(app)
	snap, err := store.CaptureSnapshot(ctx, drift.TriggerManual)
	if err != nil {
		t.Fatalf("CaptureSnapshot: %v", err)
	}
	if snap.ControlsPassing != 0 {
		t.Errorf("ControlsPassing = %d, want 0 — one failing cell means the control is not passing", snap.ControlsPassing)
	}
}

func seedEvaluationWithCell(t *testing.T, admin *pgxpool.Pool, tenant string, ctrlID, cellID uuid.UUID, result, freshnessStatus string, evaluatedAt time.Time) {
	t.Helper()
	id := uuid.New()
	runID := uuid.New()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO control_evaluations (
			id, tenant_id, control_id, scope_cell_id, eval_run_id,
			evaluated_at, result, freshness_status,
			evidence_count_in_window, trigger
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 1, 'manual')
	`, id, tenant, ctrlID, cellID, runID, evaluatedAt, result, freshnessStatus); err != nil {
		t.Fatalf("seed evaluation with cell: %v", err)
	}
}

// ===== AC-3: Report computes the signed delta + the flipped-to-fail set =====

func TestReport_ComputesSignedDeltaAndFlips(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	app := openPool(t, appDSN(t))
	defer app.Close()
	tenant := freshTenant(t, admin)
	ctx := ctxFor(t, tenant)

	now := time.Now().UTC()
	ctrlStable := seedControl(t, admin, tenant)  // passing both days
	ctrlFlipped := seedControl(t, admin, tenant) // passing yesterday, NOT today

	// Yesterday's snapshot: both controls passing (count 2).
	seedSnapshot(t, admin, tenant, dayOf(now.AddDate(0, 0, -1)),
		[]uuid.UUID{ctrlStable, ctrlFlipped})

	// Today: only the stable control is pass+fresh; the flipped control
	// went to fail. CaptureSnapshot records today's set (count 1).
	seedEvaluation(t, admin, tenant, ctrlStable, "pass", "fresh", now.Add(-1*time.Hour))
	seedEvaluation(t, admin, tenant, ctrlFlipped, "fail", "fresh", now.Add(-1*time.Hour))

	store := drift.NewStore(app)
	if _, err := store.CaptureSnapshot(ctx, drift.TriggerScheduled); err != nil {
		t.Fatalf("CaptureSnapshot today: %v", err)
	}

	report, err := store.Report(ctx, 7*24*time.Hour)
	if err != nil {
		t.Fatalf("Report: %v", err)
	}
	// delta = passing(today=1) - passing(yesterday=2) = -1.
	if report.Delta != -1 {
		t.Errorf("Delta = %d, want -1 (2 passing yesterday -> 1 today)", report.Delta)
	}
	if !containsControl(report.FlippedToOut, ctrlFlipped) {
		t.Errorf("FlippedToOut missing the control that went pass->fail: %s", ctrlFlipped)
	}
	if containsControl(report.FlippedToOut, ctrlStable) {
		t.Error("FlippedToOut wrongly includes the still-passing control")
	}
	// The flipped control's last_passing date should be yesterday.
	for _, fr := range report.FlippedToOut {
		if fr.ControlID == ctrlFlipped {
			wantDay := dayOf(now.AddDate(0, 0, -1))
			if !fr.LastPassing.Equal(wantDay) {
				t.Errorf("flipped control LastPassing = %v, want %v (yesterday)", fr.LastPassing, wantDay)
			}
		}
	}
}

// ===== AC-4 / P0-3: the drift signal is DB-backed — a fresh Store sees the
// snapshot a prior Store captured (persists across "restart") =====

func TestCaptureSnapshot_PersistsAcrossStoreInstances(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	app := openPool(t, appDSN(t))
	defer app.Close()
	tenant := freshTenant(t, admin)
	ctx := ctxFor(t, tenant)

	now := time.Now().UTC()
	ctrlID := seedControl(t, admin, tenant)
	seedEvaluation(t, admin, tenant, ctrlID, "pass", "fresh", now.Add(-1*time.Hour))

	// Store instance #1 captures a snapshot, then is discarded.
	store1 := drift.NewStore(app)
	if _, err := store1.CaptureSnapshot(ctx, drift.TriggerScheduled); err != nil {
		t.Fatalf("CaptureSnapshot (store1): %v", err)
	}

	// A brand-new Store instance — simulating a process restart — must still
	// see the snapshot. The signal lives in Postgres, never in memory.
	store2 := drift.NewStore(app)
	report, err := store2.Report(ctx, 7*24*time.Hour)
	if err != nil {
		t.Fatalf("Report (store2): %v", err)
	}
	if len(report.Snapshots) == 0 {
		t.Fatal("a fresh Store saw zero snapshots — the drift signal did not persist across instances")
	}
	if report.Snapshots[len(report.Snapshots)-1].ControlsPassing != 1 {
		t.Errorf("persisted snapshot ControlsPassing = %d, want 1",
			report.Snapshots[len(report.Snapshots)-1].ControlsPassing)
	}
}

// ===== cross-tenant RLS isolation: tenant A's drift Store never reads
// tenant B's snapshots =====

func TestReport_CrossTenantIsolation(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	app := openPool(t, appDSN(t))
	defer app.Close()
	tenantA := freshTenant(t, admin)
	tenantB := freshTenant(t, admin)

	now := time.Now().UTC()
	ctrlB := seedControl(t, admin, tenantB)
	// Tenant B has a snapshot with a known passing count.
	seedSnapshot(t, admin, tenantB, dayOf(now), []uuid.UUID{ctrlB})

	store := drift.NewStore(app)
	// Tenant A reads — must see ZERO snapshots (B's are RLS-invisible).
	ctxA := ctxFor(t, tenantA)
	reportA, err := store.Report(ctxA, 7*24*time.Hour)
	if err != nil {
		t.Fatalf("Report A: %v", err)
	}
	if len(reportA.Snapshots) != 0 {
		t.Errorf("tenant A's drift Report saw %d snapshots, want 0 — RLS isolation breach", len(reportA.Snapshots))
	}

	// Tenant B reads its own — must see exactly its snapshot.
	ctxB := ctxFor(t, tenantB)
	reportB, err := store.Report(ctxB, 7*24*time.Hour)
	if err != nil {
		t.Fatalf("Report B: %v", err)
	}
	if len(reportB.Snapshots) != 1 {
		t.Errorf("tenant B's drift Report saw %d snapshots, want 1 (its own)", len(reportB.Snapshots))
	}
}
