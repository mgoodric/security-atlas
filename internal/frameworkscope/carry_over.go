package frameworkscope

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// Applicability carry-over (#1793, Statement of Applicability #1744).
//
// When a framework moves from version N to N+1, the version-migration queue
// (ADR 0019) holds one row per requirement: an exact-code match between the
// two versions, or a requirement added in N+1 or removed from N. A
// super_admin approves each exact-code match. That queue is catalog-wide, so
// approving a row does not write any tenant's decisions; it decides what a
// tenant may carry. CarryOverDecisions is the tenant's own act, run under its
// RLS: it copies its N decisions for approved matches into its draft revision
// on N+1. Everything else is reported and left undecided.

// Reasons reported for a queue row that was not copied.
const (
	// CarryOverAlreadyDecided: the draft already has a decision for the
	// requirement. It is left alone.
	CarryOverAlreadyDecided = "already_decided"
	// CarryOverSourceUndecided: the requirement has no decision on N, so
	// there is nothing to copy.
	CarryOverSourceUndecided = "source_undecided"
	// CarryOverPending: the carry-over has not been reviewed yet.
	CarryOverPending = "carry_over_pending"
	// CarryOverRejected: a reviewer rejected the carry-over.
	CarryOverRejected = "carry_over_rejected"
	// CarryOverNewRequirement: the requirement is new in N+1. It stays
	// undecided.
	CarryOverNewRequirement = "new_requirement"
	// CarryOverRemovedRequirement: the requirement is not in N+1.
	CarryOverRemovedRequirement = "removed_requirement"
)

var (
	// ErrCarryOverNotDraft is returned when the target revision is not a
	// draft. Carried-over decisions land only in a draft. HTTP 409.
	ErrCarryOverNotDraft = errors.New("frameworkscope: decisions carry over only into a draft revision")
	// ErrNoCarryOverSource is returned when the version-migration queue has
	// no rows into the revision's framework version, so there is no prior
	// version to carry from. HTTP 409.
	ErrNoCarryOverSource = errors.New("frameworkscope: no version migration leads into this revision's framework version")
	// ErrAmbiguousCarryOverSource is returned when the queue leads into the
	// revision's framework version from more than one prior version. HTTP 409.
	ErrAmbiguousCarryOverSource = errors.New("frameworkscope: version migrations lead into this revision's framework version from more than one version")
)

// carrySource is recorded under `carried_over_from` in the workflow-log
// after image of each carried decision.
type carrySource struct {
	FrameworkVersionID     uuid.UUID `json:"framework_version_id"`
	FrameworkVersion       string    `json:"framework_version"`
	FrameworkScopeID       uuid.UUID `json:"framework_scope_id"`
	FrameworkRequirementID uuid.UUID `json:"framework_requirement_id"`
	MigrationID            uuid.UUID `json:"migration_id"`
}

// CarryOverItem is one queue row that was not copied, with the reason.
type CarryOverItem struct {
	RequirementCode   string
	FromRequirementID *uuid.UUID
	ToRequirementID   *uuid.UUID
	Reason            string
}

// CarryOverResult reports a carry-over. SourceScope is nil when the tenant
// has no revision on the source version; nothing is copied then.
type CarryOverResult struct {
	Scope           FrameworkScope
	SourceVersionID uuid.UUID
	SourceVersion   string
	SourceScope     *FrameworkScope
	Copied          []Decision
	Skipped         []CarryOverItem
	Flagged         []CarryOverItem
}

