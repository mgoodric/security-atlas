package actionplan

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// pgErrUniqueViolation is the SQLSTATE Postgres returns when a link INSERT
// hits the (action_plan_id, target_id) primary key — i.e. already linked.
const pgErrUniqueViolation = "23505"

// pgErrForeignKeyViolation is the SQLSTATE Postgres returns when a composite
// FK references a (tenant_id, target_id) tuple that does not resolve — the
// structural backstop for cross-tenant linkage (P0-384-4).
const pgErrForeignKeyViolation = "23503"

// pgErrCheckViolation / pgErrRestrictViolation surface from the DB-layer
// transition trigger (illegal status edge) and the append-only trigger.
const (
	pgErrCheckViolation    = "23514"
	pgErrRaiseCheck        = "check_violation"
	pgErrRestrictViolation = "restrict_violation"
)

// ActionPlan is the domain shape returned from store calls.
type ActionPlan struct {
	ID              uuid.UUID
	TenantID        uuid.UUID
	Title           string
	Description     string
	TriggeringEvent string
	OwnerID         uuid.UUID
	DueDate         *time.Time
	Status          string
	AuditPeriodID   *uuid.UUID
	TombstonedAt    *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// Link is one M2M linkage row (a risk or a control target).
type Link struct {
	TargetID uuid.UUID
	LinkedAt time.Time
	LinkedBy uuid.UUID
}

// Linkage bundles a plan's linked risks + controls.
type Linkage struct {
	Risks    []Link
	Controls []Link
}

// PlanRef is the compact shape used by the "Linked Action Plans" read-only
// sections on /risks/{id} and /controls/{id} (AC-25 / AC-26).
type PlanRef struct {
	ID      uuid.UUID
	Title   string
	Status  string
	DueDate *time.Time
}

// AuditEntry is the public shape of an action_plan_audit_log row.
type AuditEntry struct {
	ID           uuid.UUID
	ActionPlanID uuid.UUID
	ActorID      uuid.UUID
	ActionType   string
	BeforeState  json.RawMessage
	AfterState   json.RawMessage
	CreatedAt    time.Time
}

// Store wraps the sqlc Queries with the tenancy plumbing required for RLS.
// Same shape as exception.Store / decision.Store: every method opens a tx,
// applies the tenant GUC, and runs queries inside that transaction.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore constructs a Store over an existing pgx pool. The pool must be
// connected as `atlas_app` (NOSUPERUSER NOBYPASSRLS) for RLS to fire.
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// CreateInput is the API shape for POST /v1/action-plans. The store
// re-validates every rule the handler should have validated already.
type CreateInput struct {
	Title           string
	Description     string
	TriggeringEvent string
	OwnerID         uuid.UUID
	DueDate         *time.Time
	// Status is optional; defaults to draft. A create may only land in
	// `draft` (AC-15: "* -> draft except creation" — creation is the only
	// path INTO draft, and a new plan starts there).
	AuditPeriodID *uuid.UUID
	Actor         uuid.UUID
	// Now is the request-time clock for the 5-year due_date cap. Defaults to
	// time.Now().UTC() when zero.
	Now time.Time
}

// Create inserts a new action plan in `draft` state and writes the matching
// `created` audit-log row in the same transaction (AC-10 + AC-16).
func (s *Store) Create(ctx context.Context, in CreateInput) (ActionPlan, error) {
	if err := ValidateTitle(in.Title); err != nil {
		return ActionPlan{}, err
	}
	if err := ValidateDescription(in.Description); err != nil {
		return ActionPlan{}, err
	}
	if err := ValidateTriggeringEvent(in.TriggeringEvent); err != nil {
		return ActionPlan{}, err
	}
	if in.OwnerID == uuid.Nil {
		return ActionPlan{}, ErrOwnerRequired
	}
	if in.Now.IsZero() {
		in.Now = time.Now().UTC()
	}
	if err := ValidateDueDate(in.DueDate, in.Now); err != nil {
		return ActionPlan{}, err
	}

	var out ActionPlan
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		// AC-10 tampering guard: owner must be a user in this tenant.
		ownerOK, err := q.ActionPlanOwnerExistsInTenant(ctx, dbx.ActionPlanOwnerExistsInTenantParams{
			TenantID: pgUUID(tenantID),
			ID:       pgUUID(in.OwnerID),
		})
		if err != nil {
			return fmt.Errorf("owner existence probe: %w", err)
		}
		if !ownerOK {
			return ErrOwnerNotInTenant
		}

		id := uuid.New()
		row, err := q.CreateActionPlan(ctx, dbx.CreateActionPlanParams{
			ID:              pgUUID(id),
			TenantID:        pgUUID(tenantID),
			Title:           in.Title,
			Description:     strPtr(in.Description),
			TriggeringEvent: strPtr(in.TriggeringEvent),
			OwnerID:         pgUUID(in.OwnerID),
			DueDate:         pgDate(in.DueDate),
			Status:          StatusDraft,
			AuditPeriodID:   pgUUIDPtr(in.AuditPeriodID),
		})
		if err != nil {
			return fmt.Errorf("create action plan: %w", err)
		}
		out = planFromRow(row)
		after, _ := json.Marshal(planSnapshot(out))
		return s.writeAudit(ctx, q, tenantID, id, in.Actor, ActionCreated, nil, after)
	})
	return out, err
}

