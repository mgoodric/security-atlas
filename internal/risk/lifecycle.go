// The risk open/closed lifecycle (#2107).
//
//	close   open   -> closed (closed)    reason required, note optional
//	reopen  closed -> open   (reopened)  the closure is cleared
//
// Closing retires a risk without deleting its history. A closed risk is
// left out of the register list and counts, the heatmaps, the org-unit
// rollups, the dashboard's and board pack's top risks, the review
// calendar, and aggregation-rule candidates. Hard delete stays for records
// that should never have existed, and is admin-only at the HTTP layer.
//
// Each transition writes one risk_audit_log row in its transaction. Status
// is an aggregation input (a closed risk is not a rule candidate), so close
// and reopen take the rule locks before the row lock and re-evaluate the
// rules targeting the risk's themes afterwards, in the order Delete uses
// (#1867).

package risk

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// Audit actions the lifecycle writes (migration 20261209000000).
const (
	ActionClosed   = "closed"
	ActionReopened = "reopened"
	ActionDeleted  = "deleted"
)

// Closure is why and by whom a closed risk was closed. It is empty for an
// open risk.
type Closure struct {
	Reason dbx.RiskClosureReason
	Note   string
	At     *time.Time
	By     string
}

// CloseInput closes an open risk.
type CloseInput struct {
	Reason dbx.RiskClosureReason
	Note   string
	Actor  string
}

// ReopenInput reopens a closed risk.
type ReopenInput struct {
	Actor string
}

// ValidClosureReason reports whether reason is one of the closure reasons.
func ValidClosureReason(reason dbx.RiskClosureReason) bool {
	for _, v := range dbx.AllRiskClosureReasonValues() {
		if reason == v {
			return true
		}
	}
	return false
}

// Close closes an open risk with a reason and an optional note.
func (s *Store) Close(ctx context.Context, id uuid.UUID, in CloseInput) (Risk, error) {
	if in.Reason == "" {
		return Risk{}, fmt.Errorf("%w: reason is required", ErrTransitionInput)
	}
	if !ValidClosureReason(in.Reason) {
		return Risk{}, fmt.Errorf("%w: reason must be one of not_applicable, merged, realised_and_handled", ErrTransitionInput)
	}
	if in.Actor == "" {
		return Risk{}, fmt.Errorf("%w: actor is required", ErrTransitionInput)
	}
	note := strings.TrimSpace(in.Note)
	return s.lifecycle(ctx, id, func(current Risk, now time.Time) (dbx.SetRiskStatusParams, string, map[string]any, map[string]any, error) {
		if current.Status != dbx.RiskStatusOpen {
			return dbx.SetRiskStatusParams{}, "", nil, nil, fmt.Errorf("%w: only an open risk can be closed", ErrTransitionState)
		}
		reason := in.Reason
		params := dbx.SetRiskStatusParams{
			Status:        dbx.RiskStatusClosed,
			ClosureReason: &reason,
			ClosureNote:   note,
			ClosedAt:      pgtype.Timestamptz{Time: now, Valid: true},
			ClosedBy:      in.Actor,
		}
		before := map[string]any{"status": current.Status}
		after := map[string]any{"status": dbx.RiskStatusClosed, "closure_reason": reason}
		if note != "" {
			after["closure_note"] = note
		}
		return params, in.Actor, before, after, nil
	})
}

// Reopen returns a closed risk to open and clears its closure. The audit
// row keeps the closure it clears.
func (s *Store) Reopen(ctx context.Context, id uuid.UUID, in ReopenInput) (Risk, error) {
	if in.Actor == "" {
		return Risk{}, fmt.Errorf("%w: actor is required", ErrTransitionInput)
	}
	return s.lifecycle(ctx, id, func(current Risk, _ time.Time) (dbx.SetRiskStatusParams, string, map[string]any, map[string]any, error) {
		if current.Status != dbx.RiskStatusClosed {
			return dbx.SetRiskStatusParams{}, "", nil, nil, fmt.Errorf("%w: only a closed risk can be reopened", ErrTransitionState)
		}
		before := map[string]any{
			"status":         current.Status,
			"closure_reason": current.Closure.Reason,
			"closed_by":      current.Closure.By,
		}
		if current.Closure.Note != "" {
			before["closure_note"] = current.Closure.Note
		}
		if current.Closure.At != nil {
			before["closed_at"] = current.Closure.At.UTC().Format(time.RFC3339)
		}
		after := map[string]any{"status": dbx.RiskStatusOpen}
		return dbx.SetRiskStatusParams{Status: dbx.RiskStatusOpen}, in.Actor, before, after, nil
	})
}

type lifecycleFn func(current Risk, now time.Time) (params dbx.SetRiskStatusParams, actor string, before, after map[string]any, err error)

// lifecycle runs a close or reopen: the rule locks for the risk's themes,
// then the row lock, the state check, the write and its audit row, then
// the re-evaluation of the rules the risk feeds. A rule-generated
// meta-risk does not re-evaluate its own rule, as Delete does not.
func (s *Store) lifecycle(ctx context.Context, id uuid.UUID, fn lifecycleFn) (Risk, error) {
	var out Risk
	err := s.inTxRuleLocked(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID, lastAttempt bool) error {
		row, err := q.GetRiskByID(ctx, dbx.GetRiskByIDParams{TenantID: pgUUID(tenantID), ID: pgUUID(id)})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("risk lifecycle get: %w", err)
		}
		preLocked := row.Themes
		if err := s.runBeforeRiskWrite(ctx, q, tenantID, preLocked); err != nil {
			return err
		}
		current, err := lockRisk(ctx, q, tenantID, id)
		if err != nil {
			return err
		}
		if err := s.checkRuleLocks(lastAttempt, preLocked, current.Themes); err != nil {
			return err
		}
		params, actor, before, after, err := fn(current, time.Now().UTC())
		if err != nil {
			return err
		}
		params.TenantID, params.ID = pgUUID(tenantID), pgUUID(id)
		updated, err := q.SetRiskStatus(ctx, params)
		if err != nil {
			return fmt.Errorf("set risk status: %w", err)
		}
		action := ActionReopened
		if params.Status == dbx.RiskStatusClosed {
			action = ActionClosed
		}
		if err := insertRiskAudit(ctx, q, tenantID, id, actor, action, before, after); err != nil {
			return err
		}
		return s.finishPatch(ctx, q, tenantID, riskFromRow(updated), &out, !isRuleGeneratedScore(updated.InherentScore))
	})
	return out, err
}

// closureFromRow reads the closure columns; empty for an open risk.
func closureFromRow(r dbx.Risk) Closure {
	c := Closure{Note: r.ClosureNote, By: r.ClosedBy, At: timeFromPg(r.ClosedAt)}
	if r.ClosureReason != nil {
		c.Reason = *r.ClosureReason
	}
	return c
}

// deletedState is the before half of a deleted row: enough of the risk to
// say what was deleted once the row is gone.
func deletedState(r Risk) map[string]any {
	s := map[string]any{
		"title":       r.Title,
		"category_id": r.CategoryID,
		"methodology": r.Methodology,
		"treatment":   r.Treatment,
		"status":      r.Status,
	}
	if r.Status == dbx.RiskStatusClosed {
		s["closure_reason"] = r.Closure.Reason
	}
	return s
}
