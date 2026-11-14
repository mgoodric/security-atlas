package frameworkscope

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// Applicability decisions (issue #1787, Statement of Applicability #1744).
// A decision records, per requirement of the scope revision's framework
// version, whether the requirement is applicable or excluded. A requirement
// with no decision is undecided, and undecided means applicable.

// Decision values, mirroring the CHECK on framework_scope_applicability.
const (
	DecisionApplicable = "applicable"
	DecisionExcluded   = "excluded"
)

// ActionApplicabilityUpdate is the workflow-log action for a decision change.
const ActionApplicabilityUpdate = "applicability.update"

var (
	// ErrInvalidDecision is returned for a decision other than applicable or
	// excluded. HTTP 400.
	ErrInvalidDecision = errors.New("frameworkscope: decision must be `applicable` or `excluded`")
	// ErrJustificationRequired is returned when an exclusion has no
	// justification. The DB CHECK framework_scope_applicability_excluded_justified
	// enforces the same rule. HTTP 400.
	ErrJustificationRequired = errors.New("frameworkscope: a justification is required to exclude a requirement")
	// ErrRequirementNotFound is returned when the requirement is not in the
	// scope revision's framework version. HTTP 404.
	ErrRequirementNotFound = errors.New("frameworkscope: requirement not found in the scope's framework version")
	// ErrDecisionsFrozen is returned when a decision is written on an
	// activated or superseded revision. Draft a new revision instead. HTTP 409.
	ErrDecisionsFrozen = errors.New("frameworkscope: decisions on an activated or superseded scope are frozen")
)

// Decision is one recorded applicability decision.
type Decision struct {
	RequirementID     uuid.UUID
	Decision          string
	Justification     string
	ImplementationRef string
	UpdatedBy         string
	UpdatedAt         time.Time
}

// RequirementApplicability is one requirement of a framework version with
// its decision on a revision. Decision is nil when the requirement is
// undecided.
type RequirementApplicability struct {
	RequirementID uuid.UUID
	Code          string
	Title         string
	Decision      *Decision
}

// ApplicabilityCounts tallies a revision's requirements. Undecided
// requirements count as applicable for coverage but are reported apart so
// the operator knows how much of the SoA is left.
type ApplicabilityCounts struct {
	Total      int
	Applicable int
	Excluded   int
	Undecided  int
}

// Applicability is the decision list for one revision. Scope is nil when an
// as_of read finds no revision in force; every requirement is then undecided.
type Applicability struct {
	Scope        *FrameworkScope
	Requirements []RequirementApplicability
	Counts       ApplicabilityCounts
}

// SetDecisionRequest is the input for SetDecision.
type SetDecisionRequest struct {
	ScopeID           uuid.UUID
	RequirementID     uuid.UUID
	Decision          string
	Justification     string
	ImplementationRef string
	// Actor is the calling credential id, recorded on the decision and the
	// workflow log.
	Actor string
}

// ValidateDecision is the domain check for a decision write: the decision is
// one of the two values, and an exclusion carries a non-blank justification.
func ValidateDecision(decision, justification string) error {
	switch decision {
	case DecisionApplicable:
		return nil
	case DecisionExcluded:
		if strings.TrimSpace(justification) == "" {
			return ErrJustificationRequired
		}
		return nil
	default:
		return ErrInvalidDecision
	}
}