// Get returns a single live (non-tombstoned) plan by id. ErrNotFound when
// absent or tombstoned (AC-14).
func (s *Store) Get(ctx context.Context, id uuid.UUID) (ActionPlan, error) {
	var out ActionPlan
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		row, err := q.GetActionPlanByID(ctx, dbx.GetActionPlanByIDParams{
			TenantID: pgUUID(tenantID),
			ID:       pgUUID(id),
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("get action plan: %w", err)
		}
		out = planFromRow(row)
		return nil
	})
	return out, err
}

// GetWithLinkage returns a plan plus its linked risks + controls in one
// round-trip (AC-12). ErrNotFound when absent or tombstoned.
func (s *Store) GetWithLinkage(ctx context.Context, id uuid.UUID) (ActionPlan, Linkage, error) {
	var pOut ActionPlan
	var lOut Linkage
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		row, err := q.GetActionPlanByID(ctx, dbx.GetActionPlanByIDParams{
			TenantID: pgUUID(tenantID),
			ID:       pgUUID(id),
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("get action plan: %w", err)
		}
		pOut = planFromRow(row)
		lk, err := loadLinkage(ctx, q, tenantID, id)
		if err != nil {
			return err
		}
		lOut = lk
		return nil
	})
	return pOut, lOut, err
}

// ListFilter narrows List. Empty fields are ignored.
type ListFilter struct {
	Status string
	// Limit is the page size (already clamped by the handler, but clamped
	// again here for defense in depth).
	Limit int
	// Cursor is the (created_at, id) of the last row of the previous page.
	// Nil CursorCreatedAt means "first page".
	CursorCreatedAt *time.Time
	CursorID        *uuid.UUID
}

// List returns one page of the tenant's live action plans, newest first,
// with keyset pagination (AC-11).
func (s *Store) List(ctx context.Context, filter ListFilter) ([]ActionPlan, error) {
	limit := ClampLimit(filter.Limit)
	var out []ActionPlan
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		rows, err := q.ListActionPlans(ctx, dbx.ListActionPlansParams{
			TenantID:        pgUUID(tenantID),
			Status:          nilIfEmpty(filter.Status),
			CursorCreatedAt: pgTimestamptzPtr(filter.CursorCreatedAt),
			CursorID:        pgUUIDPtr(uuidPtrOrNilDefault(filter.CursorID)),
			RowLimit:        int32(limit),
		})
		if err != nil {
			return fmt.Errorf("list action plans: %w", err)
		}
		out = make([]ActionPlan, len(rows))
		for i, r := range rows {
			out[i] = planFromRow(r)
		}
		return nil
	})
	return out, err
}

// ListSnapshot returns the audit-period-frozen view (AC-27): only plans
// created on or before frozenAt. Live state continues independently; this is
// the snapshot read used when a period is frozen.
func (s *Store) ListSnapshot(ctx context.Context, frozenAt time.Time) ([]ActionPlan, error) {
	var out []ActionPlan
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		rows, err := q.ListActionPlansSnapshot(ctx, dbx.ListActionPlansSnapshotParams{
			TenantID:  pgUUID(tenantID),
			CreatedAt: pgTimestamptz(frozenAt),
		})
		if err != nil {
			return fmt.Errorf("list action plans snapshot: %w", err)
		}
		out = make([]ActionPlan, len(rows))
		for i, r := range rows {
			out[i] = planFromRow(r)
		}
		return nil
	})
	return out, err
}

