// Risk placement (#1749): a risk's hierarchy level and org unit.
//
// `risks.level` and `risks.org_unit_id` drive every hierarchy view, so
// POST /v1/risks accepts both and PATCH /v1/risks/{id} moves a risk between
// them. One rule holds on every path: a risk bound to an org unit sits at
// that unit's level. A risk with no org unit may sit at any level (smaller
// orgs may not configure org units at all).
//
// A move writes one risk_audit_log row in the same transaction, and runs
// the aggregation hooks for the risk's themes: the aggregation engine counts
// distinct org units toward a rule's min_teams threshold, so a move can
// cross it.

package risk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// ErrInvalidRiskLevel is returned when a risk's requested level is not in
// the closed risk_level enum.
var ErrInvalidRiskLevel = errors.New("risk: level must be one of: team, org, company")

// ErrOrgUnitNotFound is returned when the requested org_unit_id does not
// resolve within the active tenant.
var ErrOrgUnitNotFound = errors.New("risk: org_unit not found")

// ErrLevelMismatch is returned when the requested level differs from the
// level of the requested org unit.
var ErrLevelMismatch = errors.New("risk: level must match the org_unit's level")

// IsPlacementValidation reports whether err is a caller error in a risk's
// level or org unit, which the HTTP layer renders as a 400.
func IsPlacementValidation(err error) bool {
	return errors.Is(err, ErrInvalidRiskLevel) ||
		errors.Is(err, ErrOrgUnitNotFound) ||
		errors.Is(err, ErrLevelMismatch)
}

// resolveLevel returns the level a risk takes given the requested level
// (empty when the caller did not name one), the level of its org unit (nil
// when it has none), and the level to fall back to. A unit with no
// requested level gives the risk the unit's level; a requested level that
// differs from the unit's is ErrLevelMismatch.
func resolveLevel(requested dbx.RiskLevel, unitLevel *dbx.RiskLevel, fallback dbx.RiskLevel) (dbx.RiskLevel, error) {
	if requested != "" && !isValidLevel(requested) {
		return "", ErrInvalidRiskLevel
	}
	switch {
	case unitLevel == nil && requested == "":
		return fallback, nil
	case unitLevel == nil:
		return requested, nil
	case requested == "":
		return *unitLevel, nil
	case requested != *unitLevel:
		return "", fmt.Errorf("%w: level %q, org_unit level %q", ErrLevelMismatch, requested, *unitLevel)
	default:
		return requested, nil
	}
}

// orgUnitLevel reads the level of org unit id within the tenant. A nil id
// returns a nil level.
func orgUnitLevel(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID, id *uuid.UUID) (*dbx.RiskLevel, error) {
	if id == nil {
		return nil, nil
	}
	row, err := q.GetOrgUnitByID(ctx, dbx.GetOrgUnitByIDParams{
		TenantID: pgUUID(tenantID),
		ID:       pgUUID(*id),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("%w: %s", ErrOrgUnitNotFound, *id)
		}
		return nil, fmt.Errorf("get org_unit: %w", err)
	}
	return &row.Level, nil
}

// PlacementInput is the API shape for PATCH /v1/risks/{id}. Level empty
// keeps the current level, unless the org unit changes to a non-nil unit,
// in which case the risk takes that unit's level. OrgUnitSet false keeps
// the current org unit; OrgUnitSet true with a nil OrgUnitID unbinds it.
type PlacementInput struct {
	Level      dbx.RiskLevel
	OrgUnitSet bool
	OrgUnitID  *uuid.UUID
	// CategoryRef, when non-empty, moves the risk onto the tenant's
	// category with that slug or id (#1812). It must be assignable: an
	// unknown or archived category is refused (IsCategoryRefusal). A risk
	// already on an archived category keeps it until moved off.
	CategoryRef string
	// Fields are the record fields to change (#2104). Each edit that
	// changes something writes one fields_changed audit row.
	Fields RecordFields
	// Actor is recorded on the audit row. It must be non-empty.
	Actor string
}

// categoryState is the before/after shape of a category_changed
// risk_audit_log row.
type categoryState struct {
	CategoryID uuid.UUID `json:"category_id"`
	Slug       string    `json:"category"`
}

// placementState is the before/after shape of a risk_audit_log row.
type placementState struct {
	Level     dbx.RiskLevel `json:"level"`
	OrgUnitID *uuid.UUID    `json:"org_unit_id"`
}

