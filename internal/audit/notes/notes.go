// Package notes owns the audit_notes primitive.
//
// History:
//   - Slice 025 introduced audit_notes as the auditor's *private* testing-
//     notes workspace (canvas §8.1, §8.3). Visibility was strictly
//     'auditor_only'; the column was reserved with a single allowed
//     value pinned by a CHECK constraint.
//   - Slice 029 activates the canvas §8.5 Audit Hub pattern: the auditor↔
//     auditee shared-thread workflow. The CHECK is relaxed to allow
//     'shared', a `parent_note_id` self-FK enables replies, scope_type
//     grows to include 'walkthrough', and the table becomes append-only
//     by construction (the UPDATE + DELETE RLS policies are dropped).
//
// The query layer enforces the visibility guarantee. Reads:
//
//   - 'auditor_only' rows: only the author can read them back. RLS keeps
//     things inside the tenant boundary; the query layer pins
//     author_user_id = caller.UserID as defense-in-depth.
//   - 'shared' rows: any tenant member with the OPA `audit-notes` read
//     allow can read them. The query layer expression is
//     `visibility = 'shared' OR author_user_id = caller`.
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

// ErrNotFound is returned for cross-tenant, cross-author (private-note),
// or absent lookups. The HTTP handler surfaces this as 404 to avoid
// leaking existence to non-authors.
var ErrNotFound = errors.New("auditnotes: not found")

// ErrInvalidScopeType is returned when scope_type is outside the
// canvas §8.3 + slice 029 enum {control, finding, sample, period, walkthrough}.
var ErrInvalidScopeType = errors.New("auditnotes: invalid scope_type")

// ErrInvalidVisibility is returned when visibility is outside the
// slice 029 enum {auditor_only, shared}.
var ErrInvalidVisibility = errors.New("auditnotes: invalid visibility")

// ErrParentMismatch is returned when a reply's parent_note_id points to
// a note in a different scope or audit_period.
var ErrParentMismatch = errors.New("auditnotes: parent_note_id does not match scope/period")

// VisibilityAuditorOnly + VisibilityShared mirror the audit_notes_visibility_chk
// constraint. Slice 025 shipped only the first; slice 029 adds the second.
const (
	VisibilityAuditorOnly = "auditor_only"
	VisibilityShared      = "shared"
)

// validScopeTypes mirrors the audit_notes_scope_type_chk CHECK constraint.
// Slice 029 added 'walkthrough'.
var validScopeTypes = map[string]bool{
	"control":     true,
	"finding":     true,
	"sample":      true,
	"period":      true,
	"walkthrough": true,
}

// validVisibilities mirrors the audit_notes_visibility_chk CHECK.
var validVisibilities = map[string]bool{
	VisibilityAuditorOnly: true,
	VisibilityShared:      true,
}

// Note is the public shape returned from the Store.
type Note struct {
	ID            uuid.UUID
	TenantID      uuid.UUID
	AuditPeriodID uuid.UUID
	AuthorUserID  string
	ScopeType     string
	ScopeID       string
	Body          string
	Visibility    string
	ParentNoteID  *uuid.UUID // nil for root notes
	CreatedAt     time.Time
	UpdatedAt     time.Time
	Depth         int // populated by ListThreadForScope; 0 for non-thread reads
}

// CreateInput is the API-shape for POST /v1/audit-notes.
//
// CreateV2 callers populate Visibility (default 'shared') and optional
// ParentNoteID. Slice-025 Create callers leave both at the zero value.
type CreateInput struct {
	AuditPeriodID uuid.UUID
	AuthorUserID  string
	ScopeType     string
	ScopeID       string // optional, empty -> NULL
	Body          string
	Visibility    string     // 'auditor_only' or 'shared'; required by CreateV2
	ParentNoteID  *uuid.UUID // optional reply parent
}

// CreatedWithThread is the CreateV2 return shape -- the new note plus
// the notification recipients computed during the same transaction so
// the caller can dispatch notifications.
type CreatedWithThread struct {
	Note             Note
	NotifyRecipients []string // distinct prior-thread authors excluding the new note's author
}

