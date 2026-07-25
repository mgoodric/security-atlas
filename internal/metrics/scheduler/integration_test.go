//go:build integration

// Integration tests for the metrics scheduler (slice 076). The DB-touching
// paths in worker.go — SweepOnce's success branches, sweepTenant's full
// transaction lifecycle, ApplyTenant, the per-evaluator loop, and the
// InsertMetricObservation write — only have meaningful semantics against a
// real Postgres with RLS enforced. The unit tests in worker_test.go cover
// the pure-Go branches (New, Run cancellation, encodeDimensions,
// discardWriter, SweepReport, and the list-tenants error wrap).
//
// Load-bearing functions exercised here:
//
//   - SweepOnce — full success path: list tenants → per-tenant sweep →
//     write N observations → aggregate the report
//   - sweepTenant — Begin/ApplyTenant/Compute-loop/Insert/Commit lifecycle
//     against the real app role (RLS-enforced), plus the
//     evaluator-failure-recovery branch when an evaluator's read returns a
//     real error
//
// Required env (matches the pattern from internal/freshnessdrift and
// internal/catalog/metrics):
//
//   DATABASE_URL      — migration role DSN (BYPASSRLS); seeds fixtures
//                       AND the scheduler enumerates tenants through it.
//   DATABASE_URL_APP  — application role DSN (NOSUPERUSER NOBYPASSRLS);
//                       sweepTenant's transaction runs through it so RLS
//                       is enforced.
//
// Run via: go test -tags=integration -race ./internal/metrics/scheduler/...

package scheduler_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/dbtest"
	metricseval "github.com/mgoodric/security-atlas/internal/metrics/eval"
	"github.com/mgoodric/security-atlas/internal/metrics/scheduler"
)

// ----- harness -----

// seedCatalog ensures every starter evaluator's metric_id row exists in
// metrics_catalog so the InsertMetricObservation FK resolves. Uses a
// direct INSERT (idempotent via ON CONFLICT DO NOTHING) over the
// registry's evaluator names so this slice's test surface is decoupled
// from the embedded-YAML seeder. The seeder ships its own integration
// coverage in internal/catalog/metrics — we exercise the scheduler in
// isolation.
func seedCatalog(t *testing.T, admin *pgxpool.Pool, app *pgxpool.Pool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	registry := metricseval.NewRegistry(app)
	for _, name := range registry.Names() {
		if _, err := admin.Exec(ctx, `
			INSERT INTO metrics_catalog (
				id, tenant_id, level, category, name, description, unit,
				cadence, compute_strategy, compute_evaluator,
				source_slices, notes
			) VALUES (
				$1, NULL, 'program', 'test', $1, 'slice 295 fixture',
				'percent', 'daily', 'computed', $1,
				ARRAY[]::TEXT[], ''
			)
			ON CONFLICT (id) DO NOTHING
		`, name); err != nil {
			t.Fatalf("seed catalog row %q: %v", name, err)
		}
	}
}

// freshTenant returns a brand-new tenant UUID and registers a cleanup
// that drops every row this slice's tests can introduce. Mirrors the
// freshnessdrift integration harness. Carve-out from the slice-435 dbtest
// harness: this suite's seeders key off a uuid.UUID tenant (not the string
// dbtest.SeedTenant returns), so the helper stays inline; only its pool is
// re-routed to dbtest.NewMigratePool at the call sites (742 drain batch 17).
func freshTenant(t *testing.T, admin *pgxpool.Pool) uuid.UUID {
	t.Helper()
	tenant := uuid.New()
	t.Cleanup(func() {
		ctx := context.Background()
		for _, stmt := range []string{
			`DELETE FROM metric_observations WHERE tenant_id = $1`,
			`DELETE FROM control_evaluations  WHERE tenant_id = $1`,
			`DELETE FROM evidence_records     WHERE tenant_id = $1`,
			`DELETE FROM controls             WHERE tenant_id = $1`,
		} {
			if _, err := admin.Exec(ctx, stmt, tenant); err != nil {
				t.Logf("cleanup %s: %v", stmt, err)
			}
		}
	})
	return tenant
}