// CarryOverDecisions copies the tenant's decisions on the prior framework
// version into the draft revision scopeID, for every requirement whose
// carry-over is approved in the version-migration queue. The source is the
// tenant's activated revision on the prior version, or its newest revision
// there when none is activated. Decision, justification and implementation
// reference are copied; each copy is logged as applicability.carry_over
// naming its source. Decisions on the prior version are never written.
//
// It is idempotent: a requirement the draft already decides is left alone
// and reported as already_decided, so a re-run writes nothing.
func (s *Store) CarryOverDecisions(ctx context.Context, scopeID uuid.UUID, actor string) (CarryOverResult, error) {
	var out CarryOverResult
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		target, err := q.GetFrameworkScopeByID(ctx, dbx.GetFrameworkScopeByIDParams{
			TenantID: pgUUID(tenantID),
			ID:       pgUUID(scopeID),
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("get framework_scope: %w", err)
		}
		if target.State != StateDraft {
			return ErrCarryOverNotDraft
		}
		out.Scope = rowToScope(target)

		migrations, err := q.ListFrameworkVersionMigrationsInto(ctx, target.FrameworkVersionID)
		if err != nil {
			return fmt.Errorf("list version migrations: %w", err)
		}
		if len(migrations) == 0 {
			return ErrNoCarryOverSource
		}
		sourceVersion := migrations[0].FromVersionID
		for _, m := range migrations[1:] {
			if m.FromVersionID != sourceVersion {
				return ErrAmbiguousCarryOverSource
			}
		}
		out.SourceVersionID = uuid.UUID(sourceVersion.Bytes)
		label, err := q.GetFrameworkVersionLabel(ctx, dbx.GetFrameworkVersionLabelParams{
			FrameworkVersionID: sourceVersion,
			TenantID:           pgUUID(tenantID),
		})
		if err != nil {
			return fmt.Errorf("get source framework version: %w", err)
		}
		out.SourceVersion = label.FrameworkVersion

		source, err := sourceRevision(ctx, q, tenantID, sourceVersion)
		if err != nil {
			return err
		}
		if source != nil {
			sc := rowToScope(*source)
			out.SourceScope = &sc
		}

		var items []DecisionInput
		for _, m := range migrations {
			item := CarryOverItem{
				RequirementCode:   m.RequirementCode,
				FromRequirementID: optUUID(m.FromRequirementID),
				ToRequirementID:   optUUID(m.ToRequirementID),
			}
			switch {
			case m.MatchKind == dbx.FrameworkVersionMigrationMatchKindAdded:
				item.Reason = CarryOverNewRequirement
				out.Flagged = append(out.Flagged, item)
				continue
			case m.MatchKind == dbx.FrameworkVersionMigrationMatchKindRemoved:
				item.Reason = CarryOverRemovedRequirement
				out.Flagged = append(out.Flagged, item)
				continue
			case m.Status == dbx.FrameworkVersionMigrationStatusPending:
				item.Reason = CarryOverPending
				out.Flagged = append(out.Flagged, item)
				continue
			case m.Status == dbx.FrameworkVersionMigrationStatusRejected:
				item.Reason = CarryOverRejected
				out.Flagged = append(out.Flagged, item)
				continue
			}

			// An approved exact-code match.
			if source == nil {
				item.Reason = CarryOverSourceUndecided
				out.Skipped = append(out.Skipped, item)
				continue
			}
			from, ferr := q.GetFrameworkScopeApplicability(ctx, dbx.GetFrameworkScopeApplicabilityParams{
				TenantID:               pgUUID(tenantID),
				FrameworkScopeID:       source.ID,
				FrameworkRequirementID: m.FromRequirementID,
			})
			if errors.Is(ferr, pgx.ErrNoRows) {
				item.Reason = CarryOverSourceUndecided
				out.Skipped = append(out.Skipped, item)
				continue
			}
			if ferr != nil {
				return fmt.Errorf("get source decision: %w", ferr)
			}
			_, terr := q.GetFrameworkScopeApplicability(ctx, dbx.GetFrameworkScopeApplicabilityParams{
				TenantID:               pgUUID(tenantID),
				FrameworkScopeID:       target.ID,
				FrameworkRequirementID: m.ToRequirementID,
			})
			if terr == nil {
				item.Reason = CarryOverAlreadyDecided
				out.Skipped = append(out.Skipped, item)
				continue
			}
			if !errors.Is(terr, pgx.ErrNoRows) {
				return fmt.Errorf("get draft decision: %w", terr)
			}
			items = append(items, DecisionInput{
				RequirementID:     uuid.UUID(m.ToRequirementID.Bytes),
				Decision:          from.Decision,
				Justification:     from.Justification,
				ImplementationRef: from.ImplementationRef,
				carriedFrom: &carrySource{
					FrameworkVersionID:     out.SourceVersionID,
					FrameworkVersion:       out.SourceVersion,
					FrameworkScopeID:       uuid.UUID(source.ID.Bytes),
					FrameworkRequirementID: uuid.UUID(m.FromRequirementID.Bytes),
					MigrationID:            uuid.UUID(m.ID.Bytes),
				},
			})
		}
		if len(items) == 0 {
			return nil
		}
		decs, last, werr := writeDecisions(ctx, q, tenantID, target, items, actor)
		if werr != nil {
			return werr
		}
		out.Copied = decs
		out.Scope = rowToScope(last)
		return nil
	})
	return out, err
}

// sourceRevision picks the tenant's revision on the source version whose
// decisions carry: the activated one, else the newest. Nil when the tenant
// has none.
func sourceRevision(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID, versionID pgtype.UUID) (*dbx.FrameworkScope, error) {
	rows, err := q.ListFrameworkScopesByFrameworkVersion(ctx, dbx.ListFrameworkScopesByFrameworkVersionParams{
		TenantID:           pgUUID(tenantID),
		FrameworkVersionID: versionID,
	})
	if err != nil {
		return nil, fmt.Errorf("list source revisions: %w", err)
	}
	if len(rows) == 0 {
		return nil, nil
	}
	for i := range rows {
		if rows[i].State == StateActivated {
			return &rows[i], nil
		}
	}
	// Newest first.
	return &rows[0], nil
}

func optUUID(u pgtype.UUID) *uuid.UUID {
	if !u.Valid {
		return nil
	}
	v := uuid.UUID(u.Bytes)
	return &v
}
