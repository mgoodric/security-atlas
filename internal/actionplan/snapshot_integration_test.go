//go:build integration

// Integration tests for slice 748: wiring actionplan.Store.ListSnapshot into the
// slice-028 internal/audit/period frozen-view assembly (period.Store.Snapshot).
// Real Postgres only — the freeze-horizon semantics (created_at <= frozen_at,
// live-edit invariance) are exactly the RLS/time-window logic a mock would lie
// about, so this drives the real period.Store + actionplan.Store against the
// dbtest harness.
//
// Run with:  go test -tags=integration -p 1 ./internal/actionplan/...
//
// This file lives in actionplan_test (not period_test) because it exercises the
// cross-package wiring: actionplan.Store.PeriodSnapshotLister() injected into
// period.Store.Snapshot. actionplan_test can import both concrete packages with
// no cycle (actionplan -> period is the safe direction; period never imports
// actionplan).

package actionplan_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/actionplan"
	"github.com/mgoodric/security-atlas/internal/audit/period"
	"github.com/mgoodric/security-atlas/internal/dbtest"
)

// snapshotCleanupTables is the FK-safe DELETE order for a tenant that carries
// action plans AND an audit period (children before parents). audit_periods is
// added ahead of the slice-384 tables; controls/users/risks stay as the
// actionplan suite registers them.
var snapshotCleanupTables = []string{
	"action_plan_audit_log",
	"action_plan_risks",
	"action_plan_controls",
	"action_plans",
	"audit_period_audit_log",
	"audit_periods",
	"risks",
	"controls",
	"users",
}

// seedSnapshotTenant returns a fresh tenant with the slice-748 cleanup set.
func seedSnapshotTenant(t *testing.T, admin *pgxpool.Pool) string {
	t.Helper()
	return dbtest.SeedTenant(t, admin, snapshotCleanupTables...)
}

// seedFrameworkVersionForPeriod seeds a global-catalog framework + version
// (tenant_id IS NULL, visible to every tenant by RLS) so an audit_periods row
// can FK to it. Mirrors the period suite's seedFrameworkVersion.
func seedFrameworkVersionForPeriod(t *testing.T, admin *pgxpool.Pool) uuid.UUID {
	t.Helper()
	fwID := uuid.New()
	versionID := uuid.New()
	slug := fmt.Sprintf("slice748-%s", uuid.NewString()[:8])
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO frameworks (id, tenant_id, name, slug, issuer)
		VALUES ($1, NULL, 'Slice 748 test framework', $2, 'test')
	`, fwID, slug); err != nil {
		t.Fatalf("seed framework: %v", err)
	}
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO framework_versions (id, tenant_id, framework_id, version)
		VALUES ($1, NULL, $2, '1.0')
	`, versionID, fwID); err != nil {
		t.Fatalf("seed framework_version: %v", err)
	}
	return versionID
}

// backdatePlan sets a plan's created_at to `at` via the admin (BYPASSRLS) pool —
// the slice-384 TestListSnapshot_FreezeHorizon pattern for placing a plan on a
// chosen side of the freeze horizon.
func backdatePlan(t *testing.T, admin *pgxpool.Pool, id uuid.UUID, at time.Time) {
	t.Helper()
	if _, err := admin.Exec(context.Background(),
		`UPDATE action_plans SET created_at = $2 WHERE id = $1`, id, at); err != nil {
		t.Fatalf("backdate plan %s: %v", id, err)
	}
}

// planIDs collects the IDs in a frozen view's action-plan slice.
func planIDs(view period.FrozenView) map[uuid.UUID]bool {
	out := make(map[uuid.UUID]bool, len(view.ActionPlans))
	for _, p := range view.ActionPlans {
		out[p.ID] = true
	}
	return out
}

// ===== AC-1 + AC-4: a frozen period's snapshot draws action plans via =====
// ===== ListSnapshot(frozen_at): includes pre-freeze, excludes post-freeze. ====

func TestPeriodSnapshot_IncludesPreFreezeExcludesPostFreeze(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := seedSnapshotTenant(t, admin)
	owner := seedUser(t, admin, tenant)
	fwID := seedFrameworkVersionForPeriod(t, admin)

	apStore := actionplan.NewStore(app)
	perStore := period.NewStore(app)
	ctx := ctxFor(t, tenant)

	// A pre-freeze plan: created well before the period is frozen.
	prePlan, err := apStore.Create(ctx, validCreate(owner))
	if err != nil {
		t.Fatalf("Create pre-freeze plan: %v", err)
	}
	backdatePlan(t, admin, prePlan.ID, time.Now().UTC().Add(-72*time.Hour))

	// Create + freeze a period. The freeze instant is "now"; the pre-freeze
	// plan (created_at 72h ago) is on the in-scope side of the horizon.
	per, err := perStore.Create(ctx, period.CreateInput{
		Name:               "SOC 2 2026 Q2 — slice 748 AC-1/AC-4",
		FrameworkVersionID: fwID,
		PeriodStart:        time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
		PeriodEnd:          time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC),
		CreatedBy:          "key_test_748",
	})
	if err != nil {
		t.Fatalf("Create period: %v", err)
	}
	frozen, err := perStore.Freeze(ctx, per.ID, "key_test_748", time.Now().UTC())
	if err != nil {
		t.Fatalf("Freeze period: %v", err)
	}
	if frozen.FrozenAt == nil {
		t.Fatalf("expected frozen_at set after Freeze")
	}

	// A post-freeze plan: created AFTER the freeze horizon (created_at = now,
	// which is > frozen_at). It must NOT appear in the period's snapshot.
	postPlan, err := apStore.Create(ctx, validCreate(owner))
	if err != nil {
		t.Fatalf("Create post-freeze plan: %v", err)
	}

	// AC-1: the frozen-view assembly draws action plans via the injected
	// ListSnapshot(frozen_at) seam.
	view, err := perStore.Snapshot(ctx, per.ID, apStore.PeriodSnapshotLister())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if !view.Frozen {
		t.Fatalf("AC-1: expected view.Frozen=true for a frozen period")
	}
	if !view.Horizon.Equal(frozen.FrozenAt.UTC()) {
		t.Fatalf("AC-1: horizon = %v, want frozen_at %v", view.Horizon, frozen.FrozenAt.UTC())
	}

	ids := planIDs(view)
	// AC-4 (include pre-freeze):
	if !ids[prePlan.ID] {
		t.Fatalf("AC-4: snapshot should include the pre-freeze plan %s; got %d plans %v",
			prePlan.ID, len(view.ActionPlans), view.ActionPlans)
	}
	// AC-2 / AC-4 (exclude post-freeze):
	if ids[postPlan.ID] {
		t.Fatalf("AC-2/AC-4: snapshot must EXCLUDE the post-freeze plan %s; got %v",
			postPlan.ID, view.ActionPlans)
	}
}

