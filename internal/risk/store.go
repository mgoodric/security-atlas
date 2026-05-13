package risk

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

// ErrNotFound is returned when a tenant-scoped lookup yields zero rows. Stays
// a sentinel (not pgx.ErrNoRows) so callers do not need to import pgx.
var ErrNotFound = errors.New("risk: not found")

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

// ListFilter narrows the result set of List. Empty fields are ignored.
type ListFilter struct {
	Treatment   dbx.RiskTreatment
	Category    dbx.RiskCategory
	Methodology dbx.RiskMethodology
}

// List returns all risks for the active tenant, optionally filtered.
// Filtering is in-memory to keep sqlc queries static; the row count is bounded
// by tenant-size and v1 cardinality is small (canvas §1.1 — solo security lead).
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
			out = append(out, riskFromRow(r))
		}
		return nil
	})
	return out, err
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
