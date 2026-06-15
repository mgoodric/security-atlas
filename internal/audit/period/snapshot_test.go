// snapshot_test.go — pure-Go coverage for the slice-748 frozen-view snapshot
// wiring that does NOT require Postgres.
//
// Load-bearing branches covered here (fast loop, no build tag, per the
// CLAUDE.md "Pure-Go pre-DB unit convention" Q-2):
//
//   - Store.Snapshot nil-lister guard — a nil ActionPlanSnapshotLister is a
//     programming error; Snapshot rejects it BEFORE touching the DB, so the
//     branch is reachable with a nil *pgxpool.Pool (the guard returns before
//     any pool use). This pins the contract that the caller MUST wire
//     actionplan.Store.ListSnapshot.
//   - FrozenView / ActionPlanRef zero-value shape — documents the assembly
//     struct the production path fills (the DB-bound assembly itself is covered
//     by the live integration suite in internal/actionplan).
//
// The created_at <= frozen_at horizon, RLS isolation, and live-edit invariance
// (AC-1..AC-5) are covered against real Postgres in
// internal/actionplan/snapshot_integration_test.go — they cannot be asserted
// against a fake DB.

package period

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestSnapshot_NilListerIsRejectedBeforeDB(t *testing.T) {
	t.Parallel()
	// A Store with a nil pool: the nil-lister guard must fire BEFORE any pool
	// access, so this never dereferences the pool.
	s := &Store{pool: nil}
	_, err := s.Snapshot(context.Background(), uuid.New(), nil)
	if err == nil {
		t.Fatal("Snapshot with a nil lister should return an error, got nil")
	}
}

func TestFrozenView_ZeroValue(t *testing.T) {
	t.Parallel()
	var v FrozenView
	if v.Frozen {
		t.Error("zero FrozenView.Frozen should be false")
	}
	if v.ActionPlans != nil {
		t.Error("zero FrozenView.ActionPlans should be nil")
	}
	if !v.Horizon.IsZero() {
		t.Error("zero FrozenView.Horizon should be the zero time")
	}
}

func TestActionPlanRef_FieldRoundTrip(t *testing.T) {
	t.Parallel()
	id := uuid.New()
	owner := uuid.New()
	ap := uuid.New()
	due := time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC)
	created := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	ref := ActionPlanRef{
		ID:            id,
		Title:         "Close the IAC-06 freshness gap",
		Status:        "in_progress",
		OwnerID:       owner,
		DueDate:       &due,
		AuditPeriodID: &ap,
		CreatedAt:     created,
	}
	if ref.ID != id || ref.OwnerID != owner || *ref.AuditPeriodID != ap {
		t.Fatalf("ActionPlanRef id/owner/audit_period_id round-trip mismatch: %+v", ref)
	}
	if ref.DueDate == nil || !ref.DueDate.Equal(due) {
		t.Fatalf("ActionPlanRef due_date round-trip mismatch: %+v", ref.DueDate)
	}
	if !ref.CreatedAt.Equal(created) {
		t.Fatalf("ActionPlanRef created_at round-trip mismatch: %v", ref.CreatedAt)
	}
}