// UpdateInput is the API shape for PATCH /v1/action-plans/{id}. Every field
// is a pointer; nil means "leave unchanged". Status transitions are
// validated against the state machine (AC-13/AC-15).
type UpdateInput struct {
	Title           *string
	Description     *string
	TriggeringEvent *string
	OwnerID         *uuid.UUID
	DueDate         *time.Time
	ClearDueDate    bool
	Status          *string
	Actor           uuid.UUID
	Now             time.Time
}

// Update applies the non-nil fields and writes an audit row. A status change
// writes a `status_changed` row; a non-status edit writes `updated`. An
// illegal transition returns ErrIllegalTransition (422) before any write.
func (s *Store) Update(ctx context.Context, id uuid.UUID, in UpdateInput) (ActionPlan, error) {
	if in.Title != nil {
		if err := ValidateTitle(*in.Title); err != nil {
			return ActionPlan{}, err
		}
	}
	if in.Description != nil {
		if err := ValidateDescription(*in.Description); err != nil {
			return ActionPlan{}, err
		}
	}
	if in.TriggeringEvent != nil {
		if err := ValidateTriggeringEvent(*in.TriggeringEvent); err != nil {
			return ActionPlan{}, err
		}
	}
	if in.Status != nil && !ValidStatus(*in.Status) {
		return ActionPlan{}, ErrInvalidStatus
	}
	if in.Now.IsZero() {
		in.Now = time.Now().UTC()
	}

	var out ActionPlan
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		before, err := q.GetActionPlanByID(ctx, dbx.GetActionPlanByIDParams{
			TenantID: pgUUID(tenantID),
			ID:       pgUUID(id),
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("get action plan (update): %w", err)
		}
		cur := planFromRow(before)

		next := cur
		statusChanged := false
		if in.Title != nil {
			next.Title = *in.Title
		}
		if in.Description != nil {
			next.Description = *in.Description
		}
		if in.TriggeringEvent != nil {
			next.TriggeringEvent = *in.TriggeringEvent
		}
		if in.OwnerID != nil {
			ownerOK, oErr := q.ActionPlanOwnerExistsInTenant(ctx, dbx.ActionPlanOwnerExistsInTenantParams{
				TenantID: pgUUID(tenantID),
				ID:       pgUUID(*in.OwnerID),
			})
			if oErr != nil {
				return fmt.Errorf("owner existence probe: %w", oErr)
			}
			if !ownerOK {
				return ErrOwnerNotInTenant
			}
			next.OwnerID = *in.OwnerID
		}
		switch {
		case in.ClearDueDate:
			next.DueDate = nil
		case in.DueDate != nil:
			if err := ValidateDueDate(in.DueDate, in.Now); err != nil {
				return err
			}
			d := in.DueDate.UTC()
			next.DueDate = &d
		}
		if in.Status != nil && *in.Status != cur.Status {
			// AC-13/AC-15: validate the transition at the store layer; the DB
			// trigger is the second gate.
			if !AllowedTransition(cur.Status, *in.Status) {
				return ErrIllegalTransition
			}
			next.Status = *in.Status
			statusChanged = true
		}

		row, err := q.UpdateActionPlan(ctx, dbx.UpdateActionPlanParams{
			TenantID:        pgUUID(tenantID),
			ID:              pgUUID(id),
			Title:           next.Title,
			Description:     strPtr(next.Description),
			TriggeringEvent: strPtr(next.TriggeringEvent),
			OwnerID:         pgUUID(next.OwnerID),
			DueDate:         pgDate(next.DueDate),
			Status:          next.Status,
			AuditPeriodID:   pgUUIDPtr(next.AuditPeriodID),
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			// The DB transition trigger raises check_violation on an illegal
			// edge — map it to the same 422 the store-layer guard produces.
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == pgErrCheckViolation {
				return ErrIllegalTransition
			}
			return fmt.Errorf("update action plan: %w", err)
		}
		out = planFromRow(row)

		action := ActionUpdated
		if statusChanged {
			action = ActionStatusChanged
		}
		beforeJSON, _ := json.Marshal(planSnapshot(cur))
		afterJSON, _ := json.Marshal(planSnapshot(out))
		return s.writeAudit(ctx, q, tenantID, id, in.Actor, action, beforeJSON, afterJSON)
	})
	return out, err
}

