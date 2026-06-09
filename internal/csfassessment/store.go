// Package csfassessment is the slice-515 domain store for the NIST CSF 2.0
// Tier / Profile assessment workflow. It owns the tenant-confidential
// assessment STATE (a tenant's Tier rating + Current/Target Profiles) that
// CSF 2.0 layers on top of the SHARED crosswalk reference data (the CSF
// Subcategory rows in framework_requirements + their fw_to_scf_edges to SCF
// anchors, landed by slices 480 + 514).
//
// JUDGMENT (slice 515 decisions-log D1): this is CSF-SPECIFIC, not a
// generalized maturity-assessment engine. The Tier enum (a fixed 1-4 ordinal
// with CSF-defined semantics) has no analog in ISO Annex A applicability or
// PCI compensating-controls; generalizing now would be speculative generality
// (Article VII Simplicity Gate, anti-criterion P0-515-4). The tables are
// already framework-pinned, so a future generalization is additive.
//
// Constitutional grounding:
//   - Invariant #1 / P0-515-2: the gap view does NOT duplicate the crosswalk.
//     A Profile stores only the tenant's per-Subcategory TARGET outcome
//     (csf_profile_selections, FK to the shared framework_requirements row);
//     the Subcategory↔SCF-anchor mapping + the Current coverage are derived at
//     read time by internal/api/ucfcoverage's requirement→anchor→coverage
//     traversal — never re-stored here.
//   - Invariant #6 / P0-515-1: every method runs inside a tenancy tx that sets
//     the app.current_tenant GUC so RLS isolates the read/write. The store
//     never adds a `WHERE tenant_id` clause to bypass RLS; the GUC + the
//     four-policy split on each table is the enforcement.
//   - Threat-model R: every mutating method appends a csf_assessment_audit row
//     in the SAME tx as the mutation (who set which Tier/selection, when,
//     against which CSF version).
//   - P0-515-3: a Tier is never auto-rated — RateTier takes an
//     operator-supplied tier; there is no inference path.
package csfassessment

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// Errors returned by Store operations; handlers map these to HTTP codes.
var (
	// ErrNotFound is returned when an id doesn't resolve under the active
	// tenant context (RLS-friendly: indistinguishable from "in another
	// tenant"). HTTP 404.
	ErrNotFound = errors.New("csfassessment: not found")
	// ErrInvalidTier is returned when a tier token isn't one of the four
	// canonical CSF tiers. HTTP 400.
	ErrInvalidTier = errors.New("csfassessment: invalid tier")
	// ErrInvalidKind is returned when a profile kind isn't current|target.
	// HTTP 400.
	ErrInvalidKind = errors.New("csfassessment: invalid profile kind")
	// ErrInvalidOutcome is returned when a selection outcome isn't one of the
	// four canonical target outcomes. HTTP 400.
	ErrInvalidOutcome = errors.New("csfassessment: invalid target outcome")
)

// Canonical token sets (mirror the DB enum + CHECK constraints). Validated in
// Go so a bad value is rejected with a 400 before it reaches the DB.

// ValidTiers is the canonical CSF 2.0 Tier set (1-4).
var ValidTiers = map[string]bool{
	"tier1_partial":       true,
	"tier2_risk_informed": true,
	"tier3_repeatable":    true,
	"tier4_adaptive":      true,
}

// tierOrdinal maps a tier token to its 1-4 ordinal for the gap delta.
var tierOrdinal = map[string]int{
	"tier1_partial":       1,
	"tier2_risk_informed": 2,
	"tier3_repeatable":    3,
	"tier4_adaptive":      4,
}

// ValidKinds is the canonical profile-kind set.
var ValidKinds = map[string]bool{
	"current": true,
	"target":  true,
}

// ValidOutcomes is the canonical per-Subcategory target-outcome set, with the
// ordinal used to compute a Current-vs-Target gap.
var ValidOutcomes = map[string]int{
	"not_targeted": 0,
	"partial":      1,
	"largely":      2,
	"fully":        3,
}