// SetDecision records one requirement's decision on a scope revision. On a
// revision in review or approved, the table's reset trigger sends it back to
// draft and clears the approval; invalidated reports that. The change is
// written to the workflow log in the same tx.
func (s *Store) SetDecision(ctx context.Context, req SetDecisionRequest) (dec Decision, scope FrameworkScope, invalidated bool, err error) {
	if verr := ValidateDecision(req.Decision, req.Justification); verr != nil {
		return Decision{}, FrameworkScope{}, false, verr
	}
	err = s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		before, gerr := q.GetFrameworkScopeByID(ctx, dbx.GetFrameworkScopeByIDParams{
			TenantID: pgUUID(tenantID),
			ID:       pgUUID(req.ScopeID),
		})
		if gerr != nil {
			if errors.Is(gerr, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("get framework_scope: %w", gerr)
		}
		if before.State == StateActivated || before.State == StateSuperseded {
			return ErrDecisionsFrozen
		}
		if _, rerr := q.GetFrameworkRequirementInVersion(ctx, dbx.GetFrameworkRequirementInVersionParams{
			ID:                 pgUUID(req.RequirementID),
			FrameworkVersionID: before.FrameworkVersionID,
		}); rerr != nil {
			if errors.Is(rerr, pgx.ErrNoRows) {
				return ErrRequirementNotFound
			}
			return fmt.Errorf("get framework_requirement: %w", rerr)
		}
		var prior *dbx.FrameworkScopeApplicability
		if p, perr := q.GetFrameworkScopeApplicability(ctx, dbx.GetFrameworkScopeApplicabilityParams{
			TenantID:               pgUUID(tenantID),
			FrameworkScopeID:       pgUUID(req.ScopeID),
			FrameworkRequirementID: pgUUID(req.RequirementID),
		}); perr == nil {
			prior = &p
		} else if !errors.Is(perr, pgx.ErrNoRows) {
			return fmt.Errorf("get prior decision: %w", perr)
		}
		row, uerr := q.UpsertFrameworkScopeApplicability(ctx, dbx.UpsertFrameworkScopeApplicabilityParams{
			TenantID:               pgUUID(tenantID),
			FrameworkScopeID:       pgUUID(req.ScopeID),
			FrameworkRequirementID: pgUUID(req.RequirementID),
			Decision:               req.Decision,
			Justification:          req.Justification,
			ImplementationRef:      req.ImplementationRef,
			UpdatedBy:              req.Actor,
		})
		if uerr != nil {
			return fmt.Errorf("upsert decision: %w", uerr)
		}
		after, aerr := q.GetFrameworkScopeByID(ctx, dbx.GetFrameworkScopeByIDParams{
			TenantID: pgUUID(tenantID),
			ID:       pgUUID(req.ScopeID),
		})
		if aerr != nil {
			return fmt.Errorf("re-read framework_scope: %w", aerr)
		}
		if lerr := logDecisionChange(ctx, q, req.Actor, before, after, prior, row); lerr != nil {
			return lerr
		}
		dec = rowToDecision(row)
		scope = rowToScope(after)
		invalidated = before.State != after.State && after.State == StateDraft
		return nil
	})
	return dec, scope, invalidated, err
}

// ListDecisions returns every requirement of the revision's framework
// version with its decision on that revision.
func (s *Store) ListDecisions(ctx context.Context, scopeID uuid.UUID) (Applicability, error) {
	var out Applicability
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
		out, err = listApplicability(ctx, q, tenantID, sc.FrameworkVersionID, &sc)
		return err
	})
	return out, err
}

// DecisionsAsOf returns the decisions in force at t for a framework version:
// those of the revision active then. Decisions on an activated or superseded
// revision are frozen, so that revision's rows are exactly what applied.
// With no revision in force, every requirement is undecided.
func (s *Store) DecisionsAsOf(ctx context.Context, frameworkVersionID uuid.UUID, t time.Time) (Applicability, error) {
	var out Applicability
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		var sc *FrameworkScope
		row, err := q.GetFrameworkScopeAsOf(ctx, dbx.GetFrameworkScopeAsOfParams{
			TenantID:           pgUUID(tenantID),
			FrameworkVersionID: pgUUID(frameworkVersionID),
			EffectiveFrom:      pgtype.Timestamptz{Time: t, Valid: true},
		})
		switch {
		case err == nil:
			v := rowToScope(row)
			sc = &v
		case errors.Is(err, pgx.ErrNoRows):
		default:
			return fmt.Errorf("get as_of: %w", err)
		}
		out, err = listApplicability(ctx, q, tenantID, frameworkVersionID, sc)
		return err
	})
	return out, err
}