// Tombstone soft-deletes the plan (P0-384-6 — never hard-deleted) and writes
// a `tombstoned` audit row. A subsequent GET returns ErrNotFound (AC-14).
func (s *Store) Tombstone(ctx context.Context, id uuid.UUID, actor uuid.UUID) error {
	return s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		before, err := q.GetActionPlanByID(ctx, dbx.GetActionPlanByIDParams{
			TenantID: pgUUID(tenantID),
			ID:       pgUUID(id),
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("get action plan (tombstone): %w", err)
		}
		cur := planFromRow(before)
		row, err := q.TombstoneActionPlan(ctx, dbx.TombstoneActionPlanParams{
			TenantID: pgUUID(tenantID),
			ID:       pgUUID(id),
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("tombstone action plan: %w", err)
		}
		after := planFromRow(row)
		beforeJSON, _ := json.Marshal(planSnapshot(cur))
		afterJSON, _ := json.Marshal(planSnapshot(after))
		return s.writeAudit(ctx, q, tenantID, id, actor, ActionTombstoned, beforeJSON, afterJSON)
	})
}

// LinkRisk links a risk to the plan. Returns ErrLinkTargetNotFound (404) if
// the risk is absent/cross-tenant (P0-384-4), ErrAlreadyLinked (409) if the
// link exists, ErrLimitExceeded (422) at the 50-risk cap (P0-384-7).
func (s *Store) LinkRisk(ctx context.Context, planID, riskID, actor uuid.UUID) error {
	return s.link(ctx, linkSpec{
		planID:   planID,
		targetID: riskID,
		actor:    actor,
		action:   ActionRiskLinked,
		maxCap:   MaxLinkedRisks,
		targetExists: func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) (bool, error) {
			return q.ActionPlanRiskExistsInTenant(ctx, dbx.ActionPlanRiskExistsInTenantParams{
				TenantID: pgUUID(tenantID), ID: pgUUID(riskID),
			})
		},
		count: func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) (int64, error) {
			return q.CountActionPlanRisks(ctx, dbx.CountActionPlanRisksParams{
				TenantID: pgUUID(tenantID), ActionPlanID: pgUUID(planID),
			})
		},
		linkExists: func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) (bool, error) {
			return q.ActionPlanRiskExists(ctx, dbx.ActionPlanRiskExistsParams{
				TenantID: pgUUID(tenantID), ActionPlanID: pgUUID(planID), RiskID: pgUUID(riskID),
			})
		},
		insert: func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
			return q.LinkActionPlanRisk(ctx, dbx.LinkActionPlanRiskParams{
				ActionPlanID: pgUUID(planID), RiskID: pgUUID(riskID),
				TenantID: pgUUID(tenantID), LinkedBy: pgUUID(actor),
			})
		},
	})
}

// UnlinkRisk removes a risk link (AC-18). ErrNotLinked (404) if absent.
func (s *Store) UnlinkRisk(ctx context.Context, planID, riskID, actor uuid.UUID) error {
	return s.unlink(ctx, planID, actor, ActionRiskUnlinked, riskID, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) (int64, error) {
		return q.UnlinkActionPlanRisk(ctx, dbx.UnlinkActionPlanRiskParams{
			TenantID: pgUUID(tenantID), ActionPlanID: pgUUID(planID), RiskID: pgUUID(riskID),
		})
	})
}

// LinkControl links a control to the plan (AC-19). Same 404/409/422 semantics.
func (s *Store) LinkControl(ctx context.Context, planID, controlID, actor uuid.UUID) error {
	return s.link(ctx, linkSpec{
		planID:   planID,
		targetID: controlID,
		actor:    actor,
		action:   ActionControlLinked,
		maxCap:   MaxLinkedControls,
		targetExists: func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) (bool, error) {
			return q.ActionPlanControlExistsInTenant(ctx, dbx.ActionPlanControlExistsInTenantParams{
				TenantID: pgUUID(tenantID), ID: pgUUID(controlID),
			})
		},
		count: func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) (int64, error) {
			return q.CountActionPlanControls(ctx, dbx.CountActionPlanControlsParams{
				TenantID: pgUUID(tenantID), ActionPlanID: pgUUID(planID),
			})
		},
		linkExists: func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) (bool, error) {
			return q.ActionPlanControlExists(ctx, dbx.ActionPlanControlExistsParams{
				TenantID: pgUUID(tenantID), ActionPlanID: pgUUID(planID), ControlID: pgUUID(controlID),
			})
		},
		insert: func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
			return q.LinkActionPlanControl(ctx, dbx.LinkActionPlanControlParams{
				ActionPlanID: pgUUID(planID), ControlID: pgUUID(controlID),
				TenantID: pgUUID(tenantID), LinkedBy: pgUUID(actor),
			})
		},
	})
}