// Store is the entry point for audit-notes read/write operations.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore wires the Store.
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// Create inserts an auditor_only note via the slice-025 legacy entry
// point. Preserved for backward compatibility with the slice-025
// integration tests and any caller that hasn't migrated to CreateV2.
func (s *Store) Create(ctx context.Context, in CreateInput) (Note, error) {
	if err := s.validate(in, false); err != nil {
		return Note{}, err
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

// CreateV2 is the slice-029 entry point. It accepts visibility +
// optional parent_note_id, validates the parent matches the scope+period
// of the new reply, and returns the prior-thread authors so the caller
// can dispatch notifications.
//
// The visibility default (when in.Visibility == "") is 'shared' -- the
// Audit Hub pattern's default behavior. Callers that want private
// semantics must pass VisibilityAuditorOnly explicitly.
func (s *Store) CreateV2(ctx context.Context, in CreateInput) (CreatedWithThread, error) {
	if in.Visibility == "" {
		in.Visibility = VisibilityShared
	}
	if err := s.validate(in, true); err != nil {
		return CreatedWithThread{}, err
	}
	var out CreatedWithThread
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		// Parent-match guard: when ParentNoteID is set, the parent row
		// must exist in the same tenant + audit_period + scope_type +
		// scope_id. We reuse GetAuditNoteByIDForReader with the caller
		// as the "reader" -- if the parent is auditor_only and the
		// caller is not the parent's author, the reader-aware get
		// returns no rows (anti-criteria: cannot reply to a private
		// note you cannot see).
		if in.ParentNoteID != nil {
			parent, err := q.GetAuditNoteByIDForReader(ctx, dbx.GetAuditNoteByIDForReaderParams{
				TenantID:     pgUUID(tenantID),
				ID:           pgUUID(*in.ParentNoteID),
				AuthorUserID: in.AuthorUserID,
			})
			if err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return ErrNotFound
				}
				return fmt.Errorf("auditnotes: parent lookup: %w", err)
			}
			if uuid.UUID(parent.AuditPeriodID.Bytes) != in.AuditPeriodID {
				return ErrParentMismatch
			}
			if parent.ScopeType != in.ScopeType {
				return ErrParentMismatch
			}
			parentScopeID := ""
			if parent.ScopeID != nil {
				parentScopeID = *parent.ScopeID
			}
			if parentScopeID != in.ScopeID {
				return ErrParentMismatch
			}
		}

		params := dbx.CreateAuditNoteV2Params{
			ID:            pgUUID(uuid.New()),
			TenantID:      pgUUID(tenantID),
			AuditPeriodID: pgUUID(in.AuditPeriodID),
			AuthorUserID:  in.AuthorUserID,
			ScopeType:     in.ScopeType,
			Body:          in.Body,
			Visibility:    in.Visibility,
		}
		if in.ScopeID != "" {
			sid := in.ScopeID
			params.ScopeID = &sid
		}
		if in.ParentNoteID != nil {
			params.ParentNoteID = pgUUID(*in.ParentNoteID)
		}
		row, err := q.CreateAuditNoteV2(ctx, params)
		if err != nil {
			return fmt.Errorf("auditnotes: createV2: %w", err)
		}
		out.Note = noteFromRow(row)

		// Compute notification recipients for the new note. Only fire
		// notifications for 'shared' notes -- private notes have no
		// audience. The new note's own author is excluded.
		if in.Visibility == VisibilityShared {
			authors, err := q.ListThreadAuthorsForScope(ctx, dbx.ListThreadAuthorsForScopeParams{
				TenantID:      pgUUID(tenantID),
				AuditPeriodID: pgUUID(in.AuditPeriodID),
				ScopeType:     in.ScopeType,
				Column4:       in.ScopeID,
			})
			if err != nil {
				return fmt.Errorf("auditnotes: list thread authors: %w", err)
			}
			seen := map[string]bool{in.AuthorUserID: true}
			for _, a := range authors {
				if a == "" || seen[a] {
					continue
				}
				seen[a] = true
				out.NotifyRecipients = append(out.NotifyRecipients, a)
			}
		}
		return nil
	})
	return out, err
}

// Get returns the note with the supplied id when authoredBy matches.
// Cross-author / cross-tenant / absent rows all return ErrNotFound.
// Preserved for slice-025 compatibility (private-note read path).
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

