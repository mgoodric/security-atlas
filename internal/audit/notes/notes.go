// Package notes owns the slice 025 audit_notes primitive: the auditor's
// private testing-notes workspace (canvas §8.1, §8.3). Visibility is
// strictly auditor-only in v1 -- the §8.5 auditor-auditee shared-thread
// pattern is deferred to a later slice.
//
// The query layer enforces the author-only-read guarantee
// (`WHERE author_user_id = $current_user`) so an auditor cannot read
// another auditor's notes within the same tenant. RLS keeps things
// inside the tenant boundary; this is the defense-in-depth layer.
//
// Constitutional invariants honored:
//
//	#6  Tenant isolation via RLS (FORCE) + tenant GUC applied at tx start.
//	#10 Audit-period freezing -- notes pin to a specific audit_period_id
//	    via a composite FK. Frozen periods continue to accept new notes
//	    (auditor commentary on a frozen period is legitimate; the freeze
//	    is over the evidence ledger, not the auditor workspace).
package notes

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// ErrNotFound is returned for cross-tenant, cross-author, or absent
// lookups. The HTTP handler surfaces this as 404 to avoid leaking
// existence to non-authors (P0-2 / AC-4).
var ErrNotFound = errors.New("auditnotes: not found")

// ErrInvalidScopeType is returned when scope_type is outside the
// canvas §8.3 enum {control, finding, sample, period}.
var ErrInvalidScopeType = errors.New("auditnotes: invalid scope_type")

// validScopeTypes mirrors the audit_notes_scope_type_chk CHECK
// constraint. Kept in sync by the migration review process.
var validScopeTypes = map[string]bool{
	"control": true,
	"finding": true,
	"sample":  true,
	"period":  true,
}

// Note is the public shape returned from the Store. Mirrors the
// audit_notes row.
type Note struct {
	ID            uuid.UUID
	TenantID      uuid.UUID
	AuditPeriodID uuid.UUID
	AuthorUserID  string
	ScopeType     string
	ScopeID       string
	Body          string
	Visibility    string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// CreateInput is the API-shape for POST /v1/audit-notes.
type CreateInput struct {
	AuditPeriodID uuid.UUID
	AuthorUserID  string
	ScopeType     string
	ScopeID       string // optional, empty -> NULL
	Body          string
}

// Store is the entry point for slice-025 audit-notes read/write
// operations.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore wires the Store. The pool is held but not owned -- callers
// (typically internal/api.New) close it.
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// Create inserts a new audit_notes row. The author_user_id is the
// caller's UserID lifted from the request context by the handler --
// the Store does not look it up. Visibility is pinned to 'auditor_only'
// by the DB CHECK constraint.
func (s *Store) Create(ctx context.Context, in CreateInput) (Note, error) {
	if in.AuthorUserID == "" {
		return Note{}, fmt.Errorf("auditnotes: author_user_id must be non-empty")
	}
	if strings.TrimSpace(in.Body) == "" {
		return Note{}, fmt.Errorf("auditnotes: body must be non-empty")
	}
	if !validScopeTypes[in.ScopeType] {
		return Note{}, ErrInvalidScopeType
	}

	var out Note
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		params := dbx.CreateAuditNoteParams{
			ID:            pgUUID(uuid.New()),
			TenantID:      pgUUID(tenantID),
			AuditPeriodID: pgUUID(in.AuditPeriodID),
			AuthorUserID:  in.AuthorUserID,
			ScopeType:     in.ScopeType,
			Body:          in.Body,
		}
		if in.ScopeID != "" {
			sid := in.ScopeID
			params.ScopeID = &sid
		}
		row, err := q.CreateAuditNote(ctx, params)
		if err != nil {
			return fmt.Errorf("auditnotes: create: %w", err)
		}
		out = noteFromRow(row)
		return nil
	})
	return out, err
}

// Get returns the note with the supplied id when authoredBy matches.
// Cross-author / cross-tenant / absent rows all return ErrNotFound --
// the WHERE clause pins (tenant, id, author_user_id) and zero rows
// indicates one of those three.
func (s *Store) Get(ctx context.Context, id uuid.UUID, authorUserID string) (Note, error) {
	if authorUserID == "" {
		return Note{}, fmt.Errorf("auditnotes: author_user_id must be non-empty")
	}
	var out Note
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		row, err := q.GetAuditNoteByID(ctx, dbx.GetAuditNoteByIDParams{
			TenantID:     pgUUID(tenantID),
			ID:           pgUUID(id),
			AuthorUserID: authorUserID,
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("auditnotes: get: %w", err)
		}
		out = noteFromRow(row)
		return nil
	})
	return out, err
}

// ListForAuthorAndPeriod returns every note authored by authorUserID
// in periodID within the current tenant. Cross-author calls return an
// empty slice rather than ErrNotFound -- list endpoints conventionally
// return [] for the "nothing visible" case.
func (s *Store) ListForAuthorAndPeriod(ctx context.Context, periodID uuid.UUID, authorUserID string) ([]Note, error) {
	if authorUserID == "" {
		return nil, fmt.Errorf("auditnotes: author_user_id must be non-empty")
	}
	var out []Note
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		rows, err := q.ListAuditNotesForAuthorAndPeriod(ctx, dbx.ListAuditNotesForAuthorAndPeriodParams{
			TenantID:      pgUUID(tenantID),
			AuditPeriodID: pgUUID(periodID),
			AuthorUserID:  authorUserID,
		})
		if err != nil {
			return fmt.Errorf("auditnotes: list: %w", err)
		}
		out = make([]Note, len(rows))
		for i, r := range rows {
			out[i] = noteFromRow(r)
		}
		return nil
	})
	return out, err
}

// inTx mirrors the slice-028 period.Store.inTx pattern.
func (s *Store) inTx(ctx context.Context, fn func(context.Context, *dbx.Queries, uuid.UUID) error) error {
	tenantStr, err := tenancy.TenantFromContext(ctx)
	if err != nil {
		return err
	}
	tenantID, err := uuid.Parse(tenantStr)
	if err != nil {
		return fmt.Errorf("auditnotes: parse tenant id: %w", err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("auditnotes: begin tx: %w", err)
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
		return fmt.Errorf("auditnotes: commit: %w", err)
	}
	return nil
}

func noteFromRow(r dbx.AuditNote) Note {
	n := Note{
		ID:            uuid.UUID(r.ID.Bytes),
		TenantID:      uuid.UUID(r.TenantID.Bytes),
		AuditPeriodID: uuid.UUID(r.AuditPeriodID.Bytes),
		AuthorUserID:  r.AuthorUserID,
		ScopeType:     r.ScopeType,
		Body:          r.Body,
		Visibility:    r.Visibility,
	}
	if r.ScopeID != nil {
		n.ScopeID = *r.ScopeID
	}
	if r.CreatedAt.Valid {
		n.CreatedAt = r.CreatedAt.Time
	}
	if r.UpdatedAt.Valid {
		n.UpdatedAt = r.UpdatedAt.Time
	}
	return n
}

func pgUUID(u uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: u, Valid: true}
}
