//go:build integration

// Integration tests for the slice 671 post-seed evaluation driver.
//
// Requires BOTH pools:
//
//	DATABASE_URL      - BYPASSRLS migrate-role DSN (the seed write path +
//	                    cross-tenant assertion reads). Already used by the
//	                    slice-205 harness (adminPool, TestMain).
//	DATABASE_URL_APP  - RLS-enforced app-role DSN. EvaluateSeededTenant runs
//	                    through this so RLS scopes evaluation to the seeded
//	                    tenant (invariant #6). Skipped when unset.
//
// Run with:
//
//	go test -tags=integration -p 1 ./internal/demoseed/...
//
// The load-bearing test (TestEvaluateSeededTenant_ProducesRealControlState)
// REPRODUCES the reported symptom: immediately after Apply, before evaluation
// runs, control_evaluations + evidence_freshness are EMPTY and a /controls-
// style read shows STATE = "—". It then runs EvaluateSeededTenant and asserts
// the tables populate and the controls show real pass/fail state. The
// pre-evaluation assertions are the "fails without the fix" half: they pin the
// exact symptom slice 671 fixes.

package demoseed_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/demoseed"
)

// appPoolOrSkip opens the RLS-enforced app-role pool, or skips when the DSN is
// unset (matches the eval/freshness integration harness convention).
func appPoolOrSkip(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL_APP")
	if dsn == "" {
		t.Skip("DATABASE_URL_APP not set; skipping slice-671 post-seed evaluation test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New app: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// controlEvalCount counts control_evaluations rows for the tenant (BYPASSRLS
// admin pool so the count is not RLS-scoped by an absent context).
func controlEvalCount(t *testing.T, tenantID uuid.UUID) int {
	t.Helper()
	var n int
	if err := adminPool.QueryRow(context.Background(),
		`SELECT count(*) FROM control_evaluations WHERE tenant_id = $1`, tenantID,
	).Scan(&n); err != nil {
		t.Fatalf("count control_evaluations: %v", err)
	}
	return n
}

// freshnessCount counts evidence_freshness rows for the tenant.
func freshnessCount(t *testing.T, tenantID uuid.UUID) int {
	t.Helper()
	var n int
	if err := adminPool.QueryRow(context.Background(),
		`SELECT count(*) FROM evidence_freshness WHERE tenant_id = $1`, tenantID,
	).Scan(&n); err != nil {
		t.Fatalf("count evidence_freshness: %v", err)
	}
	return n
}

// controlsWithInWindowEvidence counts the tenant's controls whose freshest
// evidence_records observation falls INSIDE the control's freshness window
// (per the eval.FreshnessMaxAge class→max-age mapping). Only these resolve to
// a real pass/fail; a control whose only evidence is older than its window is
// correctly inconclusive ("—") — faithful engine behavior, NOT the slice-671
// bug. The CASE mirrors eval.state.go's freshnessMaxAgeTable
// (realtime 24h · daily 7d · weekly 30d · monthly 90d · quarterly 120d ·
// annual 400d; unknown/NULL falls back to the monthly 90d default).
func controlsWithInWindowEvidence(t *testing.T, tenantID uuid.UUID) int {
	t.Helper()
	var n int
	if err := adminPool.QueryRow(context.Background(), `
		WITH latest AS (
			SELECT c.id AS control_id,
			       c.freshness_class AS class,
			       max(e.observed_at) AS latest_observed
			FROM controls c
			JOIN evidence_records e
			  ON e.tenant_id = c.tenant_id AND e.control_id = c.id
			WHERE c.tenant_id = $1
			GROUP BY c.id, c.freshness_class
		)
		SELECT count(*) FROM latest
		WHERE latest_observed >= now() - (
			CASE class
				WHEN 'realtime'  THEN interval '24 hours'
				WHEN 'daily'     THEN interval '7 days'
				WHEN 'weekly'    THEN interval '30 days'
				WHEN 'monthly'   THEN interval '90 days'
				WHEN 'quarterly' THEN interval '120 days'
				WHEN 'annual'    THEN interval '400 days'
				ELSE interval '90 days'
			END
		)
	`, tenantID).Scan(&n); err != nil {
		t.Fatalf("count controls with in-window evidence: %v", err)
	}
	return n
}

// controlsWithEvaluationRow counts how many of the tenant's controls have AT
// LEAST ONE control_evaluations row. This is the precise inverse of the
// reported symptom: a control with NO evaluation row renders STATE = "—" in
// the /controls list; a control with an evaluation row renders a concrete
// state (green pass / red fail / amber inconclusive·na). Pre-fix this is 0
// (every control shows "—"); post-fix it is the full active-control set.
func controlsWithEvaluationRow(t *testing.T, tenantID uuid.UUID) int {
	t.Helper()
	var n int
	if err := adminPool.QueryRow(context.Background(), `
		SELECT count(DISTINCT control_id)
		FROM control_evaluations
		WHERE tenant_id = $1
	`, tenantID).Scan(&n); err != nil {
		t.Fatalf("count controls with evaluation row: %v", err)
	}
	return n
}

// controlsWithPassFailState counts controls whose LATEST evaluation resolved
// to a concrete pass or fail (the green/red states), proving the engine read
// real ledger evidence rather than emitting a placeholder.
func controlsWithPassFailState(t *testing.T, tenantID uuid.UUID) int {
	t.Helper()
	var n int
	if err := adminPool.QueryRow(context.Background(), `
		WITH latest AS (
			SELECT DISTINCT ON (control_id) control_id, result
			FROM control_evaluations
			WHERE tenant_id = $1
			ORDER BY control_id, evaluated_at DESC
		)
		SELECT count(*) FROM latest WHERE result IN ('pass', 'fail')
	`, tenantID).Scan(&n); err != nil {
		t.Fatalf("count controls with pass/fail state: %v", err)
	}
	return n
}

// TestEvaluateSeededTenant_ProducesRealControlState is the slice-671
// reproduction + fix test. It asserts the symptom holds BEFORE the fix runs
// and is resolved AFTER.
func TestEvaluateSeededTenant_ProducesRealControlState(t *testing.T) {
	const slug = "demo-it-671-eval"
	cleanupTenant(t, slug)
	appPool := appPoolOrSkip(t)

	seeder, err := demoseed.NewSeeder(adminPool, demoseed.DefaultScale)
	if err != nil {
		t.Fatalf("NewSeeder: %v", err)
	}
	res, err := seeder.Apply(context.Background(), demoseed.ApplyInput{
		Slug:          slug,
		ActorUserID:   uuid.Nil,
		ActorTenantID: uuid.Nil,
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if res.Idempotent {
		t.Fatal("expected first apply to write rows; got idempotent")
	}
	if res.Controls == 0 || res.Evidence == 0 {
		t.Fatalf("seed produced no controls/evidence (controls=%d evidence=%d)", res.Controls, res.Evidence)
	}

	// --- SYMPTOM (reproduces the bug; this is the "fails without the fix"
	// half). The seed wrote evidence via direct BYPASSRLS INSERTs and did NOT
	// write the evaluation read models, so immediately after Apply both tables
	// are EMPTY and every control renders STATE = "—".
	if got := controlEvalCount(t, res.TenantID); got != 0 {
		t.Fatalf("pre-eval: expected 0 control_evaluations (seed must not write eval tables — invariant #2); got %d", got)
	}
	if got := freshnessCount(t, res.TenantID); got != 0 {
		t.Fatalf("pre-eval: expected 0 evidence_freshness; got %d", got)
	}
	if got := controlsWithEvaluationRow(t, res.TenantID); got != 0 {
		t.Fatalf("pre-eval: expected 0 controls with an evaluation row (all show \"—\"); got %d", got)
	}

	// --- FIX. Drive the real evaluator for the seeded tenant.
	summary, err := demoseed.EvaluateSeededTenant(context.Background(), appPool, res.TenantID)
	if err != nil {
		t.Fatalf("EvaluateSeededTenant: %v", err)
	}

	// (a) control_evaluations rows exist for the tenant's controls.
	if summary.ControlEvaluations < res.Controls {
		t.Errorf("control_evaluations: summary reported %d; want >= %d (one per active control)",
			summary.ControlEvaluations, res.Controls)
	}
	if got := controlEvalCount(t, res.TenantID); got < res.Controls {
		t.Errorf("control_evaluations rows: got %d; want >= %d active controls", got, res.Controls)
	}

	// (b) evidence_freshness rows exist.
	if summary.FreshnessRows < res.Controls {
		t.Errorf("freshness: summary reported %d; want >= %d", summary.FreshnessRows, res.Controls)
	}
	if got := freshnessCount(t, res.TenantID); got < res.Controls {
		t.Errorf("evidence_freshness rows: got %d; want >= %d", got, res.Controls)
	}

	// (c) a /controls-style read shows a concrete STATE (not "—") for every
	// active control: the engine writes one evaluation row per active control,
	// so NONE render "—". The symptom flipped from 0 evaluation rows (all "—")
	// to the full active-control set.
	withRow := controlsWithEvaluationRow(t, res.TenantID)
	if withRow < res.Controls {
		t.Errorf("controls with an evaluation row: got %d; want >= %d (no active control may show \"—\")",
			withRow, res.Controls)
	}

	// And the controls whose freshest evidence is in-window resolve to a real
	// pass/fail rather than inconclusive — proving the state is computed from
	// the seeded ledger, not a placeholder. (A control whose only evidence is
	// older than its window is correctly inconclusive — faithful behavior.)
	inWindow := controlsWithInWindowEvidence(t, res.TenantID)
	passOrFail := controlsWithPassFailState(t, res.TenantID)
	if inWindow > 0 && passOrFail == 0 {
		t.Errorf("expected some controls to resolve to pass/fail from in-window evidence; got 0 (in-window controls: %d)", inWindow)
	}
	t.Logf("slice-671: %d/%d controls have an evaluation row; %d resolve pass/fail (in-window evidence controls: %d)",
		withRow, res.Controls, passOrFail, inWindow)

	// The demo evidence carries a pass-heavy mix, so the engine must surface
	// at least one pass — proving the state is the real ledger result.
	if passOrFail == 0 {
		t.Errorf("expected at least one control to evaluate to a concrete pass/fail from seeded evidence; got 0")
	}
}

// TestEvaluateSeededTenant_Idempotent asserts AC-5: a second evaluation pass
// does not corrupt state. EvaluateAll appends an immutable row per
// (control, cell, run) and the read surfaces project the latest; Refresh
// UPSERTs one row per control. A second pass leaves the latest-state read
// unchanged and the freshness row count stable.
func TestEvaluateSeededTenant_Idempotent(t *testing.T) {
	const slug = "demo-it-671-idem"
	cleanupTenant(t, slug)
	appPool := appPoolOrSkip(t)

	seeder, err := demoseed.NewSeeder(adminPool, demoseed.DefaultScale)
	if err != nil {
		t.Fatalf("NewSeeder: %v", err)
	}
	res, err := seeder.Apply(context.Background(), demoseed.ApplyInput{Slug: slug})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	if _, err := demoseed.EvaluateSeededTenant(context.Background(), appPool, res.TenantID); err != nil {
		t.Fatalf("EvaluateSeededTenant pass 1: %v", err)
	}
	stateAfter1 := controlsWithPassFailState(t, res.TenantID)
	freshAfter1 := freshnessCount(t, res.TenantID)

	// Second pass — must not corrupt the latest-state read or duplicate
	// freshness rows.
	if _, err := demoseed.EvaluateSeededTenant(context.Background(), appPool, res.TenantID); err != nil {
		t.Fatalf("EvaluateSeededTenant pass 2: %v", err)
	}
	stateAfter2 := controlsWithPassFailState(t, res.TenantID)
	freshAfter2 := freshnessCount(t, res.TenantID)

	if stateAfter1 != stateAfter2 {
		t.Errorf("latest-state read changed across passes: %d -> %d (AC-5 idempotency)", stateAfter1, stateAfter2)
	}
	if freshAfter1 != freshAfter2 {
		t.Errorf("evidence_freshness row count changed across passes: %d -> %d (UPSERT must not duplicate)", freshAfter1, freshAfter2)
	}
}

// TestEvaluateSeededTenant_GuardsBadInput covers the helper's pure-Go guard
// branches without needing a seeded tenant.
func TestEvaluateSeededTenant_GuardsBadInput(t *testing.T) {
	appPool := appPoolOrSkip(t)

	if _, err := demoseed.EvaluateSeededTenant(context.Background(), nil, uuid.New()); err == nil {
		t.Error("expected error on nil app pool")
	}
	if _, err := demoseed.EvaluateSeededTenant(context.Background(), appPool, uuid.Nil); err == nil {
		t.Error("expected error on zero tenant id")
	}
}
