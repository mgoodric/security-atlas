package risk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// ErrNotFound is returned when a tenant-scoped lookup yields zero rows. Stays
// a sentinel (not pgx.ErrNoRows) so callers do not need to import pgx.
var ErrNotFound = errors.New("risk: not found")

// ErrControlNotFound is returned by LinkControl when the control id does not
// resolve within the active tenant. Distinct from ErrNotFound (the risk) so
// the HTTP layer can phrase the 404 precisely. Slice 020.
var ErrControlNotFound = errors.New("risk: control not found")

// ErrLinkWeightOutOfRange is returned by LinkControl when a supplied
// design_score or weight is outside [0,1]. The DB CHECK constraints
// (migration `_029`) are the defense-in-depth peer; this is the primary
// validation so the API returns a 400 rather than a raw 23514. Slice 020.
var ErrLinkWeightOutOfRange = errors.New("risk: link weight must be between 0 and 1")

// Store wraps the sqlc Queries with the tenancy plumbing required for RLS.
// Same shape as scope.Store: every method opens a tx, applies the tenant GUC,
// runs queries inside that transaction.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore constructs a Store over an existing pgx pool. The pool must be
// connected as `atlas_app` (NOSUPERUSER NOBYPASSRLS) for RLS to fire.
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// Risk is the domain shape returned from store calls. It mirrors the sqlc Risk
// but exposes more idiomatic Go types (uuid.UUID, time.Time, json.RawMessage).
type Risk struct {
	ID                  uuid.UUID
	TenantID            uuid.UUID
	Title               string
	Description         string
	Category            dbx.RiskCategory
	Methodology         dbx.RiskMethodology
	InherentScore       []byte
	Treatment           dbx.RiskTreatment
	TreatmentOwner      string
	ResidualScore       []byte
	ReviewDueAt         *time.Time
	CreatedAt           time.Time
	UpdatedAt           time.Time
	AcceptedUntil       *time.Time
	Accepter            string
	InstrumentReference string
	LinkedControlIDs    []uuid.UUID
	// Slice 052 schema additions, surfaced for slice 053 callers (theme
	// tagging, aggregation, org-unit binding). Existing slice-019 risks
	// have `Level=team`, `OrgUnitID=nil`, `Themes=nil`.
	Level     dbx.RiskLevel
	OrgUnitID *uuid.UUID
	Themes    []string
}

// CreateInput is the API-shape for POST /v1/risks. The store re-validates
// every rule the handler should have validated already; defense in depth.
type CreateInput struct {
	Title               string
	Description         string
	Category            dbx.RiskCategory
	Methodology         dbx.RiskMethodology
	InherentScore       []byte
	Treatment           dbx.RiskTreatment
	TreatmentOwner      string
	ResidualScore       []byte
	ReviewDueAt         *time.Time
	AcceptedUntil       *time.Time
	Accepter            string
	InstrumentReference string
	LinkedControlIDs    []uuid.UUID
}

// Create inserts a new risk row plus its control links. Methodology defaults
// to DefaultMethodology if empty. Returns ErrInvalidMethodology /
// ErrInherentScoreInvalid / ErrTreatmentValidation on validation failure.
func (s *Store) Create(ctx context.Context, in CreateInput) (Risk, error) {
	if in.Methodology == "" {
		in.Methodology = DefaultMethodology
	}
	if err := ValidateInherentScore(in.Methodology, in.InherentScore); err != nil {
		return Risk{}, err
	}
	if err := ValidateTreatment(TreatmentInput{
		Treatment:            in.Treatment,
		AcceptedUntilPresent: in.AcceptedUntil != nil,
		Accepter:             in.Accepter,
		InstrumentReference:  in.InstrumentReference,
		LinkedControlCount:   len(in.LinkedControlIDs),
	}); err != nil {
		return Risk{}, err
	}

	var out Risk
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		id := uuid.New()
		row, err := q.CreateRisk(ctx, dbx.CreateRiskParams{
			ID:                  pgUUID(id),
			TenantID:            pgUUID(tenantID),
			Title:               in.Title,
			Description:         in.Description,
			Category:            in.Category,
			Methodology:         in.Methodology,
			InherentScore:       in.InherentScore,
			Treatment:           in.Treatment,
			TreatmentOwner:      in.TreatmentOwner,
			ResidualScore:       defaultResidual(in.ResidualScore),
			ReviewDueAt:         pgTimestamptzPtr(in.ReviewDueAt),
			AcceptedUntil:       pgDatePtr(in.AcceptedUntil),
			Accepter:            in.Accepter,
			InstrumentReference: in.InstrumentReference,
		})
		if err != nil {
			return fmt.Errorf("create risk: %w", err)
		}

		for _, ctrlID := range in.LinkedControlIDs {
			if err := q.LinkRiskControl(ctx, dbx.LinkRiskControlParams{
				RiskID:    row.ID,
				ControlID: pgUUID(ctrlID),
				TenantID:  pgUUID(tenantID),
			}); err != nil {
				return fmt.Errorf("link risk to control %s: %w", ctrlID, err)
			}
		}
		out = riskFromRow(row)
		out.LinkedControlIDs = append([]uuid.UUID(nil), in.LinkedControlIDs...)
		return nil
	})
	return out, err
}

