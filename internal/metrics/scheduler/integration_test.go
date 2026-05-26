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
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	metricseval "github.com/mgoodric/security-atlas/internal/metrics/eval"
	"github.com/mgoodric/security-atlas/internal/metrics/scheduler"
)

// ----- harness -----

func appDSN(t *testing.T) string {
	t.Helper()
	v := os.Getenv("DATABASE_URL_APP")
	if v == "" {
		t.Skip("DATABASE_URL_APP not set; skipping slice 295 integration test")
	}
	return v
}

func adminDSN(t *testing.T) string {
	t.Helper()
	v := os.Getenv("DATABASE_URL")
	if v == "" {
		t.Skip("DATABASE_URL not set; skipping slice 295 integration test")
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
	t.Cleanup(func() { pool.Close() })
	return pool
}

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
// freshnessdrift integration harness.
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
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))

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
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
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

// ===== Run-loop integration: a tight Run + context-cancel run against
// a real DB exits cleanly and produces observations from the inline
// pre-tick sweep. Exercises the Run() path that the SweepOnce-only
// tests above do not (Run wraps SweepOnce in a goroutine-friendly
// error-swallowing wrapper). =====

func TestRun_FiresInlineSweepAndExitsOnCancel(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	seedCatalog(t, admin, app)
	tenant := freshTenant(t, admin)
	seedAnchorControl(t, admin, tenant)

	registry := metricseval.NewRegistry(app)

	s := scheduler.New(admin, app, registry, nil)

	// Long interval; the inline sweep is the one tick we exercise.
	// 500ms is plenty for the SweepOnce write path.
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- s.Run(ctx, 5*time.Second)
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run returned err=%v; want nil", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return within 10s of context cancellation")
	}

	if got := countObservations(t, admin, tenant); got < 1 {
		t.Errorf("post-Run observations = %d; want >= 1 (the inline sweep should have written at least one)", got)
	}
}