// TierRating is the Go surface for a csf_tier_ratings row.
type TierRating struct {
	ID                 uuid.UUID
	FrameworkVersionID uuid.UUID
	Tier               string
	Rationale          string
	RatedBy            string
	RatedAt            time.Time
}

// Profile is the Go surface for a csf_profiles row.
type Profile struct {
	ID                 uuid.UUID
	FrameworkVersionID uuid.UUID
	Kind               string
	Name               string
	CreatedBy          string
}

// Selection is one per-Subcategory target outcome inside a profile, joined to
// the shared CSF Subcategory code + title.
type Selection struct {
	SubcategoryCode  string
	SubcategoryTitle string
	RequirementID    uuid.UUID
	TargetOutcome    string
	Note             string
}

// GapRow is one Current-vs-Target gap row for a single Subcategory.
type GapRow struct {
	SubcategoryCode  string `json:"subcategory_code"`
	SubcategoryTitle string `json:"subcategory_title"`
	RequirementID    string `json:"requirement_id"`
	CurrentOutcome   string `json:"current_outcome"`
	TargetOutcome    string `json:"target_outcome"`
	// GapDelta = targetOrdinal - currentOrdinal. >0 means the target outcome
	// is above the current outcome (a gap to close); <=0 means met.
	GapDelta int  `json:"gap_delta"`
	Met      bool `json:"met"`
}

// Store wraps the sqlc Queries with the tenancy plumbing RLS requires. Same
// shape as internal/frameworkscope.Store: every method opens a tx, applies the
// tenant GUC, and runs queries inside that tx so RLS policies see the tenant.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore constructs a Store over an existing pgx pool. The pool MUST be
// connected as the application role (NOSUPERUSER NOBYPASSRLS) — RLS is
// unenforceable otherwise.
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// ----- Tier rating -----

// RateTierRequest is the input for RateTier.
type RateTierRequest struct {
	FrameworkVersionID uuid.UUID
	Tier               string // operator-supplied token (P0-515-3: never inferred)
	Rationale          string
	Actor              string // credential id / user that set the rating
}

// RateTier upserts the tenant's single Tier rating for a CSF framework_version
// and appends an audit row in the same tx (threat-model R). Returns the rating
// and whether it was a first-time rating (inserted) vs a re-rate (update).
func (s *Store) RateTier(ctx context.Context, req RateTierRequest) (TierRating, bool, error) {
	if !ValidTiers[req.Tier] {
		return TierRating{}, false, ErrInvalidTier
	}
	var out TierRating
	var inserted bool
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		row, err := q.UpsertCsfTierRating(ctx, dbx.UpsertCsfTierRatingParams{
			ID:                 pgUUID(uuid.New()),
			TenantID:           pgUUID(tenantID),
			FrameworkVersionID: pgUUID(req.FrameworkVersionID),
			Tier:               dbx.CsfTier(req.Tier),
			Rationale:          req.Rationale,
			RatedBy:            req.Actor,
		})
		if err != nil {
			return fmt.Errorf("upsert csf_tier_rating: %w", err)
		}
		inserted = row.Inserted
		action := "tier_rerated"
		if inserted {
			action = "tier_rated"
		}
		if err := s.audit(ctx, q, tenantID, req.FrameworkVersionID, "tier", uuidFromPG(row.ID), action, req.Actor, req.Tier); err != nil {
			return err
		}
		out = TierRating{
			ID:                 uuidFromPG(row.ID),
			FrameworkVersionID: uuidFromPG(row.FrameworkVersionID),
			Tier:               string(row.Tier),
			Rationale:          row.Rationale,
			RatedBy:            row.RatedBy,
			RatedAt:            row.RatedAt.Time,
		}
		return nil
	})
	return out, inserted, err
}

