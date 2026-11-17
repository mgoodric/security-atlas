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
	pool       *pgxpool.Pool
	beforeRisk BeforeRiskWrite
	afterRisk  AfterRiskWrite
}

// NewStore constructs a Store over an existing pgx pool. The pool must be
// connected as `atlas_app` (NOSUPERUSER NOBYPASSRLS) for RLS to fire.
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// AfterRiskWrite runs inside the transaction of a risk write (create, theme
// assignment, delete) with the themes the write touched: the risk's themes
// after a create, the themes before and after a theme assignment, and the
// deleted risk's themes after a delete (#1861). An error rolls
// the write back. It is how the slice 054 aggregation engine
// (internal/risk/aggrule) evaluates its rules on each risk write; aggrule
// imports this package, so the engine is injected rather than imported
// (#1750).
type AfterRiskWrite func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID, themes []string) error

// WithAfterRiskWrite sets the hook run inside every risk write's
// transaction and returns the Store.
func (s *Store) WithAfterRiskWrite(fn AfterRiskWrite) *Store {
	s.afterRisk = fn
	return s
}

// BeforeRiskWrite runs inside the transaction of a theme assignment or a
// delete, before the write locks or deletes the risk row, with the themes
// the write may touch. The aggregation engine uses it to take its rules'
// advisory locks first, in the order an evaluation takes them, so the write
// and a concurrent evaluation never wait on each other in opposite orders
// (#1867). An error rolls the write back. Set it together with
// AfterRiskWrite (httpserver.go wires both).
type BeforeRiskWrite func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID, themes []string) error

// WithBeforeRiskWrite sets the hook run before a theme assignment or delete
// locks the risk row, and returns the Store.
func (s *Store) WithBeforeRiskWrite(fn BeforeRiskWrite) *Store {
	s.beforeRisk = fn
	return s
}

// runBeforeRiskWrite invokes the BeforeRiskWrite hook, if one is set.
func (s *Store) runBeforeRiskWrite(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID, themes []string) error {
	if s.beforeRisk == nil {
		return nil
	}
	return s.beforeRisk(ctx, q, tenantID, themes)
}

// errStaleRuleLocks is returned inside a risk write's transaction when the
// themes read under the row lock are not covered by the themes whose rule
// locks BeforeRiskWrite took from the unlocked read: a concurrent theme
// write committed in between. inTxRuleLocked rolls back and retries once.
var errStaleRuleLocks = errors.New("risk: themes changed between rule lock and row lock")

// checkRuleLocks returns errStaleRuleLocks when lockedThemes, read under the
// row lock, has a theme outside preLocked, the themes BeforeRiskWrite was
// called with. AfterRiskWrite would then take a rule lock while holding the
// row lock, the order #1867 removed, and could deadlock with an evaluation
// (40P01). Theme coverage is sufficient: every rule the after-hook
// evaluates for a covered theme was locked before the row. The store cannot
// see the rules, so a new theme no rule targets retries too, at the cost of
// one extra transaction. On the last attempt it reports nothing and the
// write proceeds, no worse than before #1880.
func (s *Store) checkRuleLocks(lastAttempt bool, preLocked, lockedThemes []string) error {
	if s.beforeRisk == nil || lastAttempt {
		return nil
	}
	set := make(map[string]struct{}, len(preLocked))
	for _, t := range preLocked {
		set[t] = struct{}{}
	}
	for _, t := range lockedThemes {
		if _, ok := set[t]; !ok {
			return errStaleRuleLocks
		}
	}
	return nil
}

// inTxRuleLocked runs fn in a transaction like inTx, and once more in a
// fresh one if fn returns errStaleRuleLocks (#1880). lastAttempt is true on
// the retry, so fn's checkRuleLocks lets it proceed.
func (s *Store) inTxRuleLocked(ctx context.Context, fn func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID, lastAttempt bool) error) error {
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		return fn(ctx, q, tenantID, false)
	})
	if !errors.Is(err, errStaleRuleLocks) {
		return err
	}
	return s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		return fn(ctx, q, tenantID, true)
	})
}