// GetForReader is the slice-029 visibility-aware get. Returns the note
// when it is shared (anyone in the tenant) or authored by the caller
// (private notes). Auditor_only rows belonging to other authors return
// ErrNotFound (anti-criteria: no auditor_only leak).
func (s *Store) GetForReader(ctx context.Context, id uuid.UUID, callerUserID string) (Note, error) {
	if callerUserID == "" {
		return Note{}, fmt.Errorf("auditnotes: caller user_id must be non-empty")
	}
	var out Note
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		row, err := q.GetAuditNoteByIDForReader(ctx, dbx.GetAuditNoteByIDForReaderParams{
			TenantID:     pgUUID(tenantID),
			ID:           pgUUID(id),
			AuthorUserID: callerUserID,
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("auditnotes: get-for-reader: %w", err)
		}
		out = noteFromRow(row)
		return nil
	})
	return out, err
}

// ListForAuthorAndPeriod returns every note authored by authorUserID
// in periodID within the current tenant. Preserved for slice-025
// compatibility (auditor's "my notes" view).
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

// ListThreadForScope returns the visible thread for a (scope_type,
// scope_id, audit_period_id) anchor. Slice 029 entry point for GET
// /v1/audit-notes thread retrieval.
//
// Visibility filter applies row-by-row: shared notes are visible to
// anyone; auditor_only notes are only visible to their author. Tree
// shape is preserved via the Depth field on each note.
func (s *Store) ListThreadForScope(ctx context.Context, periodID uuid.UUID, scopeType, scopeID, callerUserID string) ([]Note, error) {
	if !validScopeTypes[scopeType] {
		return nil, ErrInvalidScopeType
	}
	if callerUserID == "" {
		return nil, fmt.Errorf("auditnotes: caller user_id must be non-empty")
	}
	var out []Note
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		rows, err := q.ListThreadForScope(ctx, dbx.ListThreadForScopeParams{
			TenantID:      pgUUID(tenantID),
			AuditPeriodID: pgUUID(periodID),
			ScopeType:     scopeType,
			Column4:       scopeID, // empty string is the "no scope_id" sentinel
			Column5:       callerUserID,
		})
		if err != nil {
			return fmt.Errorf("auditnotes: thread: %w", err)
		}
		out = make([]Note, len(rows))
		for i, r := range rows {
			out[i] = threadRowToNote(r)
		}
		return nil
	})
	return out, err
}

// validate centralizes the slice-025 + slice-029 input checks. The
// requireVisibility flag forces a non-empty visibility check (the
// slice-029 path); the slice-025 Create entrypoint passes false because
// CreateAuditNote pins visibility to 'auditor_only' at the SQL layer.
func (s *Store) validate(in CreateInput, requireVisibility bool) error {
	if in.AuthorUserID == "" {
		return fmt.Errorf("auditnotes: author_user_id must be non-empty")
	}
	if strings.TrimSpace(in.Body) == "" {
		return fmt.Errorf("auditnotes: body must be non-empty")
	}
	if !validScopeTypes[in.ScopeType] {
		return ErrInvalidScopeType
	}
	if requireVisibility {
		if !validVisibilities[in.Visibility] {
			return ErrInvalidVisibility
		}
	}
	return nil
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
	if r.ParentNoteID.Valid {
		pid := uuid.UUID(r.ParentNoteID.Bytes)
		n.ParentNoteID = &pid
	}
	if r.CreatedAt.Valid {
		n.CreatedAt = r.CreatedAt.Time
	}
	if r.UpdatedAt.Valid {
		n.UpdatedAt = r.UpdatedAt.Time
	}
	return n
}

func threadRowToNote(r dbx.ListThreadForScopeRow) Note {
	n := Note{
		ID:            uuid.UUID(r.ID.Bytes),
		TenantID:      uuid.UUID(r.TenantID.Bytes),
		AuditPeriodID: uuid.UUID(r.AuditPeriodID.Bytes),
		AuthorUserID:  r.AuthorUserID,
		ScopeType:     r.ScopeType,
		Body:          r.Body,
		Visibility:    r.Visibility,
		Depth:         int(r.Depth),
	}
	if r.ScopeID != nil {
		n.ScopeID = *r.ScopeID
	}
	if r.ParentNoteID.Valid {
		pid := uuid.UUID(r.ParentNoteID.Bytes)
		n.ParentNoteID = &pid
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