// Get returns a single risk by id. ErrNotFound if absent.
func (s *Store) Get(ctx context.Context, id uuid.UUID) (Risk, error) {
	var out Risk
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		row, err := q.GetRiskByID(ctx, dbx.GetRiskByIDParams{
			TenantID: pgUUID(tenantID),
			ID:       pgUUID(id),
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("get risk: %w", err)
		}
		out = riskFromRow(row)
		links, err := q.ListRiskControlLinks(ctx, dbx.ListRiskControlLinksParams{
			TenantID: pgUUID(tenantID),
			RiskID:   pgUUID(id),
		})
		if err != nil {
			return fmt.Errorf("list control links: %w", err)
		}
		out.LinkedControlIDs = make([]uuid.UUID, len(links))
		for i, l := range links {
			out.LinkedControlIDs[i] = uuid.UUID(l.ControlID.Bytes)
		}
		return nil
	})
	return out, err
}

// ListSort selects the result ordering of List. The empty value keeps the
// default ListRisks order (created_at DESC, id ASC). Slice 066 adds
// SortResidualAge for the program dashboard's "top risks aging" panel.
type ListSort string

const (
	// SortDefault is the ListRisks SQL order: newest-first.
	SortDefault ListSort = ""
	// SortResidualAge ranks by residual-score magnitude descending, then
	// risk age ascending (oldest in treatment first). Slice 066 AC-3 — the
	// canvas §7.5 "top risks aging" ranking ("residual × age-since-
	// treatment"). residual_score is the opaque {likelihood, impact} JSONB;
	// the magnitude is likelihood × impact. A risk whose residual_score has
	// no numeric likelihood/impact sorts as magnitude 0 (it ranks below any
	// scored risk) rather than erroring — the dashboard ranking degrades
	// gracefully for a malformed score.
	SortResidualAge ListSort = "residual,age"
)

// ParseListSort maps the ?sort= query value to a ListSort. An empty value
// is SortDefault. An unrecognized value returns ErrInvalidSort so the HTTP
// layer can 400 rather than silently ignoring a typo'd sort.
func ParseListSort(raw string) (ListSort, error) {
	switch ListSort(raw) {
	case SortDefault:
		return SortDefault, nil
	case SortResidualAge:
		return SortResidualAge, nil
	default:
		return SortDefault, ErrInvalidSort
	}
}

// ErrInvalidSort is returned by ParseListSort for an unrecognized ?sort=
// value. The HTTP layer maps it to 400.
var ErrInvalidSort = errors.New("risk: sort must be empty or 'residual,age'")

// ListFilter narrows and orders the result set of List. Empty filter
// fields are ignored; an empty Sort keeps the default ListRisks order.
type ListFilter struct {
	Treatment   dbx.RiskTreatment
	Category    dbx.RiskCategory
	Methodology dbx.RiskMethodology
	// Sort selects the result ordering. Slice 066 (AC-3): additive — the
	// slice-019 callers that leave it zero get the unchanged default order.
	Sort ListSort
	// Theme, when non-empty, restricts to risks carrying that theme slug
	// in their themes array. Slice 067 (AC-4): additive — powers slice
	// 056's heatmap-cell-click side panel. Composes with every other
	// filter and with Sort.
	Theme string
	// OrgUnitID, when non-nil, restricts to risks attributed to that
	// org_unit. Slice 067 (AC-4): additive — the other half of the
	// heatmap-cell-click filter (a cell is a (theme, org_unit) pair).
	OrgUnitID *uuid.UUID
}