// GetTier returns the tenant's Tier rating for a framework_version, or
// ErrNotFound if none is set yet.
func (s *Store) GetTier(ctx context.Context, fvID uuid.UUID) (TierRating, error) {
	var out TierRating
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		row, err := q.GetCsfTierRating(ctx, dbx.GetCsfTierRatingParams{
			TenantID:           pgUUID(tenantID),
			FrameworkVersionID: pgUUID(fvID),
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("get csf_tier_rating: %w", err)
		}
		out = TierRating{
			ID:                 uuidFromPG(row.ID),
			FrameworkVersionID: uuidFromPG(row.FrameworkVersionID),
			Tier:               string(row.Tier),
			Rationale:          row.Rationale,
			RatedBy:            row.RatedBy,
			RatedAt:            row.RatedAt.Time,
		}
		return nil
	})
	return out, err
}

// ----- Profile -----

// EnsureProfileRequest is the input for EnsureProfile.
type EnsureProfileRequest struct {
	FrameworkVersionID uuid.UUID
	Kind               string // current|target
	Name               string
	Actor              string
}

// EnsureProfile upserts the tenant's single profile of a given kind for a
// framework_version. Idempotent: re-creating the same kind returns the existing
// profile (name refreshed). Appends a 'profile_created' audit row on first
// creation.
func (s *Store) EnsureProfile(ctx context.Context, req EnsureProfileRequest) (Profile, error) {
	if !ValidKinds[req.Kind] {
		return Profile{}, ErrInvalidKind
	}
	var out Profile
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		row, err := q.UpsertCsfProfile(ctx, dbx.UpsertCsfProfileParams{
			ID:                 pgUUID(uuid.New()),
			TenantID:           pgUUID(tenantID),
			FrameworkVersionID: pgUUID(req.FrameworkVersionID),
			Kind:               dbx.CsfProfileKind(req.Kind),
			Name:               req.Name,
			CreatedBy:          req.Actor,
		})
		if err != nil {
			return fmt.Errorf("upsert csf_profile: %w", err)
		}
		if row.Inserted {
			if err := s.audit(ctx, q, tenantID, req.FrameworkVersionID, "profile", uuidFromPG(row.ID), "profile_created", req.Actor, req.Kind); err != nil {
				return err
			}
		}
		out = Profile{
			ID:                 uuidFromPG(row.ID),
			FrameworkVersionID: uuidFromPG(row.FrameworkVersionID),
			Kind:               string(row.Kind),
			Name:               row.Name,
			CreatedBy:          row.CreatedBy,
		}
		return nil
	})
	return out, err
}

// GetProfile returns the tenant's profile of a kind for a framework_version, or
// ErrNotFound.
func (s *Store) GetProfile(ctx context.Context, fvID uuid.UUID, kind string) (Profile, error) {
	if !ValidKinds[kind] {
		return Profile{}, ErrInvalidKind
	}
	var out Profile
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		row, err := q.GetCsfProfile(ctx, dbx.GetCsfProfileParams{
			TenantID:           pgUUID(tenantID),
			FrameworkVersionID: pgUUID(fvID),
			Kind:               dbx.CsfProfileKind(kind),
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("get csf_profile: %w", err)
		}
		out = profileFromRow(row)
		return nil
	})
	return out, err
}

// ----- Selection -----

// SetSelectionRequest is the input for SetSelection.
type SetSelectionRequest struct {
	ProfileID     uuid.UUID
	RequirementID uuid.UUID // the shared CSF Subcategory row
	TargetOutcome string
	Note          string
	Actor         string
}

