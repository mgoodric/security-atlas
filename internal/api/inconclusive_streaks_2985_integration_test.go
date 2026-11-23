//go:build integration

// #2985 — ListInconclusiveStreaks reads each cell's streak with index
// probes instead of a window over the full history. This holds it to the
// #2915 full-scan query it replaced: on a random ledger (ties, cells never
// broken, cells that recovered, the NULL whole-tenant cell), both return
// the same rows at every as-of.
//
// Requires DATABASE_URL_APP (atlas_app, RLS) and DATABASE_URL (migrate role).
package api_test

import (
	"context"
	"math/rand"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/dbtest"
)

// fullScanStreaks2985 is the #2915 query, kept here as the reference.
const fullScanStreaks2985 = `
SELECT h.control_id, h.scope_cell_id, min(h.evaluated_at)::timestamptz
FROM (
    SELECT ce.control_id, ce.scope_cell_id, ce.evaluated_at,
           max(ce.evaluated_at) FILTER (WHERE ce.result <> 'inconclusive')
               OVER (PARTITION BY ce.control_id, ce.scope_cell_id) AS last_break
    FROM control_evaluations ce
    WHERE ce.tenant_id = $1 AND ce.control_id = ANY($2::uuid[]) AND ce.evaluated_at <= $3
) h
WHERE h.last_break IS NULL OR h.evaluated_at > h.last_break
GROUP BY h.control_id, h.scope_cell_id`

func TestListInconclusiveStreaks_2985_MatchesFullScan(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	ctx := context.Background()
	tenant := dbtest.SeedTenant(t, admin, "control_evaluations", "controls", "scope_cells")
	now := time.Now().UTC().Truncate(time.Second)
	rng := rand.New(rand.NewSource(2985))

	exec := func(what, sql string, args ...any) {
		t.Helper()
		if _, err := admin.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("seed %s: %v", what, err)
		}
	}
	cells := []*uuid.UUID{nil}
	for i := 0; i < 3; i++ {
		id := uuid.New()
		exec("scope cell", `INSERT INTO scope_cells (id, tenant_id, label, dimensions, dimensions_hash) VALUES ($1, $2, 'cell 2985', jsonb_build_object('n', $3::int), $4)`,
			id, tenant, i, "hash-2985-"+id.String())
		cells = append(cells, &id)
	}
	var ids []pgtype.UUID
	for c := 0; c < 12; c++ {
		ctrl := uuid.New()
		exec("control", `
			INSERT INTO controls (
				id, tenant_id, title, control_family, implementation_type,
				bundle_id, evidence_queries, applicability_expr, lifecycle_state
			)
			VALUES ($1, $2, 'control 2985', 'AAA', 'automated', $3, '[]'::jsonb, 'true', 'active')
		`, ctrl, tenant, "test-bundle-2985-"+ctrl.String())
		ids = append(ids, pgtype.UUID{Bytes: ctrl, Valid: true})
		// Each control has evaluations for a random subset of the cells.
		// pInconclusive varies per cell so some cells are never broken and
		// some rarely inconclusive. Hours repeat, so rows tie on
		// evaluated_at.
		for _, cell := range cells {
			if rng.Intn(4) == 0 {
				continue
			}
			pInconclusive := []float64{1, 0.9, 0.5, 0.1}[rng.Intn(4)]
			batch := &pgx.Batch{}
			for i := 0; i < 40; i++ {
				result, freshness, count := "pass", "fresh", 1
				if rng.Float64() < pInconclusive {
					result, freshness, count = "inconclusive", "no_evidence", 0
				}
				at := now.Add(-time.Duration(rng.Intn(30)) * time.Hour)
				batch.Queue(`
					INSERT INTO control_evaluations (
						id, tenant_id, control_id, scope_cell_id, eval_run_id, evaluated_at,
						result, freshness_status, evidence_count_in_window, trigger
					)
					VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, 'scheduled')
				`, uuid.New(), tenant, ctrl, cell, uuid.New(), at, result, freshness, count)
			}
			if err := admin.SendBatch(ctx, batch).Close(); err != nil {
				t.Fatalf("seed evaluations: %v", err)
			}
		}
	}

	type key struct{ control, cell uuid.UUID }
	tid := pgtype.UUID{Bytes: uuid.MustParse(tenant), Valid: true}
	read := func(asOf time.Time) (got, want map[key]time.Time) {
		t.Helper()
		tx, err := app.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback(ctx) }()
		if _, err := tx.Exec(ctx, "SELECT set_config('app.current_tenant', $1, true)", tenant); err != nil {
			t.Fatal(err)
		}
		asOfTs := pgtype.Timestamptz{Time: asOf, Valid: true}
		rows, err := dbx.New(tx).ListInconclusiveStreaks(ctx, dbx.ListInconclusiveStreaksParams{
			TenantID: tid, ControlIds: ids, AsOf: asOfTs,
		})
		if err != nil {
			t.Fatalf("ListInconclusiveStreaks: %v", err)
		}
		got = map[key]time.Time{}
		for _, r := range rows {
			k := key{control: r.ControlID.Bytes}
			if r.ScopeCellID.Valid {
				k.cell = r.ScopeCellID.Bytes
			}
			if _, dup := got[k]; dup {
				t.Fatalf("as of %v: duplicate row for %v", asOf, k)
			}
			got[k] = r.InconclusiveSince.Time
		}
		ref, err := tx.Query(ctx, fullScanStreaks2985, tid, ids, asOfTs)
		if err != nil {
			t.Fatalf("full scan: %v", err)
		}
		defer ref.Close()
		want = map[key]time.Time{}
		for ref.Next() {
			var ctrl, cell pgtype.UUID
			var since pgtype.Timestamptz
			if err := ref.Scan(&ctrl, &cell, &since); err != nil {
				t.Fatal(err)
			}
			k := key{control: ctrl.Bytes}
			if cell.Valid {
				k.cell = cell.Bytes
			}
			want[k] = since.Time
		}
		if err := ref.Err(); err != nil {
			t.Fatal(err)
		}
		return got, want
	}

	total := 0
	for _, back := range []int{0, 3, 10, 20, 29, 40} {
		asOf := now.Add(-time.Duration(back) * time.Hour)
		got, want := read(asOf)
		total += len(want)
		if len(got) != len(want) {
			t.Errorf("as of -%dh: %d streaks; the full scan finds %d", back, len(got), len(want))
		}
		for k, w := range want {
			if g, ok := got[k]; !ok || !g.Equal(w) {
				t.Errorf("as of -%dh, %v: since = %v (found %v); full scan says %v", back, k, g, ok, w)
			}
		}
	}
	// The fixture must exercise something: a seed that produced no
	// streaks at all would pass vacuously.
	if total == 0 {
		t.Fatal("no streaks in the fixture; the comparison proves nothing")
	}
}