// List returns all risks for the active tenant, optionally filtered and
// ordered. Filtering and the optional residual/age sort are in-memory to
// keep sqlc queries static; the row count is bounded by tenant-size and v1
// cardinality is small (canvas §1.1 — solo security lead).
func (s *Store) List(ctx context.Context, filter ListFilter) ([]Risk, error) {
	var out []Risk
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		rows, err := q.ListRisks(ctx, pgUUID(tenantID))
		if err != nil {
			return fmt.Errorf("list risks: %w", err)
		}
		out = make([]Risk, 0, len(rows))
		for _, r := range rows {
			if filter.Treatment != "" && r.Treatment != filter.Treatment {
				continue
			}
			if filter.Category != "" && r.Category != filter.Category {
				continue
			}
			if filter.Methodology != "" && r.Methodology != filter.Methodology {
				continue
			}
			// Slice 067 (AC-4): theme + org_unit filters. Theme matches
			// when the slug is an element of the risk's themes array;
			// org_unit matches when the risk is attributed to that unit.
			// Both are additive and compose with the filters above. Theme
			// slugs are stored normalised (trimmed, lowercased) by slice
			// 053's AssignThemes, so an exact-element match is correct.
			if filter.Theme != "" && !slices.Contains(r.Themes, filter.Theme) {
				continue
			}
			if filter.OrgUnitID != nil {
				if !r.OrgUnitID.Valid || uuid.UUID(r.OrgUnitID.Bytes) != *filter.OrgUnitID {
					continue
				}
			}
			out = append(out, riskFromRow(r))
		}
		if filter.Sort == SortResidualAge {
			sortByResidualThenAge(out)
		}
		return nil
	})
	return out, err
}

// sortByResidualThenAge orders risks by residual-score magnitude descending
// (the riskiest first), then by created_at ascending (the oldest in
// treatment first). It is a stable sort so risks tied on both keys keep the
// underlying ListRisks order. Slice 066 AC-3.
//
// Decorate-sort-undecorate: each risk is paired with its magnitude once
// (so the residual_score JSONB is unmarshalled n times, not the O(n log n)
// times an unmarshal inside the comparator would cost), the paired slice is
// sorted, then the risks are written back in order.
func sortByResidualThenAge(risks []Risk) {
	type scored struct {
		risk Risk
		mag  float64
	}
	decorated := make([]scored, len(risks))
	for i, r := range risks {
		decorated[i] = scored{risk: r, mag: residualMagnitude(r.ResidualScore)}
	}
	sort.SliceStable(decorated, func(i, j int) bool {
		if decorated[i].mag != decorated[j].mag {
			return decorated[i].mag > decorated[j].mag // higher residual ranks first
		}
		return decorated[i].risk.CreatedAt.Before(decorated[j].risk.CreatedAt) // older ranks first
	})
	for i, d := range decorated {
		risks[i] = d.risk
	}
}

// residualMagnitude extracts a sortable scalar from the opaque
// residual_score JSONB. The v1 residual shape is {likelihood, impact}
// (canvas §2.2, the 5x5 grid); the magnitude is their product. A score
// missing either numeric field yields 0 — a malformed or empty score ranks
// below every properly-scored risk rather than erroring the whole list.
func residualMagnitude(raw []byte) float64 {
	if len(raw) == 0 {
		return 0
	}
	var score struct {
		Likelihood *float64 `json:"likelihood"`
		Impact     *float64 `json:"impact"`
	}
	if err := json.Unmarshal(raw, &score); err != nil {
		return 0
	}
	if score.Likelihood == nil || score.Impact == nil {
		return 0
	}
	return *score.Likelihood * *score.Impact
}

// HeatmapCell is one (likelihood, impact) bucket in the 5x5 grid.
type HeatmapCell struct {
	Likelihood int
	Impact     int
	Count      int
}