// seedAnchorControl inserts ONE control owned by the supplied tenant so
// the tenant appears in the ListTenantsForMetricsScheduler UNION. The
// concrete column values mirror the freshnessdrift integration harness
// (slice 016).
func seedAnchorControl(t *testing.T, admin *pgxpool.Pool, tenant uuid.UUID) uuid.UUID {
	t.Helper()
	ctrlID := uuid.New()
	bundleID := "test-bundle-295-" + ctrlID.String()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO controls (
			id, tenant_id, title, control_family, implementation_type,
			bundle_id, evidence_queries, applicability_expr
		)
		VALUES ($1, $2, 'slice 295 metrics scheduler test control', 'AAA', 'manual_attested',
		        $3, '[]'::jsonb, 'true')
	`, ctrlID, tenant, bundleID); err != nil {
		t.Fatalf("seed control: %v", err)
	}
	return ctrlID
}

func countObservations(t *testing.T, admin *pgxpool.Pool, tenant uuid.UUID) int {
	t.Helper()
	var n int
	if err := admin.QueryRow(context.Background(),
		`SELECT count(*) FROM metric_observations WHERE tenant_id = $1`, tenant).Scan(&n); err != nil {
		t.Fatalf("count observations: %v", err)
	}
	return n
}

// ===== AC-1 + AC-2: SweepOnce iterates registered evaluators for every
// tenant that has any primitive presence. With a minimally-seeded tenant
// (one control + no other primitives), most evaluators succeed (empty
// result sets are not an error — they yield Value=0) and write one
// observation each. Evaluators whose queries touch tables that hold no
// rows for this tenant may legitimately fail their pre-conditions and
// hit the per-evaluator failure-recovery branch (rep.EvaluatorFailures
// counts them; the sweep does NOT abort — slice doc AC-13). We assert
// the load-bearing invariants: tenant was swept, AT LEAST ONE
// observation landed, and successes + failures sum to the registered
// evaluator count.
// =====

func TestSweepOnce_WritesObservationsForOurTenant(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)

	seedCatalog(t, admin, app)

	tenant := freshTenant(t, admin)
	seedAnchorControl(t, admin, tenant) // tenant now appears in the UNION

	if n := countObservations(t, admin, tenant); n != 0 {
		t.Fatalf("pre-sweep observations = %d, want 0", n)
	}

	registry := metricseval.NewRegistry(app)
	want := len(registry.Names())
	if want < 1 {
		t.Fatalf("registry returned %d evaluators; expected at least 1", want)
	}

	s := scheduler.New(admin, app, registry, nil)
	rep, err := s.SweepOnce(context.Background())
	if err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	if rep.TenantsSwept < 1 {
		t.Errorf("rep.TenantsSwept = %d; want >= 1 (our tenant is in the UNION)", rep.TenantsSwept)
	}

	// Per-tenant successes + failures must sum to the registered
	// evaluator count — this is the slice-076 AC-13 contract that
	// per-evaluator try/log/continue catches every evaluator failure
	// without abandoning the run.
	if rep.ObservationsWritten+rep.EvaluatorFailures < want {
		t.Errorf("rep.ObservationsWritten (%d) + rep.EvaluatorFailures (%d) = %d; want >= %d (one row per registered evaluator for our tenant)",
			rep.ObservationsWritten, rep.EvaluatorFailures, rep.ObservationsWritten+rep.EvaluatorFailures, want)
	}
	if got := countObservations(t, admin, tenant); got < 1 {
		t.Errorf("post-sweep observations for tenant = %d; want >= 1 (at least one evaluator should succeed against a minimally-seeded tenant)", got)
	}
	if got := countObservations(t, admin, tenant); got > want {
		t.Errorf("post-sweep observations for tenant = %d; want <= %d (one per registered evaluator)", got, want)
	}
}

// ===== AC-1 + AC-2: Two sweeps APPEND — metric_observations is an
// append-only ledger (slice 076 schema). The second sweep writes another
// batch for our tenant; the post-state count must be exactly double the
// post-one-sweep count. =====

func TestSweepOnce_AppendsOnRepeatedRuns(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	seedCatalog(t, admin, app)
	tenant := freshTenant(t, admin)
	seedAnchorControl(t, admin, tenant)

	registry := metricseval.NewRegistry(app)
	s := scheduler.New(admin, app, registry, nil)

	if _, err := s.SweepOnce(context.Background()); err != nil {
		t.Fatalf("SweepOnce #1: %v", err)
	}
	after1 := countObservations(t, admin, tenant)
	if after1 < 1 {
		t.Fatalf("after first sweep: observations = %d; want >= 1", after1)
	}

	if _, err := s.SweepOnce(context.Background()); err != nil {
		t.Fatalf("SweepOnce #2: %v", err)
	}
	after2 := countObservations(t, admin, tenant)
	// The metric_observations table has SELECT + INSERT policies only
	// (slice 076), so the second sweep is a clean append, not an
	// upsert. Each evaluator that succeeded on sweep #1 should succeed
	// again on sweep #2.
	if after2 != 2*after1 {
		t.Errorf("post-two-sweep observations = %d; want %d (append-only doubling)", after2, 2*after1)
	}
}

// ===== Run-loop integration: Run fires one inline pre-tick sweep, then
// exits cleanly when its context is cancelled. Exercises the Run() path
// that the SweepOnce-only tests above do not (Run wraps SweepOnce in a
// goroutine-friendly error-swallowing wrapper).
//
// Flake history (fixed here): the prior version handed Run a 500ms-deadline
// context and then asserted on the DB row count. That single context had to
// cover the ENTIRE list-tenants -> per-tenant BEGIN -> N-evaluator ->
// INSERT -> COMMIT cycle; under the serialized `-p 1 -race` integration job
// that work routinely exceeds 500ms. When the deadline fired mid-sweep the
// COMMIT returned context.Canceled, the sweep wrapper swallowed it, zero
// rows landed, and the >=1 assertion failed (~10+ reruns across sessions).
//
// The fix decouples inline-sweep COMPLETION from the cancel DEADLINE:
//
//   - Run's context is NOT on a wall-clock; the inline sweep runs to
//     completion against real DB latency.
//   - We assert on the inline sweep's RETURNED SweepReport (surfaced via
//     the SetInlineSweepHookForTest hook) rather than racing a timeout
//     against DB latency. ObservationsWritten >= 1 is the deterministic
//     ground truth.
//   - Only AFTER the inline sweep reports do we cancel the context, which
//     proves the ticker loop exits cleanly on cancel.
//
// This removes the race rather than widening the window (cf. slice 381:
// no single-sample wall-clock asserts).
// =====

// runInlineSweepOnce drives one Run() lifecycle to completion: it registers
// the inline-sweep hook, starts Run with a cancellable (non-wall-clock)
// context, waits for the inline sweep to report, then cancels and waits for
// Run to return nil. It returns the inline sweep's report. Factored out so
// the stress test can replay it N times.
func runInlineSweepOnce(t *testing.T, admin, app *pgxpool.Pool, registry *metricseval.Registry, tenant uuid.UUID) scheduler.SweepReport {
	t.Helper()

	s := scheduler.New(admin, app, registry, nil)

	// Buffered so the hook never blocks Run even if we are slow to receive.
	inline := make(chan struct {
		rep scheduler.SweepReport
		err error
	}, 1)
	s.SetInlineSweepHookForTest(func(rep scheduler.SweepReport, err error) {
		inline <- struct {
			rep scheduler.SweepReport
			err error
		}{rep, err}
	})

	// Run's context is cancellable but NOT on a 500ms wall-clock: the inline
	// sweep gets as long as the DB legitimately needs. Long ticker interval
	// so the only sweep we exercise is the inline one.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- s.Run(ctx, 1*time.Hour)
	}()

	// Wait for the inline sweep to COMPLETE and report. Generous ceiling
	// guards against a genuine hang (not a flake knob — the inline sweep is
	// expected to finish in well under a second; the ceiling only catches a
	// deadlock).
	var got scheduler.SweepReport
	select {
	case res := <-inline:
		if res.err != nil {
			t.Fatalf("inline sweep returned err=%v; want nil", res.err)
		}
		got = res.rep
	case <-time.After(30 * time.Second):
		t.Fatal("inline sweep did not report within 30s (hang, not a deadline race)")
	}

	// The inline sweep finished cleanly; NOW cancel to prove the ticker loop
	// exits gracefully on cancel.
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run returned err=%v; want nil on graceful cancel", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return within 10s of context cancellation")
	}

	return got
}

func TestRun_FiresInlineSweepAndExitsOnCancel(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	seedCatalog(t, admin, app)
	tenant := freshTenant(t, admin)
	seedAnchorControl(t, admin, tenant)

	registry := metricseval.NewRegistry(app)

	rep := runInlineSweepOnce(t, admin, app, registry, tenant)

	// Deterministic ground truth: the inline sweep's RETURNED report says it
	// wrote at least one observation. No wall-clock race.
	if rep.TenantsSwept < 1 {
		t.Errorf("inline sweep TenantsSwept = %d; want >= 1 (our tenant is in the UNION)", rep.TenantsSwept)
	}
	if rep.ObservationsWritten < 1 {
		t.Errorf("inline sweep ObservationsWritten = %d; want >= 1 (at least one evaluator should succeed against a minimally-seeded tenant)", rep.ObservationsWritten)
	}

	// The rows are now guaranteed durable because the sweep committed before
	// the hook fired and before we cancelled — this tenant's count corroborates
	// the report. Corroboration, not the primary assertion, so no longer a race.
	// We assert THIS tenant got >= 1 row, NOT equality to rep.ObservationsWritten:
	// the report counts observations across ALL tenants in the sweep UNION
	// (rep.TenantsSwept may be > 1 in a shared CI database), so the global
	// report count is not comparable to this single tenant's row count.
	if got := countObservations(t, admin, tenant); got < 1 {
		t.Errorf("post-Run observations = %d; want >= 1 (committed inline sweep)", got)
	}
}

// TestRun_InlineSweepStress replays the full Run-inline-sweep-then-cancel
// lifecycle 5 times against the same fresh tenant to confirm the deadline
// race is gone (slice 340 chromedp 5x-stress precedent). Each iteration we
// assert the RETURNED report wrote >= 1 observation (the deterministic
// anti-race signal) AND that this tenant's append-only metric_observations
// row count (slice 076) STRICTLY GROWS. We do NOT compare to a running sum
// of rep.ObservationsWritten — that count is global across the sweep UNION
// (rep.TenantsSwept may be > 1 in a shared CI database) and is not comparable
// to this single tenant's row count. If the old race were present, an
// iteration whose COMMIT lost to a deadline would report 0 written, or this
// tenant's count would fail to grow.
func TestRun_InlineSweepStress(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	seedCatalog(t, admin, app)
	tenant := freshTenant(t, admin)
	seedAnchorControl(t, admin, tenant)

	registry := metricseval.NewRegistry(app)

	const iterations = 5
	prev := countObservations(t, admin, tenant)
	for i := 0; i < iterations; i++ {
		rep := runInlineSweepOnce(t, admin, app, registry, tenant)
		if rep.ObservationsWritten < 1 {
			t.Fatalf("iteration %d: ObservationsWritten = %d; want >= 1 (deadline race regressed)", i, rep.ObservationsWritten)
		}
		got := countObservations(t, admin, tenant)
		if got <= prev {
			t.Fatalf("iteration %d: this tenant's observation count = %d; want > %d (append-only growth)", i, got, prev)
		}
		prev = got
	}
}
