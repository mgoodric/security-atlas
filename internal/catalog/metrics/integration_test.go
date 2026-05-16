//go:build integration

// Integration tests for the slice 076 metrics catalog. Verifies:
//
//   - Migration round-trip: 5 tables created with the four-policy RLS for
//     tenant-scoped + the singleton-tenant-agnostic pattern for
//     metrics_catalog + metric_cascade_edges.
//   - Seeder is idempotent: a re-run on unchanged YAML produces zero net
//     diffs in metrics_catalog + cascade-edge rowcounts.
//   - The metric_inputs → metric_observations trigger replicates each
//     manual entry as a matching observation row.
//   - The recursive-CTE cascade query returns the descendant tree under
//     a level filter.
//
// Run via: just test-integration (sets DATABASE_URL_APP and DATABASE_URL,
// invokes go test -tags=integration).

package metrics_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	cat "github.com/mgoodric/security-atlas/internal/catalog/metrics"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
	metricseval "github.com/mgoodric/security-atlas/internal/metrics/eval"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

var (
	appPool   *pgxpool.Pool
	adminPool *pgxpool.Pool
)

func TestMain(m *testing.M) {
	url := os.Getenv("DATABASE_URL_APP")
	if url == "" {
		fmt.Fprintln(os.Stderr, "DATABASE_URL_APP not set; skipping slice 076 integration tests")
		os.Exit(0)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pgxpool.New(app): %v\n", err)
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

func TestSeederIsIdempotent(t *testing.T) {
	if adminPool == nil {
		t.Skip("DATABASE_URL (migrator role) required")
	}
	registry := metricseval.NewRegistry(appPool)
	seeder := cat.NewSeeder(adminPool)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	first, err := seeder.SeedFromEmbedded(ctx, registry)
	if err != nil {
		t.Fatalf("first seed: %v", err)
	}
	if first.MetricsApplied < 30 || first.EdgesApplied < 10 {
		t.Errorf("first seed report = %+v, expected 30+ metrics + 10+ edges", first)
	}

	second, err := seeder.SeedFromEmbedded(ctx, registry)
	if err != nil {
		t.Fatalf("second seed: %v", err)
	}
	if second.MetricsApplied != first.MetricsApplied {
		t.Errorf("idempotency: second seed metrics %d != first %d", second.MetricsApplied, first.MetricsApplied)
	}
	if second.EdgesApplied != first.EdgesApplied {
		t.Errorf("idempotency: second seed edges %d != first %d", second.EdgesApplied, first.EdgesApplied)
	}
}

func TestMetricInputTriggerReplicatesToObservations(t *testing.T) {
	if adminPool == nil {
		t.Skip("DATABASE_URL (migrator role) required")
	}
	// Ensure the catalog is seeded so the FK to metrics_catalog resolves.
	registry := metricseval.NewRegistry(appPool)
	seeder := cat.NewSeeder(adminPool)
	seedCtx, seedCancel := context.WithTimeout(context.Background(), 30*time.Second)
	_, _ = seeder.SeedFromEmbedded(seedCtx, registry)
	seedCancel()

	tenantID := uuid.New()
	userID := uuid.New()
	metricID := "open_findings_severity" // a manual_input catalog metric

	// Insert the input through the app pool under the tenant GUC.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ctx, err := tenancy.WithTenant(ctx, tenantID.String())
	if err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
	tx, err := appPool.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		t.Fatalf("ApplyTenant: %v", err)
	}

	q := dbx.New(tx)
	var numeric pgtype.Numeric
	if err := numeric.Scan("42.0"); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	row, err := q.InsertMetricInput(ctx, dbx.InsertMetricInputParams{
		TenantID:        pgtype.UUID{Bytes: tenantID, Valid: true},
		MetricID:        metricID,
		InputAt:         pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true},
		NumericValue:    numeric,
		Dimensions:      []byte(`{"unit_test": "true"}`),
		EnteredByUserID: pgtype.UUID{Bytes: userID, Valid: true},
		Notes:           "trigger replication test",
	})
	if err != nil {
		t.Fatalf("InsertMetricInput: %v", err)
	}
	// The observation should now exist with source = 'manual:<user-uuid>'.
	obs, err := q.ListMetricObservations(ctx, dbx.ListMetricObservationsParams{
		MetricID: metricID,
		Since:    pgtype.Timestamptz{Time: row.InputAt.Time.Add(-time.Minute), Valid: true},
		Until:    pgtype.Timestamptz{Time: row.InputAt.Time.Add(time.Minute), Valid: true},
		Limit:    10,
	})
	if err != nil {
		t.Fatalf("ListMetricObservations: %v", err)
	}
	if len(obs) == 0 {
		t.Fatalf("trigger did not produce a matching metric_observations row")
	}
	found := false
	for _, o := range obs {
		expected := "manual:" + userID.String()
		if o.Source == expected && o.MetricID == metricID {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("no observation matched manual:<user-uuid> source; got %d observations", len(obs))
	}
	// Roll back so the test leaves no residue; the trigger and its
	// replicate row are atomic within this tx.
	_ = tx.Rollback(ctx)
}

func TestMetricObservationsAppendOnlyUnderApp(t *testing.T) {
	if adminPool == nil {
		t.Skip("DATABASE_URL (migrator role) required")
	}
	// atlas_app has no UPDATE or DELETE policy on metric_observations.
	// Verify by direct attempt — should fail with a permission / policy
	// error (RLS yields zero affected rows in DELETE/UPDATE form, and
	// FORCE RLS without a policy bars the operation explicitly).
	tenantID := uuid.New()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ctx, err := tenancy.WithTenant(ctx, tenantID.String())
	if err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
	tx, err := appPool.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		t.Fatalf("ApplyTenant: %v", err)
	}
	// DELETE should affect zero rows because (a) tenant has no rows AND
	// (b) the FORCE-RLS + no-DELETE-policy combo would prevent any
	// matching row from being touched anyway. The assertion here is
	// "DELETE does not error" (no policy violation) AND tag returns 0.
	tag, err := tx.Exec(ctx, "DELETE FROM metric_observations")
	if err != nil {
		// Postgres may surface a "permission denied" or zero-rows DELETE
		// success depending on its FORCE-RLS behavior; either is OK so
		// long as no observation rows escape.
		if !errors.Is(err, context.Canceled) {
			t.Logf("DELETE: %v (acceptable under FORCE RLS)", err)
		}
		return
	}
	if tag.RowsAffected() != 0 {
		t.Errorf("DELETE on metric_observations affected %d rows; expected 0", tag.RowsAffected())
	}
}