// Heatmap returns the 5x5 bucket counts for risks using nist_800_30 or
// qualitative_5x5. Cells with zero risks are omitted; the API renders a full
// 5x5 grid from the sparse return.
func (s *Store) Heatmap(ctx context.Context) ([]HeatmapCell, error) {
	var out []HeatmapCell
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		rows, err := q.HeatmapBuckets(ctx, pgUUID(tenantID))
		if err != nil {
			return fmt.Errorf("heatmap: %w", err)
		}
		out = make([]HeatmapCell, len(rows))
		for i, r := range rows {
			out[i] = HeatmapCell{
				Likelihood: int(r.Likelihood),
				Impact:     int(r.Impact),
				Count:      int(r.Count),
			}
		}
		return nil
	})
	return out, err
}

// Delete removes a risk by id. ErrNotFound if no row was matched.
func (s *Store) Delete(ctx context.Context, id uuid.UUID) error {
	return s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		// Check existence first so we can return ErrNotFound rather than a
		// silent success on a missing row.
		if _, err := q.GetRiskByID(ctx, dbx.GetRiskByIDParams{
			TenantID: pgUUID(tenantID),
			ID:       pgUUID(id),
		}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("delete risk get: %w", err)
		}
		if err := q.DeleteRisk(ctx, dbx.DeleteRiskParams{
			TenantID: pgUUID(tenantID),
			ID:       pgUUID(id),
		}); err != nil {
			return fmt.Errorf("delete risk: %w", err)
		}
		return nil
	})
}

// LinkControlInput is the API-shape for POST /v1/risks/{id}/controls. The
// three weights + design_score are optional: when DesignScoreSet / WeightsSet
// are false the migration `_029` column DEFAULTs apply (design 0.5, weights
// 0.3/0.5/0.2). Slice 020.
type LinkControlInput struct {
	RiskID    uuid.UUID
	ControlID uuid.UUID

	DesignScore    float64
	DesignScoreSet bool

	WeightDesign    float64
	WeightOperation float64
	WeightCoverage  float64
	WeightsSet      bool
}

// LinkControl links a control to a risk (AC-1). Validates that BOTH the risk
// and the control exist within the active tenant before writing the link —
// the composite FK on risk_control_links would also reject a bad id, but
// resolving here lets the HTTP layer return a precise 404 (risk vs control)
// instead of a raw 23503. Idempotent: re-linking the same control updates the
// per-link weights rather than 23505-ing.
//
// When no weights are supplied (DesignScoreSet / WeightsSet both false), the
// slice-019 weight-free LinkRiskControl query is used so the migration `_029`
// column DEFAULTs apply. When any are supplied, LinkRiskControlWithWeights
// writes the explicit values (the unsupplied ones in that group default to
// their migration `_029` value via the input zero-value being overridden
// below).
func (s *Store) LinkControl(ctx context.Context, in LinkControlInput) error {
	// Primary validation — DB CHECK is defense-in-depth.
	for _, v := range []struct {
		set bool
		val float64
	}{
		{in.DesignScoreSet, in.DesignScore},
		{in.WeightsSet, in.WeightDesign},
		{in.WeightsSet, in.WeightOperation},
		{in.WeightsSet, in.WeightCoverage},
	} {
		if v.set && (v.val < 0 || v.val > 1) {
			return ErrLinkWeightOutOfRange
		}
	}

	return s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		// The risk must exist in-tenant (AC-1: link on unknown risk -> 404).
		if _, err := q.GetRiskByID(ctx, dbx.GetRiskByIDParams{
			TenantID: pgUUID(tenantID),
			ID:       pgUUID(in.RiskID),
		}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("link control: get risk: %w", err)
		}
		// The control must exist in-tenant (AC-1: link unknown control -> 404).
		if _, err := q.GetControlByID(ctx, dbx.GetControlByIDParams{
			TenantID: pgUUID(tenantID),
			ID:       pgUUID(in.ControlID),
		}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrControlNotFound
			}
			return fmt.Errorf("link control: get control: %w", err)
		}

		if !in.DesignScoreSet && !in.WeightsSet {
			// No weights supplied — let the migration `_029` DEFAULTs apply.
			if err := q.LinkRiskControl(ctx, dbx.LinkRiskControlParams{
				RiskID:    pgUUID(in.RiskID),
				ControlID: pgUUID(in.ControlID),
				TenantID:  pgUUID(tenantID),
			}); err != nil {
				return fmt.Errorf("link control: %w", err)
			}
			return nil
		}

		// At least one weight supplied — write explicit values. Unsupplied
		// values in the group fall back to the migration `_029` defaults so a
		// caller setting only design_score does not zero the weights.
		design := defaultIfUnset(in.DesignScoreSet, in.DesignScore, 0.5)
		wDesign := defaultIfUnset(in.WeightsSet, in.WeightDesign, 0.3)
		wOper := defaultIfUnset(in.WeightsSet, in.WeightOperation, 0.5)
		wCover := defaultIfUnset(in.WeightsSet, in.WeightCoverage, 0.2)

		designN, err := floatToNumeric(design)
		if err != nil {
			return err
		}
		wDesignN, err := floatToNumeric(wDesign)
		if err != nil {
			return err
		}
		wOperN, err := floatToNumeric(wOper)
		if err != nil {
			return err
		}
		wCoverN, err := floatToNumeric(wCover)
		if err != nil {
			return err
		}
		if err := q.LinkRiskControlWithWeights(ctx, dbx.LinkRiskControlWithWeightsParams{
			RiskID:          pgUUID(in.RiskID),
			ControlID:       pgUUID(in.ControlID),
			TenantID:        pgUUID(tenantID),
			DesignScore:     designN,
			WeightDesign:    wDesignN,
			WeightOperation: wOperN,
			WeightCoverage:  wCoverN,
		}); err != nil {
			return fmt.Errorf("link control with weights: %w", err)
		}
		return nil
	})
}

