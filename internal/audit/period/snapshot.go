package period

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// ActionPlanRef is the period package's view of one remediation commitment
// (action plan) as it appears in a frozen-period snapshot. It is a deliberately
// minimal projection: the period package owns the audit-period freezing
// primitive (invariant #10) and only needs the fields an auditor reads off a
// "remediation commitments in scope at freeze time" view. The richer shape
// lives in internal/actionplan; this struct is the seam across the layer
// boundary so the period package never imports internal/actionplan (which would
// be a peer-domain dependency and an import-cycle risk — see slice 748
// decisions log D1).
type ActionPlanRef struct {
	ID            uuid.UUID
	Title         string
	Status        string
	OwnerID       uuid.UUID
	DueDate       *time.Time
	AuditPeriodID *uuid.UUID
	CreatedAt     time.Time
}

// ActionPlanSnapshotLister is the injected seam the frozen-view assembly reads
// remediation commitments through. The production wiring passes a closure over
// internal/actionplan.Store.ListSnapshot (see the API callers); the integration
// tests inject the real store the same way. Keeping this a function-typed
// dependency (rather than importing internal/actionplan here) honors the
// layering: the period package stays a leaf of the audit subtree and no import
// cycle can form (slice 748 D1).
//
// The horizon passed in is the period's frozen_at instant; the implementation
// MUST return only plans with created_at <= horizon (the read-side horizon that
// honors invariant #10 + P0-384-5). actionplan.Store.ListSnapshot does exactly
// that.
type ActionPlanSnapshotLister func(ctx context.Context, horizon time.Time) ([]ActionPlanRef, error)

// FrozenView is the materialized read-side view of a period at its freeze
// horizon. Today it carries the action-plan participant slice 748 wires in;
// other frozen-view participants (per-control evidence state) continue to be
// read through their own horizon-bounded path (ControlState) and are not
// duplicated here. The struct is the assembly point a future slice can extend
// without re-plumbing the snapshot read.
type FrozenView struct {
	// PeriodID is the audit period this view belongs to.
	PeriodID uuid.UUID
	// Horizon is the instant the view is bounded by: the period's frozen_at
	// when the period is frozen. When the period is still open, Frozen is
	// false and Horizon is the wall-clock read time (live state).
	Horizon time.Time
	// Frozen reports whether the period was frozen at read time. A frozen
	// view is reproducible (invariant #2 point-in-time replay); an open view
	// reflects live state and is not.
	Frozen bool
	// ActionPlans are the remediation commitments in scope at Horizon — only
	// plans with created_at <= Horizon (AC-1/AC-2). Drawn through the injected
	// ActionPlanSnapshotLister.
	ActionPlans []ActionPlanRef
}

// Snapshot assembles the frozen-period read view for one period, drawing
// remediation commitments (action plans) through the injected lister at the
// period's freeze horizon (AC-1).
//
// Horizon semantics (invariant #10):
//   - period frozen   -> horizon = frozen_at; the view is reproducible and
//     draws only plans with created_at <= frozen_at. A plan created or mutated
//     AFTER frozen_at is excluded (AC-2), and a later edit of a pre-freeze plan
//     never changes this view's output (AC-5) — both inherent to the
//     created_at-horizoned snapshot read, which never mutates a record to
//     produce the view (invariant #2).
//   - period open     -> horizon = wall-clock now; the view reflects live state
//     and is NOT a frozen artifact. Callers that only want frozen views should
//     gate on FrozenView.Frozen.
//
// The lister is required; a nil lister is a programming error (the caller must
// wire actionplan.Store.ListSnapshot). ErrNotFound when the period is absent or
// cross-tenant.
func (s *Store) Snapshot(ctx context.Context, periodID uuid.UUID, plans ActionPlanSnapshotLister) (FrozenView, error) {
	if plans == nil {
		return FrozenView{}, fmt.Errorf("auditperiod: Snapshot requires a non-nil ActionPlanSnapshotLister")
	}

	var horizon time.Time
	var frozen bool

	// Resolve the period (and thus the horizon) inside the store's tenant-GUC
	// transaction; RLS hides cross-tenant rows so a foreign periodID surfaces
	// as ErrNotFound.
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		p, err := q.GetAuditPeriodByID(ctx, dbx.GetAuditPeriodByIDParams{
			TenantID: pgUUID(tenantID),
			ID:       pgUUID(periodID),
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("get period for snapshot: %w", err)
		}
		if p.Status == string(StatusFrozen) && p.FrozenAt.Valid {
			horizon = p.FrozenAt.Time.UTC()
			frozen = true
		} else {
			horizon = time.Now().UTC()
			frozen = false
		}
		return nil
	})
	if err != nil {
		return FrozenView{}, err
	}

	// The action-plan read goes through the injected lister, which runs its
	// own tenant-GUC transaction (actionplan.Store.ListSnapshot). The frozen
	// view is a composition of independent horizon-bounded reads, never a
	// record mutation (invariant #2).
	refs, err := plans(ctx, horizon)
	if err != nil {
		return FrozenView{}, fmt.Errorf("snapshot action plans: %w", err)
	}

	return FrozenView{
		PeriodID:    periodID,
		Horizon:     horizon,
		Frozen:      frozen,
		ActionPlans: refs,
	}, nil
}