// UpdatePlacement moves a risk to a new level and/or org unit, onto
// another category when in.CategoryRef is set, and changes the record
// fields in in.Fields (#2104, IsRecordValidation on refusal), recording each change in
// risk_audit_log. A request that changes nothing writes no row and returns
// the risk as it is. Returns ErrNotFound for an unknown risk, a placement
// validation error (IsPlacementValidation) for a bad level or unit, and a
// category refusal (IsCategoryRefusal) for a category that cannot be
// assigned.
func (s *Store) UpdatePlacement(ctx context.Context, riskID uuid.UUID, in PlacementInput) (Risk, error) {
	if in.Actor == "" {
		return Risk{}, errors.New("risk: placement actor is required")
	}
	var out Risk
	err := s.inTxRuleLocked(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID, lastAttempt bool) error {
		// Lock order (#1867, #2204): the vocabulary first, then the rules'
		// advisory locks, then the row, as AssignThemes and Delete do.
		var (
			category     Category
			categoryEnum dbx.RiskCategory
		)
		if in.CategoryRef != "" {
			var err error
			category, categoryEnum, err = ResolveCategoryTx(ctx, q, tenantID, in.CategoryRef)
			if err != nil {
				return err
			}
		}
		unlocked, err := q.GetRiskByID(ctx, dbx.GetRiskByIDParams{
			TenantID: pgUUID(tenantID),
			ID:       pgUUID(riskID),
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("get risk: %w", err)
		}
		preLocked := unlocked.Themes
		if err := s.runBeforeRiskWrite(ctx, q, tenantID, preLocked); err != nil {
			return err
		}
		row, err := q.LockRiskByID(ctx, dbx.LockRiskByIDParams{
			TenantID: pgUUID(tenantID),
			ID:       pgUUID(riskID),
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("lock risk: %w", err)
		}
		if err := s.checkRuleLocks(lastAttempt, preLocked, row.Themes); err != nil {
			return err
		}
		current := riskFromRow(row)
		before := placementState{Level: current.Level, OrgUnitID: current.OrgUnitID}

		after := before
		if in.OrgUnitSet {
			after.OrgUnitID = in.OrgUnitID
		}
		unitLevel, err := orgUnitLevel(ctx, q, tenantID, after.OrgUnitID)
		if err != nil {
			return err
		}
		requested := in.Level
		// Keeping the current unit and naming no level keeps the current
		// level; re-checking it against the unit would refuse a risk whose
		// unit's level was edited after it was placed.
		if requested == "" && !in.OrgUnitSet {
			requested = current.Level
			unitLevel = nil
		}
		after.Level, err = resolveLevel(requested, unitLevel, current.Level)
		if err != nil {
			return err
		}

		placementChanged := after.Level != before.Level || !sameUnit(after.OrgUnitID, before.OrgUnitID)
		categoryChanged := in.CategoryRef != "" && category.ID != current.CategoryID
		// Record fields are checked before any write, so a refused field
		// leaves placement and category untouched too.
		_, fieldsBefore, fieldsAfter, err := applyRecordFields(current, in.Fields)
		if err != nil {
			return err
		}
		fieldsChanged := len(fieldsAfter) > 0
		if !placementChanged && !categoryChanged && !fieldsChanged {
			return s.finishPatch(ctx, q, tenantID, current, &out, false)
		}

		if placementChanged {
			updated, err := q.UpdateRiskPlacement(ctx, dbx.UpdateRiskPlacementParams{
				TenantID:  pgUUID(tenantID),
				ID:        pgUUID(riskID),
				Level:     after.Level,
				OrgUnitID: pgUUIDPtr(after.OrgUnitID),
			})
			if err != nil {
				return fmt.Errorf("update risk placement: %w", err)
			}
			if err := insertRiskAudit(ctx, q, tenantID, riskID, in.Actor, "placement_changed", before, after); err != nil {
				return err
			}
			current = riskFromRow(updated)
		}
		if categoryChanged {
			byID, err := categoriesByID(ctx, q, tenantID)
			if err != nil {
				return err
			}
			oldCategory := categoryState{CategoryID: current.CategoryID, Slug: byID[current.CategoryID].Slug}
			updated, err := q.UpdateRiskCategory(ctx, dbx.UpdateRiskCategoryParams{
				TenantID:   pgUUID(tenantID),
				ID:         pgUUID(riskID),
				CategoryID: pgUUID(category.ID),
				Category:   categoryEnum,
			})
			if err != nil {
				return fmt.Errorf("update risk category: %w", err)
			}
			newCategory := categoryState{CategoryID: category.ID, Slug: category.Slug}
			if err := insertRiskAudit(ctx, q, tenantID, riskID, in.Actor, "category_changed", oldCategory, newCategory); err != nil {
				return err
			}
			current = riskFromRow(updated)
		}
		if fieldsChanged {
			// Re-apply onto the row as the writes above left it, so the
			// category enum and updated_at carried over are current.
			next, _, _, err := applyRecordFields(current, in.Fields)
			if err != nil {
				return err
			}
			if current, err = updateRecordTx(ctx, q, tenantID, next, in.Actor, fieldsBefore, fieldsAfter); err != nil {
				return err
			}
		}
		return s.finishPatch(ctx, q, tenantID, current, &out, true)
	})
	return out, err
}

// finishPatch fills the response shape of a PATCH (control links, category
// slug and name) into out and, when the risk changed, runs the aggregation
// hooks for its themes.
func (s *Store) finishPatch(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID, r Risk, out *Risk, changed bool) error {
	r, err := s.withLinks(ctx, q, tenantID, r)
	if err != nil {
		return err
	}
	if err := withCategoryNames(ctx, q, tenantID, &r); err != nil {
		return err
	}
	*out = r
	if !changed {
		return nil
	}
	return s.runAfterRiskWrite(ctx, q, tenantID, r.Themes)
}

// insertRiskAudit writes one risk_audit_log row in the write's transaction.
func insertRiskAudit(ctx context.Context, q *dbx.Queries, tenantID, riskID uuid.UUID, actor, action string, before, after any) error {
	beforeJSON, err := json.Marshal(before)
	if err != nil {
		return fmt.Errorf("marshal %s before: %w", action, err)
	}
	afterJSON, err := json.Marshal(after)
	if err != nil {
		return fmt.Errorf("marshal %s after: %w", action, err)
	}
	if err := q.InsertRiskAuditLog(ctx, dbx.InsertRiskAuditLogParams{
		TenantID:    pgUUID(tenantID),
		RiskID:      pgUUID(riskID),
		Actor:       actor,
		ActionType:  action,
		BeforeState: beforeJSON,
		AfterState:  afterJSON,
	}); err != nil {
		return fmt.Errorf("insert risk audit log: %w", err)
	}
	return nil
}

// PlacementAuditEntry is one risk_audit_log row: a placement, category or
// record-field change (#2104).
type PlacementAuditEntry struct {
	ID          uuid.UUID
	CreatedAt   time.Time
	Actor       string
	ActionType  string
	BeforeState json.RawMessage
	AfterState  json.RawMessage
}

// ListPlacementAudit returns a risk's audit rows, newest first. A risk
// that does not exist, or has been deleted, returns its trail (possibly
// empty): the trail outlives the risk.
func (s *Store) ListPlacementAudit(ctx context.Context, riskID uuid.UUID) ([]PlacementAuditEntry, error) {
	var out []PlacementAuditEntry
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		rows, err := q.ListRiskAuditLog(ctx, dbx.ListRiskAuditLogParams{
			TenantID: pgUUID(tenantID),
			RiskID:   pgUUID(riskID),
		})
		if err != nil {
			return fmt.Errorf("list risk audit log: %w", err)
		}
		out = make([]PlacementAuditEntry, len(rows))
		for i, r := range rows {
			out[i] = PlacementAuditEntry{
				ID:          uuid.UUID(r.ID.Bytes),
				CreatedAt:   r.CreatedAt.Time,
				Actor:       r.Actor,
				ActionType:  r.ActionType,
				BeforeState: json.RawMessage(r.BeforeState),
				AfterState:  json.RawMessage(r.AfterState),
			}
		}
		return nil
	})
	return out, err
}

// withLinks fills r.LinkedControlIDs so a write's response has the same
// shape as GET /v1/risks/{id}.
func (s *Store) withLinks(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID, r Risk) (Risk, error) {
	links, err := q.ListRiskControlLinks(ctx, dbx.ListRiskControlLinksParams{
		TenantID: pgUUID(tenantID),
		RiskID:   pgUUID(r.ID),
	})
	if err != nil {
		return Risk{}, fmt.Errorf("list risk control links: %w", err)
	}
	r.LinkedControlIDs = make([]uuid.UUID, len(links))
	for i, l := range links {
		r.LinkedControlIDs[i] = uuid.UUID(l.ControlID.Bytes)
	}
	return r, nil
}

func sameUnit(a, b *uuid.UUID) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}