// runAfterRiskWrite invokes the AfterRiskWrite hook, if one is set.
func (s *Store) runAfterRiskWrite(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID, themes []string) error {
	if s.afterRisk == nil {
		return nil
	}
	return s.afterRisk(ctx, q, tenantID, themes)
}

// isRuleGeneratedScore reports whether an inherent_score carries the slice
// 054 engine's rule_generated flag, i.e. the risk is a rule's meta-risk.
func isRuleGeneratedScore(inherent []byte) bool {
	var raw struct {
		RuleGenerated bool `json:"rule_generated"`
	}
	return json.Unmarshal(inherent, &raw) == nil && raw.RuleGenerated
}

// Risk is the domain shape returned from store calls. It mirrors the sqlc Risk
// but exposes more idiomatic Go types (uuid.UUID, time.Time, json.RawMessage).
type Risk struct {
	ID          uuid.UUID
	TenantID    uuid.UUID
	Title       string
	Description string
	// CategoryID references the tenant's own category (#1809).
	CategoryID uuid.UUID
	// CategorySlug and CategoryName are read from the category CategoryID
	// references (#1812). Every Store read that returns a Risk sets them.
	CategorySlug  string
	CategoryName  string
	Methodology   dbx.RiskMethodology
	InherentScore []byte
	Treatment     dbx.RiskTreatment
	// AcceptanceStatus is proposed | approved when Treatment is accept,
	// empty otherwise (#1746).
	AcceptanceStatus dbx.RiskAcceptanceStatus
	TreatmentOwner   string
	// OwnerUserID is the risk's owner, a user of its tenant (#2129). While
	// it is nil, a non-empty TreatmentOwner is the "Owner (unlinked)" text
	// a free-text owner left behind.
	OwnerUserID         *uuid.UUID
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
	// ChildCount is how many child risks roll up into this one (#2060).
	// Non-zero marks a roll-up. Only List sets it; other reads leave it 0.
	ChildCount int
	// Acceptance is the record of a proposed or approved acceptance
	// (#2105). It is empty unless Treatment is accept.
	Acceptance Acceptance
	// Status is open or closed (#2107); Closure is empty unless closed.
	Status  dbx.RiskStatus
	Closure Closure
}

// Acceptance is what proposing and approving an acceptance record (#2105).
// AcceptedUntil and Accepter on Risk carry the approval's expiry and
// approver; these carry the rest.
type Acceptance struct {
	Justification string
	// ProposedUntil is the proposer's suggested expiry; the approver sets
	// AcceptedUntil, which may differ.
	ProposedUntil *time.Time
	// ProposedBy is the proposer's principal (credstore.PrincipalOf), the
	// requester the approval rule compares against. Empty for a proposal
	// made before #2105.
	ProposedBy   string
	ProposedAt   *time.Time
	ApprovedAt   *time.Time
	SelfApproved bool
}

// CreateInput is the API-shape for POST /v1/risks. The store re-validates
// every rule the handler should have validated already; defense in depth.
type CreateInput struct {
	Title       string
	Description string
	// CategoryRef names the risk's category by slug, id or display name
	// (#1809, #1815). The seeded slugs are the retired risk_category enum's
	// values, so a client sending `operational` still works.
	CategoryRef   string
	Methodology   dbx.RiskMethodology
	InherentScore []byte
	// Treatment defaults to untreated when empty: no create path records a
	// treatment decision nobody made (#1746).
	Treatment        dbx.RiskTreatment
	AcceptanceStatus dbx.RiskAcceptanceStatus
	TreatmentOwner   string
	// OwnerUserID is the owner, an active user of the tenant (#2129). When
	// set, TreatmentOwner is not stored.
	OwnerUserID         *uuid.UUID
	ResidualScore       []byte
	ReviewDueAt         *time.Time
	AcceptedUntil       *time.Time
	Accepter            string
	InstrumentReference string
	LinkedControlIDs    []uuid.UUID
	// Level and OrgUnitID place the risk in the hierarchy (#1749). With an
	// org unit and no level, the risk takes the unit's level; with neither,
	// it is a team risk with no unit. A level that differs from the unit's
	// is ErrLevelMismatch.
	Level     dbx.RiskLevel
	OrgUnitID *uuid.UUID
	// Proposer is the creator's principal (credstore.PrincipalOf), recorded
	// as the acceptance's proposer when the risk is created with a proposed
	// acceptance, so the approval rule can refuse them as approver (#2371).
	// Required with acceptance_status=proposed.
	Proposer string
}

