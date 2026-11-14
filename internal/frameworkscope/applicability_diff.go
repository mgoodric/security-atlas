package frameworkscope

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// Applicability diff (issue #1795, Statement of Applicability #1744). The
// approver of a revision in review sees what changed against the revision
// in force — the activated revision of the same framework version — instead
// of the whole decision list.

// Change kinds. A requirement whose decision and justification both changed
// is reported once, as decision_changed, with both sides.
const (
	ChangeAdded                = "added"
	ChangeRemoved              = "removed"
	ChangeDecisionChanged      = "decision_changed"
	ChangeJustificationChanged = "justification_changed"
)

// DecisionChange is one requirement whose decision differs between the
// baseline and the revision. Before is nil for added, After nil for removed.
type DecisionChange struct {
	RequirementID uuid.UUID
	Code          string
	Title         string
	Kind          string
	Before        *Decision
	After         *Decision
}

// ApplicabilityDiff compares a revision against the activated revision of
// its framework version. Baseline is nil when no other revision is
// activated; every decision on the revision is then added.
type ApplicabilityDiff struct {
	Scope            FrameworkScope
	Baseline         *FrameworkScope
	PredicateChanged bool
	Changes          []DecisionChange
}

// DiffDecisions lists the requirements whose decision differs between base
// and target, in target's (requirement) order. A requirement undecided on
// both sides is unchanged. Only decision and justification count: an edit
// to the implementation reference alone is not a change the approver signs.
func DiffDecisions(base, target []RequirementApplicability) []DecisionChange {
	before := make(map[uuid.UUID]*Decision, len(base))
	for _, r := range base {
		before[r.RequirementID] = r.Decision
	}
	out := []DecisionChange{}
	for _, r := range target {
		b, a := before[r.RequirementID], r.Decision
		var kind string
		switch {
		case b == nil && a == nil:
			continue
		case b == nil:
			kind = ChangeAdded
		case a == nil:
			kind = ChangeRemoved
		case b.Decision != a.Decision:
			kind = ChangeDecisionChanged
		case b.Justification != a.Justification:
			kind = ChangeJustificationChanged
		default:
			continue
		}
		out = append(out, DecisionChange{
			RequirementID: r.RequirementID,
			Code:          r.Code,
			Title:         r.Title,
			Kind:          kind,
			Before:        b,
			After:         a,
		})
	}
	return out
}

// DiffAgainstActivated returns the applicability and predicate diff between
// the revision and the activated revision of its framework version. When
// the revision is itself the activated one, there is no baseline.
func (s *Store) DiffAgainstActivated(ctx context.Context, scopeID uuid.UUID) (ApplicabilityDiff, error) {
	var out ApplicabilityDiff
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		row, err := q.GetFrameworkScopeByID(ctx, dbx.GetFrameworkScopeByIDParams{
			TenantID: pgUUID(tenantID),
			ID:       pgUUID(scopeID),
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("get framework_scope: %w", err)
		}
		sc := rowToScope(row)
		target, err := listApplicability(ctx, q, tenantID, sc.FrameworkVersionID, &sc)
		if err != nil {
			return err
		}
		var baseReqs []RequirementApplicability
		out = ApplicabilityDiff{Scope: sc}
		act, aerr := q.GetActivatedFrameworkScope(ctx, dbx.GetActivatedFrameworkScopeParams{
			TenantID:           pgUUID(tenantID),
			FrameworkVersionID: row.FrameworkVersionID,
		})
		switch {
		case aerr == nil && act.ID != row.ID:
			bs := rowToScope(act)
			base, berr := listApplicability(ctx, q, tenantID, bs.FrameworkVersionID, &bs)
			if berr != nil {
				return berr
			}
			baseReqs = base.Requirements
			out.Baseline = &bs
			out.PredicateChanged = bs.PredicateHash != sc.PredicateHash
		case aerr == nil, errors.Is(aerr, pgx.ErrNoRows):
		default:
			return fmt.Errorf("get activated: %w", aerr)
		}
		out.Changes = DiffDecisions(baseReqs, target.Requirements)
		return nil
	})
	return out, err
}
