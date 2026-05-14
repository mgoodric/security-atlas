// store.go — the board-brief database access layer.
//
// Every method opens a transaction, applies the tenant GUC via
// internal/tenancy, and runs queries inside that transaction so RLS policies
// see the tenant id (constitutional invariant 6). Mirrors the inTx pattern
// in internal/freshness, internal/drift, internal/eval.
//
// The Store's only WRITE target is `board_briefs` (InsertBoardBrief) — an
// append-only table. It never writes a ledger or a read model. Its reads
// cover `board_briefs` (get/list), `frameworks` (the registered-framework
// catalog), and `risks` (the date-bounded top-risks read). Invariant 2:
// generation is a pure read of upstream state plus an append of the frozen
// snapshot.
package board

import (
	"context"
	"encoding/json"
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

// ErrNotFound is returned (wrapped) when a brief id does not resolve in the
// caller's tenant. The HTTP layer maps it to 404. A cross-tenant id also
// surfaces as ErrNotFound — RLS makes the foreign row invisible, so the
// lookup returns pgx.ErrNoRows, which this package normalizes.
var ErrNotFound = errors.New("board: brief not found")

// Store is the board-brief database access layer over the application pgx
// pool. The pool must be connected as the application role (NOSUPERUSER
// NOBYPASSRLS) so RLS is actually enforced.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore wires a Store over the application pgx pool.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// frameworkRow is the registered-framework metadata the Generator needs to
// build a FrameworkPosture row.
type frameworkRow struct {
	Slug string
	Name string
}

// riskRow is one risk the Generator ranks for the "top-3 risks aging"
// section. ResidualScore / InherentScore are the raw JSONB blobs — the
// Generator extracts the severity scalar (the JSONB shape is
// methodology-dependent, so the extraction is Go-side, never a JSONB-path
// SQL expression).
type riskRow struct {
	ID            uuid.UUID
	Title         string
	Category      string
	Treatment     string
	ResidualScore []byte
	InherentScore []byte
	UpdatedAt     time.Time
}

// ListFrameworks returns every registered framework for the tenant in ctx —
// the global catalog plus any tenant-private frameworks. Drives the brief's
// per-framework posture rows.
func (s *Store) ListFrameworks(ctx context.Context) ([]frameworkRow, error) {
	var out []frameworkRow
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		rows, err := q.ListFrameworksForTenant(ctx, pgUUID(tenantID))
		if err != nil {
			return fmt.Errorf("list frameworks: %w", err)
		}
		out = make([]frameworkRow, 0, len(rows))
		for _, r := range rows {
			out = append(out, frameworkRow{Slug: r.Slug, Name: r.Name})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ListRisksAsOf returns every risk created on or before `asOf` for the
// tenant in ctx, ordered oldest-touched first. The Generator ranks these by
// residual severity then age and keeps the top N.
func (s *Store) ListRisksAsOf(ctx context.Context, asOf time.Time) ([]riskRow, error) {
	var out []riskRow
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		rows, err := q.ListRisksForBoardBrief(ctx, dbx.ListRisksForBoardBriefParams{
			TenantID:  pgUUID(tenantID),
			CreatedAt: pgTimestamptz(asOf),
		})
		if err != nil {
			return fmt.Errorf("list risks for board brief: %w", err)
		}
		out = make([]riskRow, 0, len(rows))
		for _, r := range rows {
			rr := riskRow{
				ID:            uuid.UUID(r.ID.Bytes),
				Title:         r.Title,
				Category:      string(r.Category),
				Treatment:     string(r.Treatment),
				ResidualScore: r.ResidualScore,
				InherentScore: r.InherentScore,
			}
			if r.UpdatedAt.Valid {
				rr.UpdatedAt = r.UpdatedAt.Time
			}
			out = append(out, rr)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Insert appends one generated brief to the append-only `board_briefs`
// table and returns the stored row. The Brief is serialized to JSONB; the
// rendered narrative is stored alongside. A second Insert for the same
// period_end produces a NEW row with a NEW id — never an edit (AC
// anti-criterion); the table has no UPDATE path.
func (s *Store) Insert(ctx context.Context, b Brief, narrativeMd string, generatedAt time.Time) (StoredBrief, error) {
	periodEnd, err := time.Parse("2006-01-02", b.PeriodEnd)
	if err != nil {
		return StoredBrief{}, fmt.Errorf("board: parse period_end %q: %w", b.PeriodEnd, err)
	}
	contentJSON, err := json.Marshal(b)
	if err != nil {
		return StoredBrief{}, fmt.Errorf("board: marshal brief content: %w", err)
	}
	id := uuid.New()

	var stored StoredBrief
	err = s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		row, err := q.InsertBoardBrief(ctx, dbx.InsertBoardBriefParams{
			ID:          pgUUID(id),
			TenantID:    pgUUID(tenantID),
			PeriodEnd:   pgDate(periodEnd),
			GeneratedAt: pgTimestamptz(generatedAt),
			Content:     contentJSON,
			NarrativeMd: narrativeMd,
		})
		if err != nil {
			return fmt.Errorf("insert board brief: %w", err)
		}
		stored, err = storedBriefFromRow(row)
		return err
	})
	if err != nil {
		return StoredBrief{}, err
	}
	return stored, nil
}

// Get fetches one frozen brief by id for the tenant in ctx. Returns
// ErrNotFound when the id does not resolve in-tenant (a cross-tenant id is
// invisible under RLS and surfaces the same way).
func (s *Store) Get(ctx context.Context, id uuid.UUID) (StoredBrief, error) {
	var stored StoredBrief
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		row, err := q.GetBoardBriefByID(ctx, dbx.GetBoardBriefByIDParams{
			TenantID: pgUUID(tenantID),
			ID:       pgUUID(id),
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("get board brief: %w", err)
		}
		stored, err = storedBriefFromRow(row)
		return err
	})
	if err != nil {
		return StoredBrief{}, err
	}
	return stored, nil
}

// List returns every frozen brief for the tenant in ctx, newest report-date
// first.
func (s *Store) List(ctx context.Context) ([]StoredBrief, error) {
	var out []StoredBrief
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		rows, err := q.ListBoardBriefs(ctx, pgUUID(tenantID))
		if err != nil {
			return fmt.Errorf("list board briefs: %w", err)
		}
		out = make([]StoredBrief, 0, len(rows))
		for _, row := range rows {
			sb, err := storedBriefFromRow(row)
			if err != nil {
				return err
			}
			out = append(out, sb)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// storedBriefFromRow deserializes a `board_briefs` row into a StoredBrief.
// The frozen `content` JSONB is unmarshalled back into the Brief struct —
// AC-5: the read returns the original frozen content verbatim.
func storedBriefFromRow(row dbx.BoardBrief) (StoredBrief, error) {
	var content Brief
	if err := json.Unmarshal(row.Content, &content); err != nil {
		return StoredBrief{}, fmt.Errorf("board: unmarshal brief content: %w", err)
	}
	sb := StoredBrief{
		ID:          uuid.UUID(row.ID.Bytes),
		Content:     content,
		NarrativeMd: row.NarrativeMd,
	}
	if row.PeriodEnd.Valid {
		sb.PeriodEnd = row.PeriodEnd.Time.Format("2006-01-02")
	}
	if row.GeneratedAt.Valid {
		sb.GeneratedAt = row.GeneratedAt.Time
	}
	return sb, nil
}

// inTx opens a transaction, applies the tenant GUC, runs fn, commits on
// success. Mirrors freshness.Store.inTx / drift.Store.inTx / eval.Store.inTx.
func (s *Store) inTx(ctx context.Context, fn func(context.Context, *dbx.Queries, uuid.UUID) error) error {
	tenantStr, err := tenancy.TenantFromContext(ctx)
	if err != nil {
		return err
	}
	tenantID, err := uuid.Parse(tenantStr)
	if err != nil {
		return fmt.Errorf("board: parse tenant id: %w", err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("board: begin tx: %w", err)
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
		return fmt.Errorf("board: commit: %w", err)
	}
	return nil
}

func pgUUID(u uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: u, Valid: true}
}

func pgTimestamptz(t time.Time) pgtype.Timestamptz {
	if t.IsZero() {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: t.UTC(), Valid: true}
}

func pgDate(t time.Time) pgtype.Date {
	if t.IsZero() {
		return pgtype.Date{}
	}
	return pgtype.Date{
		Time:  time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC),
		Valid: true,
	}
}