// Create inserts a new risk row plus its control links. Methodology defaults
// to DefaultMethodology if empty. Returns ErrInvalidMethodology /
// ErrInherentScoreInvalid / ErrTreatmentValidation on validation failure, and
// a placement validation error (IsPlacementValidation) for a bad level or
// org unit.
func (s *Store) Create(ctx context.Context, in CreateInput) (Risk, error) {
	if in.Methodology == "" {
		in.Methodology = DefaultMethodology
	}
	if in.Treatment == "" {
		in.Treatment = dbx.RiskTreatmentUntreated
	}
	inherent, residual, err := PrepareCreateScores(in.Methodology, in.InherentScore, in.ResidualScore)
	if err != nil {
		return Risk{}, err
	}
	in.InherentScore, in.ResidualScore = inherent, residual
	if err := ValidateCreateAcceptance(in.AcceptanceStatus, in.Accepter, in.AcceptedUntil != nil); err != nil {
		return Risk{}, err
	}
	var proposedBy string
	var proposedAt *time.Time
	if in.AcceptanceStatus == dbx.RiskAcceptanceStatusProposed {
		if in.Proposer == "" {
			return Risk{}, fmt.Errorf("%w: a proposed acceptance requires its proposer", ErrTransitionInput)
		}
		now := time.Now().UTC()
		proposedBy, proposedAt = in.Proposer, &now
	}
	if err := ValidateTreatment(TreatmentInput{
		Treatment:            in.Treatment,
		AcceptanceStatus:     in.AcceptanceStatus,
		AcceptedUntilPresent: in.AcceptedUntil != nil,
		Accepter:             in.Accepter,
		InstrumentReference:  in.InstrumentReference,
		LinkedControlCount:   len(in.LinkedControlIDs),
	}); err != nil {
		return Risk{}, err
	}

	if in.OwnerUserID != nil {
		in.TreatmentOwner = ""
	}

	var out Risk
	err = s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		if err := checkOwnerUser(ctx, q, tenantID, in.OwnerUserID); err != nil {
			return err
		}
		unitLevel, err := orgUnitLevel(ctx, q, tenantID, in.OrgUnitID)
		if err != nil {
			return err
		}
		level, err := resolveLevel(in.Level, unitLevel, dbx.RiskLevelTeam)
		if err != nil {
			return err
		}
		category, err := resolveCategory(ctx, q, tenantID, in.CategoryRef)
		if err != nil {
			return err
		}
		id := uuid.New()
		row, err := q.CreateRisk(ctx, dbx.CreateRiskParams{
			ID:                   pgUUID(id),
			TenantID:             pgUUID(tenantID),
			Title:                in.Title,
			Description:          in.Description,
			CategoryID:           pgUUID(category.ID),
			Methodology:          in.Methodology,
			InherentScore:        in.InherentScore,
			Treatment:            in.Treatment,
			TreatmentOwner:       in.TreatmentOwner,
			ResidualScore:        defaultResidual(in.ResidualScore),
			ReviewDueAt:          pgTimestamptzPtr(in.ReviewDueAt),
			AcceptedUntil:        pgDatePtr(in.AcceptedUntil),
			Accepter:             in.Accepter,
			InstrumentReference:  in.InstrumentReference,
			AcceptanceStatus:     NullAcceptanceStatus(in.AcceptanceStatus),
			Level:                level,
			OrgUnitID:            pgUUIDPtr(in.OrgUnitID),
			AcceptanceProposedBy: proposedBy,
			AcceptanceProposedAt: pgTimestamptzPtr(proposedAt),
			OwnerUserID:          pgUUIDPtr(in.OwnerUserID),
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
		out.CategorySlug, out.CategoryName = category.Slug, category.Name
		out.LinkedControlIDs = append([]uuid.UUID(nil), in.LinkedControlIDs...)
		return s.runAfterRiskWrite(ctx, q, tenantID, out.Themes)
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
		if err := withCategoryNames(ctx, q, tenantID, &out); err != nil {
			return err
		}
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
	Treatment dbx.RiskTreatment
	// Categories, when non-empty, restricts to risks in any of these
	// categories, each named by slug or id (#1812). Archived categories
	// are valid filters. An unknown one is an UnknownCategoryError naming
	// every slug the tenant has.
	Categories  []string
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
	// IncludeRollUps keeps roll-up parents (risks with at least one child
	// linked) in the result (#2060). Left false, List drops them so a
	// parent is never counted beside the children it summarises.
	IncludeRollUps bool
	// IncludeClosed keeps closed risks in the result (#2107). Left false,
	// List returns open risks only, so the register and its count show
	// live risks.
	IncludeClosed bool
}

// List returns all risks for the active tenant, optionally filtered and
// ordered. Filtering and the optional residual/age sort are in-memory to
// keep sqlc queries static; the row count is bounded by tenant-size and v1
// cardinality is small (canvas §1.1 — solo security lead).
func (s *Store) List(ctx context.Context, filter ListFilter) ([]Risk, error) {
	var out []Risk
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		if len(filter.Categories) > 0 {
			// A tenant with no risks may not have its seeded categories
			// yet; filtering by `operational` must still be valid.
			if err := q.SeedRiskCategories(ctx, pgUUID(tenantID)); err != nil {
				return fmt.Errorf("seed risk categories: %w", err)
			}
		}
		byID, err := categoriesByID(ctx, q, tenantID)
		if err != nil {
			return err
		}
		wantCategory, err := categoryFilter(byID, filter.Categories)
		if err != nil {
			return err
		}
		rows, err := q.ListRisks(ctx, pgUUID(tenantID))
		if err != nil {
			return fmt.Errorf("list risks: %w", err)
		}
		counts, err := q.ListRiskChildCountsForTenant(ctx, pgUUID(tenantID))
		if err != nil {
			return fmt.Errorf("list risk child counts: %w", err)
		}
		childCount := make(map[uuid.UUID]int, len(counts))
		for _, c := range counts {
			childCount[uuid.UUID(c.ParentRiskID.Bytes)] = int(c.ChildCount)
		}
		out = make([]Risk, 0, len(rows))
		for _, r := range rows {
			n := childCount[uuid.UUID(r.ID.Bytes)]
			if n > 0 && !filter.IncludeRollUps {
				continue
			}
			if r.Status == dbx.RiskStatusClosed && !filter.IncludeClosed {
				continue
			}
			if filter.Treatment != "" && r.Treatment != filter.Treatment {
				continue
			}
			// After the roll-up exclusion above, so count still equals the
			// rows returned (#2060).
			if wantCategory != nil && !wantCategory[uuid.UUID(r.CategoryID.Bytes)] {
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
			rk := riskFromRow(r)
			setCategoryNames(byID, &rk)
			rk.ChildCount = n
			out = append(out, rk)
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
// treatment first). The magnitude is read by ScoreValue and compared by
// RanksAbove, so FAIR risks (a dollar residual) rank after every 5x5 risk,
// by dollars among themselves (#2185), and a risk with no readable residual
// ranks after both (#2516). It is a stable sort so risks tied on both keys keep the
// underlying ListRisks order. Slice 066 AC-3.
//
// Decorate-sort-undecorate: each risk is paired with its magnitude once
// (so the residual_score JSONB is unmarshalled n times, not the O(n log n)
// times an unmarshal inside the comparator would cost), the paired slice is
// sorted, then the risks are written back in order.
func sortByResidualThenAge(risks []Risk) {
	type scored struct {
		risk Risk
		rank Rank
	}
	decorated := make([]scored, len(risks))
	for i, r := range risks {
		// A residual with no readable shape is Unscored and ranks after
		// every scored risk, FAIR included, rather than erroring the list.
		mag, ok := ScoreValue(r.ResidualScore)
		decorated[i] = scored{risk: r, rank: Rank{Unscored: !ok, Monetary: Monetary(r.Methodology), Value: mag}}
	}
	sort.SliceStable(decorated, func(i, j int) bool {
		a, b := decorated[i], decorated[j]
		if RanksAbove(a.rank, b.rank) {
			return true // higher residual first; FAIR dollars after every 5x5 severity; unscored last
		}
		if RanksAbove(b.rank, a.rank) {
			return false
		}
		return decorated[i].risk.CreatedAt.Before(decorated[j].risk.CreatedAt) // older ranks first
	})
	for i, d := range decorated {
		risks[i] = d.risk
	}
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

// Delete removes a risk by id and writes a deleted row to its audit trail,
// naming actor. ErrNotFound if no row was matched. Only an admin reaches
// it over HTTP (#2107); closing is how anyone else retires a risk. A
// deadlock against a concurrent link is retried in a fresh transaction
// (retryOnDeadlock). The audit row is written inside that transaction, so
// a rolled-back attempt leaves none and a retry writes exactly one.
func (s *Store) Delete(ctx context.Context, id uuid.UUID, actor string) error {
	if actor == "" {
		return fmt.Errorf("%w: actor is required", ErrTransitionInput)
	}
	return retryOnDeadlock(ctx, func() error { return s.deleteOnce(ctx, id, actor) })
}

func (s *Store) deleteOnce(ctx context.Context, id uuid.UUID, actor string) error {
	return s.inTxRuleLocked(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID, lastAttempt bool) error {
		// Check existence first so we can return ErrNotFound rather than a
		// silent success on a missing row.
		row, err := q.GetRiskByID(ctx, dbx.GetRiskByIDParams{
			TenantID: pgUUID(tenantID),
			ID:       pgUUID(id),
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("delete risk get: %w", err)
		}
		// Lock order (#1867): the rules' advisory locks before the row. An
		// evaluation holding a rule's lock may be about to link this risk
		// as a child; deleting first and locking second deadlocks with it.
		// A meta-risk's themes include its rule's target theme, so deleting
		// one also waits out an evaluation that is updating it.
		preLocked := row.Themes
		if err := s.runBeforeRiskWrite(ctx, q, tenantID, preLocked); err != nil {
			return err
		}
		// Re-read under a row lock: the themes may have changed since the
		// unlocked read, and the hook below must see the deleted row's own.
		row, err = q.LockRiskByID(ctx, dbx.LockRiskByIDParams{
			TenantID: pgUUID(tenantID),
			ID:       pgUUID(id),
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("delete risk lock: %w", err)
		}
		if err := s.checkRuleLocks(lastAttempt, preLocked, row.Themes); err != nil {
			return err
		}
		if err := q.DeleteRisk(ctx, dbx.DeleteRiskParams{
			TenantID: pgUUID(tenantID),
			ID:       pgUUID(id),
		}); err != nil {
			return fmt.Errorf("delete risk: %w", err)
		}
		// The trail outlives the risk (#2107): risk_audit_log has no
		// foreign key to risks and refuses DELETE, so this row stays.
		if err := insertRiskAudit(ctx, q, tenantID, id, actor, ActionDeleted,
			deletedState(riskFromRow(row)), map[string]any{}); err != nil {
			return err
		}
		// Re-evaluate the rules that targeted the deleted risk's themes, so
		// a meta-risk it fed drops it from its score (#1861). Deleting a
		// rule-generated meta-risk itself does not: evaluating would
		// immediately re-create the meta-risk the operator just deleted.
		if isRuleGeneratedScore(row.InherentScore) {
			return nil
		}
		return s.runAfterRiskWrite(ctx, q, tenantID, row.Themes)
	})
}

// LinkControlInput is the API-shape for POST /v1/risks/{id}/controls. The
// three weights + design_score are optional: when DesignScoreSet / WeightsSet
// are false the migration `_029` column DEFAULTs apply (design 0.5, weights
// 0.3/0.5/0.2). Slice 020. Actor is who links it, for the audit row (#2106).
type LinkControlInput struct {
	RiskID    uuid.UUID
	ControlID uuid.UUID
	Actor     string

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
// #2106: the risk row is locked and a closed risk refused, as the
// treatment transitions do: the links back the treatment, so they stand
// as they were until the risk is reopened. A new link writes one
// control_linked audit row in the same transaction; a re-link that only
// updates weights writes none. Control links are not an aggregation
// input, so no rule lock is taken: the risk row is the only lock, the last
// step of the #2335 order.
//
// When no weights are supplied (DesignScoreSet / WeightsSet both false), the
// slice-019 weight-free LinkRiskControl query is used so the migration `_029`
// column DEFAULTs apply. When any are supplied, LinkRiskControlWithWeights
// writes the explicit values (the unsupplied ones in that group default to
// their migration `_029` value via the input zero-value being overridden
// below).
func (s *Store) LinkControl(ctx context.Context, in LinkControlInput) error {
	if in.Actor == "" {
		return fmt.Errorf("%w: actor is required", ErrTransitionInput)
	}
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
		// The risk must exist in-tenant (AC-1: link on unknown risk -> 404)
		// and be open (#2106: a closed risk -> 409).
		if _, err := lockOpenRisk(ctx, q, tenantID, in.RiskID); err != nil {
			return err
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
		linked, err := controlLinked(ctx, q, tenantID, in.RiskID, in.ControlID)
		if err != nil {
			return err
		}
		if err := writeControlLink(ctx, q, tenantID, in); err != nil {
			return err
		}
		if linked {
			return nil
		}
		return insertRiskAudit(ctx, q, tenantID, in.RiskID, in.Actor, ActionControlLinked,
			map[string]any{"control_id": nil}, map[string]any{"control_id": in.ControlID})
	})
}

// writeControlLink writes the link row: with the migration `_029`
// DEFAULTs when no weights are supplied, explicit values otherwise.
func writeControlLink(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID, in LinkControlInput) error {
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
		CategoryID:          uuid.UUID(r.CategoryID.Bytes),
		Methodology:         r.Methodology,
		InherentScore:       r.InherentScore,
		Treatment:           r.Treatment,
		TreatmentOwner:      r.TreatmentOwner,
		ResidualScore:       r.ResidualScore,
		Accepter:            r.Accepter,
		InstrumentReference: r.InstrumentReference,
		OwnerUserID:         uuidPtr(r.OwnerUserID),
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
	if r.AcceptanceStatus != nil {
		risk.AcceptanceStatus = *r.AcceptanceStatus
	}
	// Slice 052 columns: level NOT NULL DEFAULT 'team', org_unit_id nullable,
	// themes NOT NULL DEFAULT '{}'.
	risk.Level = r.Level
	if r.OrgUnitID.Valid {
		ou := uuid.UUID(r.OrgUnitID.Bytes)
		risk.OrgUnitID = &ou
	}
	risk.Themes = append([]string(nil), r.Themes...)
	risk.Acceptance = acceptanceFromRow(r)
	risk.Status = r.Status
	risk.Closure = closureFromRow(r)
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

// NullAcceptanceStatus maps an empty acceptance status to SQL NULL.
func NullAcceptanceStatus(s dbx.RiskAcceptanceStatus) *dbx.RiskAcceptanceStatus {
	if s == "" {
		return nil
	}
	return &s
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