// SetSelection upserts the target outcome for one Subcategory inside a profile
// and appends a 'selection_set' audit row. The profile must belong to the
// caller's tenant (RLS-enforced; a cross-tenant profile id resolves to
// ErrNotFound).
func (s *Store) SetSelection(ctx context.Context, req SetSelectionRequest) (Selection, error) {
	if _, ok := ValidOutcomes[req.TargetOutcome]; !ok {
		return Selection{}, ErrInvalidOutcome
	}
	var out Selection
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		// Resolve the profile to confirm tenant ownership + capture the
		// framework_version for the audit row. RLS makes a foreign profile
		// invisible (ErrNoRows → ErrNotFound).
		prof, err := q.GetCsfProfileByID(ctx, dbx.GetCsfProfileByIDParams{
			TenantID: pgUUID(tenantID),
			ID:       pgUUID(req.ProfileID),
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("get csf_profile by id: %w", err)
		}
		row, err := q.UpsertCsfProfileSelection(ctx, dbx.UpsertCsfProfileSelectionParams{
			ID:                     pgUUID(uuid.New()),
			TenantID:               pgUUID(tenantID),
			CsfProfileID:           pgUUID(req.ProfileID),
			FrameworkRequirementID: pgUUID(req.RequirementID),
			TargetOutcome:          req.TargetOutcome,
			Note:                   req.Note,
		})
		if err != nil {
			return fmt.Errorf("upsert csf_profile_selection: %w", err)
		}
		if err := s.audit(ctx, q, tenantID, uuidFromPG(prof.FrameworkVersionID), "selection", uuidFromPG(row.ID), "selection_set", req.Actor, req.TargetOutcome); err != nil {
			return err
		}
		out = Selection{
			RequirementID: uuidFromPG(row.FrameworkRequirementID),
			TargetOutcome: row.TargetOutcome,
			Note:          row.Note,
		}
		return nil
	})
	return out, err
}

// ClearSelection removes one Subcategory selection from a profile and appends a
// 'selection_cleared' audit row. Returns ErrNotFound if the selection (or the
// profile) doesn't resolve under the tenant.
func (s *Store) ClearSelection(ctx context.Context, profileID, requirementID uuid.UUID, actor string) error {
	return s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		prof, err := q.GetCsfProfileByID(ctx, dbx.GetCsfProfileByIDParams{
			TenantID: pgUUID(tenantID),
			ID:       pgUUID(profileID),
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("get csf_profile by id: %w", err)
		}
		row, err := q.DeleteCsfProfileSelection(ctx, dbx.DeleteCsfProfileSelectionParams{
			TenantID:               pgUUID(tenantID),
			CsfProfileID:           pgUUID(profileID),
			FrameworkRequirementID: pgUUID(requirementID),
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("delete csf_profile_selection: %w", err)
		}
		return s.audit(ctx, q, tenantID, uuidFromPG(prof.FrameworkVersionID), "selection", uuidFromPG(row.ID), "selection_cleared", actor, "")
	})
}

// ListSelections returns a profile's per-Subcategory selections joined to the
// shared CSF Subcategory code + title, ordered by code.
func (s *Store) ListSelections(ctx context.Context, profileID uuid.UUID) ([]Selection, error) {
	var out []Selection
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		rows, err := q.ListCsfProfileSelectionsWithSubcategory(ctx, dbx.ListCsfProfileSelectionsWithSubcategoryParams{
			TenantID:     pgUUID(tenantID),
			CsfProfileID: pgUUID(profileID),
		})
		if err != nil {
			return fmt.Errorf("list csf_profile_selections: %w", err)
		}
		out = make([]Selection, len(rows))
		for i, r := range rows {
			out[i] = Selection{
				SubcategoryCode:  r.SubcategoryCode,
				SubcategoryTitle: r.SubcategoryTitle,
				RequirementID:    uuidFromPG(r.FrameworkRequirementID),
				TargetOutcome:    r.TargetOutcome,
				Note:             r.Note,
			}
		}
		return nil
	})
	return out, err
}

// ----- Gap view -----