// UnlinkControl removes a control link (AC-20). ErrNotLinked (404) if absent.
func (s *Store) UnlinkControl(ctx context.Context, planID, controlID, actor uuid.UUID) error {
	return s.unlink(ctx, planID, actor, ActionControlUnlinked, controlID, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) (int64, error) {
		return q.UnlinkActionPlanControl(ctx, dbx.UnlinkActionPlanControlParams{
			TenantID: pgUUID(tenantID), ActionPlanID: pgUUID(planID), ControlID: pgUUID(controlID),
		})
	})
}

// PlansForRisk returns the live action plans linked to a risk (AC-25).
func (s *Store) PlansForRisk(ctx context.Context, riskID uuid.UUID) ([]PlanRef, error) {
	var out []PlanRef
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		rows, err := q.ListActionPlanIDsForRisk(ctx, dbx.ListActionPlanIDsForRiskParams{
			TenantID: pgUUID(tenantID), RiskID: pgUUID(riskID),
		})
		if err != nil {
			return fmt.Errorf("list plans for risk: %w", err)
		}
		out = make([]PlanRef, len(rows))
		for i, r := range rows {
			out[i] = PlanRef{ID: uuid.UUID(r.ID.Bytes), Title: r.Title, Status: r.Status, DueDate: datePtr(r.DueDate)}
		}
		return nil
	})
	return out, err
}

// PlansForControl returns the live action plans linked to a control (AC-26).
func (s *Store) PlansForControl(ctx context.Context, controlID uuid.UUID) ([]PlanRef, error) {
	var out []PlanRef
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		rows, err := q.ListActionPlanIDsForControl(ctx, dbx.ListActionPlanIDsForControlParams{
			TenantID: pgUUID(tenantID), ControlID: pgUUID(controlID),
		})
		if err != nil {
			return fmt.Errorf("list plans for control: %w", err)
		}
		out = make([]PlanRef, len(rows))
		for i, r := range rows {
			out[i] = PlanRef{ID: uuid.UUID(r.ID.Bytes), Title: r.Title, Status: r.Status, DueDate: datePtr(r.DueDate)}
		}
		return nil
	})
	return out, err
}

// ListAuditLog returns the audit trail for a plan, oldest first.
func (s *Store) ListAuditLog(ctx context.Context, planID uuid.UUID) ([]AuditEntry, error) {
	var out []AuditEntry
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		rows, err := q.ListActionPlanAuditLog(ctx, dbx.ListActionPlanAuditLogParams{
			TenantID: pgUUID(tenantID), ActionPlanID: pgUUID(planID),
		})
		if err != nil {
			return fmt.Errorf("list action plan audit log: %w", err)
		}
		out = make([]AuditEntry, len(rows))
		for i, r := range rows {
			out[i] = AuditEntry{
				ID:           uuid.UUID(r.ID.Bytes),
				ActionPlanID: uuid.UUID(r.ActionPlanID.Bytes),
				ActorID:      uuid.UUID(r.ActorID.Bytes),
				ActionType:   r.ActionType,
				BeforeState:  append(json.RawMessage(nil), r.BeforeState...),
				AfterState:   append(json.RawMessage(nil), r.AfterState...),
				CreatedAt:    tsTime(r.CreatedAt),
			}
		}
		return nil
	})
	return out, err
}

// ----- link plumbing -----