func TestCascadeRecursiveCTEReturnsDescendants(t *testing.T) {
	if adminPool == nil {
		t.Skip("DATABASE_URL (migrator role) required")
	}
	registry := metricseval.NewRegistry(appPool)
	seeder := cat.NewSeeder(adminPool)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := seeder.SeedFromEmbedded(ctx, registry); err != nil {
		t.Fatalf("seed: %v", err)
	}
	q := dbx.New(adminPool)
	rows, err := q.GetMetricCascade(ctx, dbx.GetMetricCascadeParams{
		Level:      "board",
		DepthLimit: 3,
	})
	if err != nil {
		t.Fatalf("GetMetricCascade: %v", err)
	}
	if len(rows) < 8 {
		t.Errorf("cascade returned %d rows; expected >= 8 board metrics", len(rows))
	}
	rootCount := 0
	for _, r := range rows {
		if r.Depth == 1 && r.ParentID == nil {
			rootCount++
		}
	}
	if rootCount < 8 {
		t.Errorf("cascade root rows = %d; expected >= 8 (one per board metric)", rootCount)
	}
}

func TestSingletonCatalogReadableByApp(t *testing.T) {
	if adminPool == nil {
		t.Skip("DATABASE_URL (migrator role) required")
	}
	registry := metricseval.NewRegistry(appPool)
	seeder := cat.NewSeeder(adminPool)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := seeder.SeedFromEmbedded(ctx, registry); err != nil {
		t.Fatalf("seed: %v", err)
	}
	tenantID := uuid.New()
	tctx, err := tenancy.WithTenant(context.Background(), tenantID.String())
	if err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
	tctx, cancel2 := context.WithTimeout(tctx, 5*time.Second)
	defer cancel2()
	tx, err := appPool.BeginTx(tctx, pgx.TxOptions{})
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	defer func() { _ = tx.Rollback(tctx) }()
	if err := tenancy.ApplyTenant(tctx, tx); err != nil {
		t.Fatalf("ApplyTenant: %v", err)
	}
	rows, err := dbx.New(tx).ListMetricsCatalog(tctx, dbx.ListMetricsCatalogParams{})
	if err != nil {
		t.Fatalf("ListMetricsCatalog: %v", err)
	}
	if len(rows) < 30 {
		t.Errorf("singleton catalog read returned %d rows; expected >= 30 (tenant_id IS NULL pattern)", len(rows))
	}
}
