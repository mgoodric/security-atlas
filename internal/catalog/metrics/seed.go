package metrics

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	metricscatalog "github.com/mgoodric/security-atlas/catalogs/metrics"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// Seeder applies the parsed Catalog into the metrics_catalog +
// metric_cascade_edges tables. Runs as the migrator role (BYPASSRLS) at
// boot so the singleton-tenant-agnostic NULL-tenant_id rows are
// permitted.
//
// Idempotent: re-running on an unchanged catalog produces zero net diffs
// (every UPSERT path either matches the existing row content or is a
// no-op write that triggers ON CONFLICT DO UPDATE). The cascade-edges
// path explicitly DELETEs every existing edge before re-inserting so a
// removed YAML edge is reflected in the DB — the catalog is the source
// of truth and DB drift from removed edges must not survive a boot.
type Seeder struct {
	pool *pgxpool.Pool
}

// NewSeeder constructs a Seeder over the migrator pool.
func NewSeeder(pool *pgxpool.Pool) *Seeder {
	return &Seeder{pool: pool}
}

// SeedFromEmbedded loads the embedded metrics catalog and applies it to
// the DB. Convenience wrapper used by cmd/atlas at boot.
func (s *Seeder) SeedFromEmbedded(ctx context.Context, evaluators EvaluatorRegistry) (Report, error) {
	c, err := Load(metricscatalog.EmbeddedFS(), evaluators)
	if err != nil {
		return Report{}, fmt.Errorf("catalog/metrics: load embedded: %w", err)
	}
	return s.Apply(ctx, c)
}

// Apply writes catalog rows + cascade edges atomically (one transaction).
// Returns a Report counting metrics + edges applied.
func (s *Seeder) Apply(ctx context.Context, c *Catalog) (Report, error) {
	if s.pool == nil {
		return Report{}, fmt.Errorf("catalog/metrics: seeder has nil pool")
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Report{}, fmt.Errorf("catalog/metrics: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := dbx.New(tx)
	report := Report{}

	for _, m := range c.Metrics {
		params := dbx.UpsertMetricCatalogGlobalParams{
			ID:              m.ID,
			Level:           string(m.Level),
			Category:        m.Category,
			Name:            m.Name,
			Description:     m.Description,
			Unit:            m.Unit,
			Cadence:         string(m.Cadence),
			ComputeStrategy: string(m.ComputeStrategy),
			SourceSlices:    append([]string(nil), m.SourceSlices...),
			Notes:           m.Notes,
		}
		// sqlc emits pointer types for nullable columns under
		// emit_pointers_for_null_types: true. compute_evaluator is
		// modeled as *string. Only computed metrics set it.
		if m.ComputeEvaluator != "" {
			ev := m.ComputeEvaluator
			params.ComputeEvaluator = &ev
		}
		if err := q.UpsertMetricCatalogGlobal(ctx, params); err != nil {
			return Report{}, fmt.Errorf("catalog/metrics: upsert %s: %w", m.ID, err)
		}
		report.MetricsApplied++
	}

	if err := q.DeleteAllMetricCascadeEdges(ctx); err != nil {
		return Report{}, fmt.Errorf("catalog/metrics: clear cascade edges: %w", err)
	}
	for _, e := range c.Edges {
		var weight pgtype.Numeric
		if err := weight.Scan(formatWeight(e.Weight)); err != nil {
			return Report{}, fmt.Errorf("catalog/metrics: weight encode %v: %w", e.Weight, err)
		}
		if err := q.UpsertMetricCascadeEdge(ctx, dbx.UpsertMetricCascadeEdgeParams{
			ParentID: e.ParentID,
			ChildID:  e.ChildID,
			Weight:   weight,
			Notes:    e.Notes,
		}); err != nil {
			return Report{}, fmt.Errorf("catalog/metrics: upsert edge %s -> %s: %w", e.ParentID, e.ChildID, err)
		}
		report.EdgesApplied++
	}

	if err := tx.Commit(ctx); err != nil {
		return Report{}, fmt.Errorf("catalog/metrics: commit: %w", err)
	}
	return report, nil
}

// Report summarizes one seed run. Surfaced to logs on boot.
type Report struct {
	MetricsApplied int
	EdgesApplied   int
}

// formatWeight produces a string the pgtype.Numeric Scan can consume.
// The DB CHECK enforces (0, 1] so we always have a small positive value.
func formatWeight(w float64) string {
	// Numeric(5,4) — 4 fractional digits max. Use fmt rather than
	// strconv.FormatFloat to keep trailing zeros predictable.
	return fmt.Sprintf("%.4f", w)
}