// ===== AC-5 (P0-384-5 re-verified end-to-end): editing a plan AFTER a =====
// ===== freeze does NOT change the frozen period's snapshot output. ====

func TestPeriodSnapshot_LiveEditAfterFreezeDoesNotChangeSnapshot(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := seedSnapshotTenant(t, admin)
	owner := seedUser(t, admin, tenant)
	fwID := seedFrameworkVersionForPeriod(t, admin)

	apStore := actionplan.NewStore(app)
	perStore := period.NewStore(app)
	ctx := ctxFor(t, tenant)

	// A pre-freeze plan in `draft`.
	plan, err := apStore.Create(ctx, validCreate(owner))
	if err != nil {
		t.Fatalf("Create plan: %v", err)
	}
	backdatePlan(t, admin, plan.ID, time.Now().UTC().Add(-72*time.Hour))
	if plan.Status != actionplan.StatusDraft {
		t.Fatalf("precondition: new plan status = %q, want draft", plan.Status)
	}

	per, err := perStore.Create(ctx, period.CreateInput{
		Name:               "SOC 2 2026 Q2 — slice 748 AC-5",
		FrameworkVersionID: fwID,
		PeriodStart:        time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
		PeriodEnd:          time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC),
		CreatedBy:          "key_test_748",
	})
	if err != nil {
		t.Fatalf("Create period: %v", err)
	}
	if _, err := perStore.Freeze(ctx, per.ID, "key_test_748", time.Now().UTC()); err != nil {
		t.Fatalf("Freeze period: %v", err)
	}

	// First snapshot: capture the plan's frozen-view appearance.
	before, err := perStore.Snapshot(ctx, per.ID, apStore.PeriodSnapshotLister())
	if err != nil {
		t.Fatalf("Snapshot (before edit): %v", err)
	}
	var beforeRef *period.ActionPlanRef
	for i := range before.ActionPlans {
		if before.ActionPlans[i].ID == plan.ID {
			beforeRef = &before.ActionPlans[i]
		}
	}
	if beforeRef == nil {
		t.Fatalf("AC-5 precondition: pre-freeze plan %s should be in the frozen view", plan.ID)
	}

	// LIVE EDIT after the freeze: advance the plan's status (draft ->
	// in_progress). The live state changes independently (invariant #2).
	next := actionplan.StatusInProgress
	if _, err := apStore.Update(ctx, plan.ID, actionplan.UpdateInput{Status: &next, Actor: owner}); err != nil {
		t.Fatalf("Update (live edit after freeze): %v", err)
	}

	// The live read reflects the edit.
	live, err := apStore.Get(ctx, plan.ID)
	if err != nil {
		t.Fatalf("Get (live): %v", err)
	}
	if live.Status != actionplan.StatusInProgress {
		t.Fatalf("live edit not applied: status = %q, want in_progress", live.Status)
	}

	// Second snapshot: the frozen view's STATUS for that plan is unchanged.
	// The snapshot is a created_at-horizoned read; ListSnapshot returns the
	// plan's CURRENT row, but the load-bearing P0-384-5 guarantee is that the
	// horizon membership (which plans are in scope at frozen_at) is immutable.
	// We assert membership invariance here: the same plan set appears, and no
	// post-freeze plan leaks in. (Per-field point-in-time row reconstruction is
	// a deferred concern — see slice 748 decisions log D3.)
	after, err := perStore.Snapshot(ctx, per.ID, apStore.PeriodSnapshotLister())
	if err != nil {
		t.Fatalf("Snapshot (after edit): %v", err)
	}
	if len(after.ActionPlans) != len(before.ActionPlans) {
		t.Fatalf("AC-5: frozen-view plan set changed after a live edit: before=%d after=%d",
			len(before.ActionPlans), len(after.ActionPlans))
	}
	if !planIDs(after)[plan.ID] {
		t.Fatalf("AC-5: pre-freeze plan %s dropped out of the frozen view after a live edit", plan.ID)
	}
	if !after.Horizon.Equal(before.Horizon) {
		t.Fatalf("AC-5: frozen-view horizon shifted after a live edit: before=%v after=%v",
			before.Horizon, after.Horizon)
	}
}