type linkSpec struct {
	planID       uuid.UUID
	targetID     uuid.UUID
	actor        uuid.UUID
	action       string
	maxCap       int
	targetExists func(context.Context, *dbx.Queries, uuid.UUID) (bool, error)
	count        func(context.Context, *dbx.Queries, uuid.UUID) (int64, error)
	linkExists   func(context.Context, *dbx.Queries, uuid.UUID) (bool, error)
	insert       func(context.Context, *dbx.Queries, uuid.UUID) error
}

func (s *Store) link(ctx context.Context, spec linkSpec) error {
	return s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		// The plan itself must exist (live) in this tenant.
		if _, err := q.GetActionPlanByID(ctx, dbx.GetActionPlanByIDParams{
			TenantID: pgUUID(tenantID), ID: pgUUID(spec.planID),
		}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("get action plan (link): %w", err)
		}
		// AC-17/AC-19/P0-384-4: the target must resolve in this tenant. RLS
		// hides the cross-tenant row, so the probe returns false -> 404.
		targetOK, err := spec.targetExists(ctx, q, tenantID)
		if err != nil {
			return fmt.Errorf("link target existence probe: %w", err)
		}
		if !targetOK {
			return ErrLinkTargetNotFound
		}
		// AC-17/AC-19: already-linked -> 409.
		exists, err := spec.linkExists(ctx, q, tenantID)
		if err != nil {
			return fmt.Errorf("link existence probe: %w", err)
		}
		if exists {
			return ErrAlreadyLinked
		}
		// P0-384-7: per-plan cap.
		n, err := spec.count(ctx, q, tenantID)
		if err != nil {
			return fmt.Errorf("link count probe: %w", err)
		}
		if int(n) >= spec.maxCap {
			return ErrLimitExceeded
		}
		if err := spec.insert(ctx, q, tenantID); err != nil {
			// The composite FK is the structural backstop: a target that
			// slipped past the probe (race) still cannot cross tenants.
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) {
				switch pgErr.Code {
				case pgErrUniqueViolation:
					return ErrAlreadyLinked
				case pgErrForeignKeyViolation:
					return ErrLinkTargetNotFound
				}
			}
			return fmt.Errorf("insert link: %w", err)
		}
		detail, _ := json.Marshal(map[string]string{"target_id": spec.targetID.String()})
		return s.writeAudit(ctx, q, tenantID, spec.planID, spec.actor, spec.action, nil, detail)
	})
}

func (s *Store) unlink(ctx context.Context, planID, actor uuid.UUID, action string, targetID uuid.UUID, del func(context.Context, *dbx.Queries, uuid.UUID) (int64, error)) error {
	return s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		if _, err := q.GetActionPlanByID(ctx, dbx.GetActionPlanByIDParams{
			TenantID: pgUUID(tenantID), ID: pgUUID(planID),
		}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("get action plan (unlink): %w", err)
		}
		n, err := del(ctx, q, tenantID)
		if err != nil {
			return fmt.Errorf("delete link: %w", err)
		}
		if n == 0 {
			return ErrNotLinked
		}
		detail, _ := json.Marshal(map[string]string{"target_id": targetID.String()})
		return s.writeAudit(ctx, q, tenantID, planID, actor, action, detail, nil)
	})
}

func loadLinkage(ctx context.Context, q *dbx.Queries, tenantID, planID uuid.UUID) (Linkage, error) {
	var lk Linkage
	riskRows, err := q.ListActionPlanRisks(ctx, dbx.ListActionPlanRisksParams{
		TenantID: pgUUID(tenantID), ActionPlanID: pgUUID(planID),
	})
	if err != nil {
		return Linkage{}, fmt.Errorf("list action plan risks: %w", err)
	}
	for _, r := range riskRows {
		lk.Risks = append(lk.Risks, Link{TargetID: uuid.UUID(r.RiskID.Bytes), LinkedAt: tsTime(r.LinkedAt), LinkedBy: uuid.UUID(r.LinkedBy.Bytes)})
	}
	ctrlRows, err := q.ListActionPlanControls(ctx, dbx.ListActionPlanControlsParams{
		TenantID: pgUUID(tenantID), ActionPlanID: pgUUID(planID),
	})
	if err != nil {
		return Linkage{}, fmt.Errorf("list action plan controls: %w", err)
	}
	for _, r := range ctrlRows {
		lk.Controls = append(lk.Controls, Link{TargetID: uuid.UUID(r.ControlID.Bytes), LinkedAt: tsTime(r.LinkedAt), LinkedBy: uuid.UUID(r.LinkedBy.Bytes)})
	}
	return lk, nil
}