func listApplicability(ctx context.Context, q *dbx.Queries, tenantID, frameworkVersionID uuid.UUID, sc *FrameworkScope) (Applicability, error) {
	var scopeID pgtype.UUID
	if sc != nil {
		scopeID = pgUUID(sc.ID)
	}
	rows, err := q.ListFrameworkScopeApplicability(ctx, dbx.ListFrameworkScopeApplicabilityParams{
		TenantID:           pgUUID(tenantID),
		FrameworkScopeID:   scopeID,
		FrameworkVersionID: pgUUID(frameworkVersionID),
	})
	if err != nil {
		return Applicability{}, fmt.Errorf("list applicability: %w", err)
	}
	out := Applicability{Scope: sc, Requirements: make([]RequirementApplicability, 0, len(rows))}
	for _, r := range rows {
		ra := RequirementApplicability{
			RequirementID: uuid.UUID(r.RequirementID.Bytes),
			Code:          r.Code,
			Title:         r.Title,
		}
		out.Counts.Total++
		if r.Decision == nil {
			out.Counts.Undecided++
		} else {
			d := Decision{
				RequirementID:     ra.RequirementID,
				Decision:          *r.Decision,
				Justification:     deref(r.Justification),
				ImplementationRef: deref(r.ImplementationRef),
				UpdatedBy:         deref(r.UpdatedBy),
			}
			if r.UpdatedAt.Valid {
				d.UpdatedAt = r.UpdatedAt.Time
			}
			ra.Decision = &d
			if d.Decision == DecisionExcluded {
				out.Counts.Excluded++
			} else {
				out.Counts.Applicable++
			}
		}
		out.Requirements = append(out.Requirements, ra)
	}
	return out, nil
}

// logDecisionChange appends the applicability.update workflow-log row. The
// before/after images are the scope row snapshots (so a reset to draft shows
// in from_state/to_state and the approval columns) with the decision under
// an `applicability` key; before's decision is null when the requirement was
// undecided.
func logDecisionChange(ctx context.Context, q *dbx.Queries, actor string, before, after dbx.FrameworkScope, prior *dbx.FrameworkScopeApplicability, row dbx.FrameworkScopeApplicability) error {
	beforeJSON, err := snapshotWithDecision(before, prior)
	if err != nil {
		return err
	}
	afterJSON, err := snapshotWithDecision(after, &row)
	if err != nil {
		return err
	}
	fromState := before.State
	if err := q.InsertFrameworkScopeWorkflowLog(ctx, dbx.InsertFrameworkScopeWorkflowLogParams{
		TenantID:         after.TenantID,
		FrameworkScopeID: after.ID,
		Action:           ActionApplicabilityUpdate,
		FromState:        &fromState,
		ToState:          after.State,
		Before:           beforeJSON,
		After:            afterJSON,
		Actor:            actor,
	}); err != nil {
		return fmt.Errorf("frameworkscope: write workflow log: %w", err)
	}
	return nil
}

func snapshotWithDecision(sc dbx.FrameworkScope, d *dbx.FrameworkScopeApplicability) ([]byte, error) {
	base, err := snapshot(sc)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(base, &m); err != nil {
		return nil, fmt.Errorf("frameworkscope: snapshot: %w", err)
	}
	if d == nil {
		m["applicability"] = nil
	} else {
		m["applicability"] = map[string]any{
			"framework_requirement_id": uuid.UUID(d.FrameworkRequirementID.Bytes),
			"decision":                 d.Decision,
			"justification":            d.Justification,
			"implementation_ref":       d.ImplementationRef,
		}
	}
	b, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("frameworkscope: snapshot: %w", err)
	}
	return b, nil
}

func rowToDecision(r dbx.FrameworkScopeApplicability) Decision {
	d := Decision{
		RequirementID:     uuid.UUID(r.FrameworkRequirementID.Bytes),
		Decision:          r.Decision,
		Justification:     r.Justification,
		ImplementationRef: r.ImplementationRef,
		UpdatedBy:         r.UpdatedBy,
	}
	if r.UpdatedAt.Valid {
		d.UpdatedAt = r.UpdatedAt.Time
	}
	return d
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