// defaultIfUnset returns val when set is true, otherwise def.
func defaultIfUnset(set bool, val, def float64) float64 {
	if set {
		return val
	}
	return def
}

// inTx is the same plumbing as scope.Store.inTx — sets the tenant GUC inside
// a transaction so RLS sees it, runs fn, commits on success.
func (s *Store) inTx(ctx context.Context, fn func(context.Context, *dbx.Queries, uuid.UUID) error) error {
	tenantStr, err := tenancy.TenantFromContext(ctx)
	if err != nil {
		return err
	}
	tenantID, err := uuid.Parse(tenantStr)
	if err != nil {
		return fmt.Errorf("risk: parse tenant id: %w", err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("risk: begin tx: %w", err)
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
		return fmt.Errorf("risk: commit: %w", err)
	}
	return nil
}

// ----- conversion helpers -----

func riskFromRow(r dbx.Risk) Risk {
	risk := Risk{
		ID:                  uuid.UUID(r.ID.Bytes),
		TenantID:            uuid.UUID(r.TenantID.Bytes),
		Title:               r.Title,
		Description:         r.Description,
		Category:            r.Category,
		Methodology:         r.Methodology,
		InherentScore:       r.InherentScore,
		Treatment:           r.Treatment,
		TreatmentOwner:      r.TreatmentOwner,
		ResidualScore:       r.ResidualScore,
		Accepter:            r.Accepter,
		InstrumentReference: r.InstrumentReference,
	}
	if r.CreatedAt.Valid {
		risk.CreatedAt = r.CreatedAt.Time
	}
	if r.UpdatedAt.Valid {
		risk.UpdatedAt = r.UpdatedAt.Time
	}
	if r.ReviewDueAt.Valid {
		t := r.ReviewDueAt.Time
		risk.ReviewDueAt = &t
	}
	if r.AcceptedUntil.Valid {
		t := r.AcceptedUntil.Time
		risk.AcceptedUntil = &t
	}
	// Slice 052 columns: level NOT NULL DEFAULT 'team', org_unit_id nullable,
	// themes NOT NULL DEFAULT '{}'.
	risk.Level = r.Level
	if r.OrgUnitID.Valid {
		ou := uuid.UUID(r.OrgUnitID.Bytes)
		risk.OrgUnitID = &ou
	}
	risk.Themes = append([]string(nil), r.Themes...)
	return risk
}

func pgUUID(u uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: u, Valid: true}
}

func pgTimestamptzPtr(t *time.Time) pgtype.Timestamptz {
	if t == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: *t, Valid: true}
}

func pgDatePtr(t *time.Time) pgtype.Date {
	if t == nil {
		return pgtype.Date{}
	}
	return pgtype.Date{Time: *t, Valid: true}
}

func defaultResidual(b []byte) []byte {
	if len(b) > 0 {
		return b
	}
	return []byte("{}")
}