// writeAudit appends one action_plan_audit_log row inside the caller's tx
// (AC-16). The append-only trigger guards the table against UPDATE/DELETE.
func (s *Store) writeAudit(ctx context.Context, q *dbx.Queries, tenantID, planID, actor uuid.UUID, action string, before, after []byte) error {
	if _, err := q.WriteActionPlanAuditLog(ctx, dbx.WriteActionPlanAuditLogParams{
		ID:           pgUUID(uuid.New()),
		TenantID:     pgUUID(tenantID),
		ActionPlanID: pgUUID(planID),
		ActorID:      pgUUID(actor),
		ActionType:   action,
		BeforeState:  before,
		AfterState:   after,
	}); err != nil {
		return fmt.Errorf("write action plan audit (%s): %w", action, err)
	}
	return nil
}

// ----- tenancy plumbing -----

func (s *Store) inTx(ctx context.Context, fn func(context.Context, *dbx.Queries, uuid.UUID) error) error {
	tenantStr, err := tenancy.TenantFromContext(ctx)
	if err != nil {
		return err
	}
	tenantID, err := uuid.Parse(tenantStr)
	if err != nil {
		return fmt.Errorf("actionplan: parse tenant id: %w", err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("actionplan: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		return err
	}
	q := dbx.New(tx)
	if err := fn(ctx, q, tenantID); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("actionplan: commit: %w", err)
	}
	return nil
}

// ----- row conversion + snapshots -----

func planFromRow(r dbx.ActionPlan) ActionPlan {
	out := ActionPlan{
		ID:       uuid.UUID(r.ID.Bytes),
		TenantID: uuid.UUID(r.TenantID.Bytes),
		Title:    r.Title,
		OwnerID:  uuid.UUID(r.OwnerID.Bytes),
		Status:   r.Status,
	}
	if r.Description != nil {
		out.Description = *r.Description
	}
	if r.TriggeringEvent != nil {
		out.TriggeringEvent = *r.TriggeringEvent
	}
	out.DueDate = datePtr(r.DueDate)
	if r.AuditPeriodID.Valid {
		u := uuid.UUID(r.AuditPeriodID.Bytes)
		out.AuditPeriodID = &u
	}
	if r.TombstonedAt.Valid {
		t := r.TombstonedAt.Time
		out.TombstonedAt = &t
	}
	out.CreatedAt = tsTime(r.CreatedAt)
	out.UpdatedAt = tsTime(r.UpdatedAt)
	return out
}

// planSnapshot is the JSONB before/after shape stored in the audit log. Kept
// compact + stable so the trail is legible.
func planSnapshot(p ActionPlan) map[string]any {
	m := map[string]any{
		"title":            p.Title,
		"description":      p.Description,
		"triggering_event": p.TriggeringEvent,
		"owner_id":         p.OwnerID.String(),
		"status":           p.Status,
	}
	if p.DueDate != nil {
		m["due_date"] = p.DueDate.Format("2006-01-02")
	}
	if p.TombstonedAt != nil {
		m["tombstoned_at"] = p.TombstonedAt.Format(time.RFC3339)
	}
	return m
}

// ----- pgtype helpers -----

func pgUUID(u uuid.UUID) pgtype.UUID { return pgtype.UUID{Bytes: u, Valid: true} }

func pgUUIDPtr(u *uuid.UUID) pgtype.UUID {
	if u == nil {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: *u, Valid: true}
}

func uuidPtrOrNilDefault(u *uuid.UUID) *uuid.UUID { return u }

func pgTimestamptz(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t, Valid: true}
}

func pgTimestamptzPtr(t *time.Time) pgtype.Timestamptz {
	if t == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: *t, Valid: true}
}

func pgDate(t *time.Time) pgtype.Date {
	if t == nil {
		return pgtype.Date{}
	}
	return pgtype.Date{Time: t.UTC(), Valid: true}
}

func datePtr(d pgtype.Date) *time.Time {
	if !d.Valid {
		return nil
	}
	t := d.Time
	return &t
}

func tsTime(t pgtype.Timestamptz) time.Time {
	if !t.Valid {
		return time.Time{}
	}
	return t.Time
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	v := s
	return &v
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	v := s
	return &v
}
