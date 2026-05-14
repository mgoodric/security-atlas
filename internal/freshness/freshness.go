// Package freshness is the EVIDENCE FRESHNESS read model of the
// security-atlas evidence pipeline (slice 016). It is a read-only consumer
// of the append-only evidence ledger (`evidence_records`, slice 013) and the
// control bundle catalog (`controls.freshness_class`, slice 009): it reads
// the freshest evidence observation per control, derives a `valid_until`
// horizon from the control's freshness class (canvas §2.3), classifies the
// control as stale or fresh, and UPSERTs the derived state into the
// `evidence_freshness` materialized read-model table.
//
// Constitutional invariant #2 — ingestion and evaluation are separated
// stages — is enforced structurally: this package's only writer
// (Store.upsertFreshness) has exactly one UPSERT target, `evidence_freshness`.
// It imports no ingestion-side write path. It NEVER deletes from
// `evidence_records` — anti-criterion P0-1 (stale records are flagged, never
// deleted) holds by construction: there is no DELETE query in this package's
// sqlc surface against the ledger. Stale evidence stays queryable for
// point-in-time audit replay (AC-6).
//
// The class -> max-age mapping is NOT redefined here. It lives in exactly one
// place — `internal/eval.FreshnessMaxAge`, the canvas §2.3 table — and this
// package calls that exported accessor. Anti-criterion P0-2 (freshness
// respects per-control freshness_class) is honored: every control's
// valid_until is computed from ITS OWN class, never a global default applied
// uniformly.
package freshness

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/eval"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// Store is the freshness read model's database access layer. Every method
// opens a transaction, applies the tenant GUC via internal/tenancy, and runs
// queries inside that transaction so RLS policies see the tenant id.
type Store struct {
	pool *pgxpool.Pool
	// now is the wall-clock source. Overridable in tests so the staleness
	// cutoff is deterministic. It feeds ONLY the is_stale classification and
	// the refreshed_at stamp.
	now func() time.Time
}

// NewStore wires a Store over the application pgx pool. The pool must be
// connected as the application role (NOSUPERUSER NOBYPASSRLS) so RLS is
// actually enforced.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool, now: func() time.Time { return time.Now().UTC() }}
}

// ControlFreshness is the derived freshness state for one control — the wire
// shape the read endpoint and the refresh both work with.
type ControlFreshness struct {
	ControlID        uuid.UUID
	FreshnessClass   string // "" when the control declares no class
	LatestObservedAt *time.Time
	ValidUntil       *time.Time
	IsStale          bool
	EvidenceCount    int
	RefreshedAt      time.Time
}