// Gap computes the Current-vs-Target gap for a framework_version: for every
// Subcategory in the union of the Current + Target profiles, the current
// outcome vs the target outcome with a per-Subcategory delta. A Subcategory
// present in only one profile defaults the missing side to 'not_targeted'.
//
// This is a PURE function over the two selection sets — the
// Subcategory↔SCF-anchor coverage traversal is the API handler's job (it joins
// the ucfcoverage requirement rollup); keeping Gap pure makes it unit-testable
// without a DB and keeps the crosswalk read where it belongs (invariant #1).
func Gap(current, target []Selection) []GapRow {
	type pair struct {
		code, title, reqID string
		cur, tgt           string
	}
	byCode := map[string]*pair{}
	order := []string{}
	upsert := func(sel Selection, isTarget bool) {
		p, ok := byCode[sel.SubcategoryCode]
		if !ok {
			p = &pair{code: sel.SubcategoryCode, title: sel.SubcategoryTitle, reqID: sel.RequirementID.String(), cur: "not_targeted", tgt: "not_targeted"}
			byCode[sel.SubcategoryCode] = p
			order = append(order, sel.SubcategoryCode)
		}
		if isTarget {
			p.tgt = sel.TargetOutcome
		} else {
			p.cur = sel.TargetOutcome
		}
	}
	for _, c := range current {
		upsert(c, false)
	}
	for _, t := range target {
		upsert(t, true)
	}
	out := make([]GapRow, 0, len(order))
	for _, code := range order {
		p := byCode[code]
		curOrd := ValidOutcomes[p.cur]
		tgtOrd := ValidOutcomes[p.tgt]
		delta := tgtOrd - curOrd
		out = append(out, GapRow{
			SubcategoryCode:  p.code,
			SubcategoryTitle: p.title,
			RequirementID:    p.reqID,
			CurrentOutcome:   p.cur,
			TargetOutcome:    p.tgt,
			GapDelta:         delta,
			Met:              delta <= 0,
		})
	}
	return out
}

// TierGap returns the numeric delta (targetOrdinal - currentOrdinal) between two
// tier tokens, and whether both tokens are valid. Used by the gap view's
// Tier-level summary. A non-tier token yields ok=false.
func TierGap(current, target string) (delta int, ok bool) {
	c, cok := tierOrdinal[current]
	t, tok := tierOrdinal[target]
	if !cok || !tok {
		return 0, false
	}
	return t - c, true
}

// ----- audit + tx plumbing -----

// audit appends one csf_assessment_audit row inside the active tx.
func (s *Store) audit(ctx context.Context, q *dbx.Queries, tenantID, fvID uuid.UUID, subjectKind string, subjectID uuid.UUID, action, actor, detail string) error {
	_, err := q.InsertCsfAssessmentAudit(ctx, dbx.InsertCsfAssessmentAuditParams{
		ID:                 pgUUID(uuid.New()),
		TenantID:           pgUUID(tenantID),
		FrameworkVersionID: pgUUID(fvID),
		SubjectKind:        subjectKind,
		SubjectID:          pgUUID(subjectID),
		Action:             action,
		Actor:              actor,
		Detail:             detail,
	})
	if err != nil {
		return fmt.Errorf("insert csf_assessment_audit: %w", err)
	}
	return nil
}

func (s *Store) inTx(ctx context.Context, fn func(context.Context, *dbx.Queries, uuid.UUID) error) error {
	tenantStr, err := tenancy.TenantFromContext(ctx)
	if err != nil {
		return err
	}
	tenantID, err := uuid.Parse(tenantStr)
	if err != nil {
		return fmt.Errorf("csfassessment: parse tenant id: %w", err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("csfassessment: begin tx: %w", err)
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
		return fmt.Errorf("csfassessment: commit: %w", err)
	}
	return nil
}

func profileFromRow(r dbx.CsfProfile) Profile {
	return Profile{
		ID:                 uuidFromPG(r.ID),
		FrameworkVersionID: uuidFromPG(r.FrameworkVersionID),
		Kind:               string(r.Kind),
		Name:               r.Name,
		CreatedBy:          r.CreatedBy,
	}
}

func pgUUID(u uuid.UUID) pgtype.UUID { return pgtype.UUID{Bytes: u, Valid: true} }

func uuidFromPG(p pgtype.UUID) uuid.UUID { return p.Bytes }
