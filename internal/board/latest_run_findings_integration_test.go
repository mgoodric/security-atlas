//go:build integration

// #2688: the board pack's open findings come only from each control's
// latest evaluation run as of the pack's horizon, the bound #2526 put on
// the OSCAL POA&M. A cell that was failing in an earlier run and no longer
// applies (it has no row in the latest run) is not an open finding.
//
// Run with: go test -tags=integration -p 1 ./internal/board/...

package board_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/board"
	"github.com/mgoodric/security-atlas/internal/dbtest"
)

func TestPackStore_ListFailingEvaluations_BoundsToControlsLatestRun(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	ctx := context.Background()
	// Cleanups run last-in first-out, so this one runs before freshTenant's:
	// drop the evaluations that reference the scope cells first.
	t.Cleanup(func() {
		for _, stmt := range []string{
			`DELETE FROM control_evaluations WHERE tenant_id = $1`,
			`DELETE FROM scope_cells WHERE tenant_id = $1`,
		} {
			if _, err := admin.Exec(context.Background(), stmt, tenant); err != nil {
				t.Logf("cleanup %s: %v", stmt, err)
			}
		}
	})

	periodEnd := time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC)
	ctrl := seedControl(t, admin, tenant)
	cells := make([]uuid.UUID, 2)
	for i, label := range []string{"a", "b"} {
		cells[i] = uuid.New()
		if _, err := admin.Exec(ctx,
			`INSERT INTO scope_cells (id, tenant_id, label, dimensions, dimensions_hash)
			 VALUES ($1, $2, $3, jsonb_build_object('env', $3::text), $4)`,
			cells[i], tenant, label, "h-2688-"+label+"-"+tenant); err != nil {
			t.Fatalf("seed scope cell %s: %v", label, err)
		}
	}
	cellA, cellB := cells[0], cells[1]

	// Run 1 fails on cells A and B; run 2 covers only cell B, so cell A
	// stopped applying. Run 1's cell-A row is the stale one.
	run1, run2 := uuid.New(), uuid.New()
	stale, current := uuid.New(), uuid.New()
	for _, r := range []struct {
		id, cell, run uuid.UUID
		at            time.Time
	}{
		{stale, cellA, run1, periodEnd.Add(-48 * time.Hour)},
		{uuid.New(), cellB, run1, periodEnd.Add(-48 * time.Hour)},
		{current, cellB, run2, periodEnd.Add(-24 * time.Hour)},
	} {
		if _, err := admin.Exec(ctx, `
			INSERT INTO control_evaluations
			  (id, tenant_id, control_id, scope_cell_id, eval_run_id, evaluated_at,
			   result, freshness_status, evidence_count_in_window, trigger)
			VALUES ($1, $2, $3, $4, $5, $6, 'fail', 'stale', 0, 'manual')
		`, r.id, tenant, ctrl, r.cell, r.run, r.at); err != nil {
			t.Fatalf("seed failing evaluation: %v", err)
		}
	}
	// A run after the horizon must not move the bound: a later run that
	// covers only cell A is not the latest run as of period_end.
	if _, err := admin.Exec(ctx, `
		INSERT INTO control_evaluations
		  (id, tenant_id, control_id, scope_cell_id, eval_run_id, evaluated_at,
		   result, freshness_status, evidence_count_in_window, trigger)
		VALUES ($1, $2, $3, $4, $5, $6, 'fail', 'stale', 0, 'manual')
	`, uuid.New(), tenant, ctrl, cellA, uuid.New(), periodEnd.Add(48*time.Hour)); err != nil {
		t.Fatalf("seed post-horizon evaluation: %v", err)
	}

	findings, err := board.NewPackStore(app).ListFailingEvaluations(ctxFor(t, tenant), periodEnd)
	if err != nil {
		t.Fatalf("ListFailingEvaluations: %v", err)
	}
	if len(findings) != 1 || findings[0].EvaluationID != current.String() {
		got := make([]string, 0, len(findings))
		for _, f := range findings {
			got = append(got, f.EvaluationID+"@"+f.ScopeCellID)
		}
		t.Fatalf("findings = %v, want only the latest run's cell-B row %s (stale cell-A row is %s)",
			got, current, stale)
	}
}