// Refresh recomputes the freshness read model for EVERY active control of the
// tenant in ctx and UPSERTs the result. Returns the number of control rows
// written. Pure read of the ledgers + write of the read model — invariant #2.
//
// For each active control the refresh:
//  1. reads the freshest evidence observed_at + record count (one grouped
//     SELECT over evidence_records),
//  2. derives valid_until = latest_observed_at + eval.FreshnessMaxAge(class)
//     — the canvas §2.3 mapping, owned by internal/eval, not redefined here,
//  3. classifies is_stale = valid_until < now (a control with no evidence is
//     stale by definition — it is not currently fresh),
//  4. UPSERTs one evidence_freshness row.
func (s *Store) Refresh(ctx context.Context) (int, error) {
	now := s.now()
	written := 0
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		rows, err := q.ListControlsWithLatestEvidence(ctx, pgUUID(tenantID))
		if err != nil {
			return fmt.Errorf("list controls with latest evidence: %w", err)
		}
		for _, r := range rows {
			cf := deriveFreshness(r, now)
			if err := s.upsertFreshness(ctx, q, tenantID, cf, now); err != nil {
				return err
			}
			written++
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return written, nil
}

// List returns the full freshness read model for the tenant in ctx — every
// control's derived state, ordered by class. The read endpoint buckets and
// counts in Go.
func (s *Store) List(ctx context.Context) ([]ControlFreshness, error) {
	var out []ControlFreshness
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		rows, err := q.ListEvidenceFreshness(ctx, pgUUID(tenantID))
		if err != nil {
			return fmt.Errorf("list evidence freshness: %w", err)
		}
		out = make([]ControlFreshness, 0, len(rows))
		for _, r := range rows {
			out = append(out, controlFreshnessFromRow(r))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// deriveFreshness turns one ListControlsWithLatestEvidence row into the
// derived freshness state. This is the PURE logic — deterministic given the
// row and `now`. The valid_until horizon uses eval.FreshnessMaxAge so the
// canvas §2.3 mapping is honored per-control (anti-criterion P0-2).
func deriveFreshness(r dbx.ListControlsWithLatestEvidenceRow, now time.Time) ControlFreshness {
	cf := ControlFreshness{
		ControlID:     uuid.UUID(r.ControlID.Bytes),
		EvidenceCount: int(r.EvidenceCount),
		RefreshedAt:   now,
	}
	if r.FreshnessClass != nil {
		cf.FreshnessClass = *r.FreshnessClass
	}
	if !r.LatestObservedAt.Valid {
		// No evidence at all: no observed_at, no valid_until horizon, stale
		// by definition (the control is not currently fresh).
		cf.IsStale = true
		return cf
	}
	observed := r.LatestObservedAt.Time
	cf.LatestObservedAt = &observed
	// eval.FreshnessMaxAge owns the canvas §2.3 class -> max-age table. An
	// unknown/empty class falls back to the monthly default INSIDE that
	// function — the mapping is never redefined here.
	maxAge, _ := eval.FreshnessMaxAge(cf.FreshnessClass)
	validUntil := observed.Add(maxAge)
	cf.ValidUntil = &validUntil
	cf.IsStale = validUntil.Before(now)
	return cf
}

// upsertFreshness writes ONE row to evidence_freshness. This is the freshness
// read model's ONLY write — there is no method that touches evidence_records
// or any ledger table (invariant #2; anti-criterion P0-1).
func (s *Store) upsertFreshness(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID, cf ControlFreshness, refreshedAt time.Time) error {
	var freshnessClass *string
	if cf.FreshnessClass != "" {
		fc := cf.FreshnessClass
		freshnessClass = &fc
	}
	_, err := q.UpsertEvidenceFreshness(ctx, dbx.UpsertEvidenceFreshnessParams{
		ID:               pgUUID(uuid.New()),
		TenantID:         pgUUID(tenantID),
		ControlID:        pgUUID(cf.ControlID),
		FreshnessClass:   freshnessClass,
		LatestObservedAt: pgTimestamptzPtr(cf.LatestObservedAt),
		ValidUntil:       pgTimestamptzPtr(cf.ValidUntil),
		IsStale:          cf.IsStale,
		EvidenceCount:    int32(cf.EvidenceCount),
		RefreshedAt:      pgTimestamptz(refreshedAt),
	})
	if err != nil {
		return fmt.Errorf("upsert evidence freshness: %w", err)
	}
	return nil
}

// controlFreshnessFromRow maps a stored evidence_freshness row back to the
// wire shape.
func controlFreshnessFromRow(r dbx.EvidenceFreshness) ControlFreshness {
	cf := ControlFreshness{
		ControlID:     uuid.UUID(r.ControlID.Bytes),
		IsStale:       r.IsStale,
		EvidenceCount: int(r.EvidenceCount),
	}
	if r.FreshnessClass != nil {
		cf.FreshnessClass = *r.FreshnessClass
	}
	if r.LatestObservedAt.Valid {
		t := r.LatestObservedAt.Time
		cf.LatestObservedAt = &t
	}
	if r.ValidUntil.Valid {
		t := r.ValidUntil.Time
		cf.ValidUntil = &t
	}
	if r.RefreshedAt.Valid {
		cf.RefreshedAt = r.RefreshedAt.Time
	}
	return cf
}

// inTx opens a transaction, applies the tenant GUC, runs fn, commits on
// success. Mirrors eval.Store.inTx / scope.Store.inTx / period.Store.inTx.
func (s *Store) inTx(ctx context.Context, fn func(context.Context, *dbx.Queries, uuid.UUID) error) error {
	tenantStr, err := tenancy.TenantFromContext(ctx)
	if err != nil {
		return err
	}
	tenantID, err := uuid.Parse(tenantStr)
	if err != nil {
		return fmt.Errorf("freshness: parse tenant id: %w", err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("freshness: begin tx: %w", err)
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
		return fmt.Errorf("freshness: commit: %w", err)
	}
	return nil
}

// ErrNoTenant is returned (wrapped) when the context carries no tenant id.
var ErrNoTenant = errors.New("freshness: no tenant in context")

func pgUUID(u uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: u, Valid: true}
}

func pgTimestamptz(t time.Time) pgtype.Timestamptz {
	if t.IsZero() {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: t.UTC(), Valid: true}
}

func pgTimestamptzPtr(t *time.Time) pgtype.Timestamptz {
	if t == nil {
		return pgtype.Timestamptz{}
	}
	return pgTimestamptz(*t)
}
