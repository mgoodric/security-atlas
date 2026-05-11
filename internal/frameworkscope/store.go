package frameworkscope

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// Errors returned by Store operations. Handlers map these to HTTP codes.
var (
	// ErrNotFound is returned when an id doesn't resolve under the active
	// tenant context (RLS-friendly: looks identical to "in another tenant").
	ErrNotFound = errors.New("frameworkscope: scope not found")
	// ErrWrongState is returned when a workflow transition is requested
	// from a state that doesn't permit it (e.g. Approve on a row in
	// `draft`). HTTP 409.
	ErrWrongState = errors.New("frameworkscope: scope not in expected state")
	// ErrAnotherActivated is returned when activation would violate the
	// partial unique index (another row is already activated for the
	// same (tenant, framework_version)). HTTP 409.
	ErrAnotherActivated = errors.New("frameworkscope: another scope is already activated for this framework version")
)

// State constants mirror the CHECK enum on framework_scopes.state.
const (
	StateDraft      = "draft"
	StateReview     = "review"
	StateApproved   = "approved"
	StateActivated  = "activated"
	StateSuperseded = "superseded"
)

// pgErrUniqueViolation is the SQLSTATE Postgres returns when a UNIQUE
// constraint trips. We translate it to ErrAnotherActivated on the partial
// unique index for the activated-row invariant.
const pgErrUniqueViolation = "23505"

// FrameworkScope is the Go surface for a framework_scopes row.
type FrameworkScope struct {
	ID                       uuid.UUID
	TenantID                 uuid.UUID
	FrameworkVersionID       uuid.UUID
	Name                     string
	State                    string
	Predicate                []byte // canonicalized JSON
	PredicateHash            string
	ApproverUserID           *uuid.UUID
	ApprovedAt               *time.Time
	PredicateHashAtApproval  *string
	ApprovalEvidenceFileURL  *string
	ApprovalEvidenceFileHash *string
	EffectiveFrom            *time.Time
	SupersededBy             *uuid.UUID
	SupersededAt             *time.Time
	CreatedAt                time.Time
	UpdatedAt                time.Time
}

// Store wraps the sqlc-generated Queries with the tenancy plumbing required
// for RLS. Same shape as internal/scope.Store: every method opens a tx,
// applies the tenant GUC, and runs queries inside that tx so RLS policies
// see the tenant id.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore constructs a Store over an existing pgx pool. Pool must be
// connected as the application role (NOSUPERUSER NOBYPASSRLS) — RLS is
// unenforceable otherwise.
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// CreateRequest is the input for Create. Predicate is the raw caller-supplied
// bytes; Create canonicalizes + hashes them.
type CreateRequest struct {
	FrameworkVersionID uuid.UUID
	Name               string
	Predicate          []byte // raw JSON from the caller
}

// Create inserts a new framework_scope row in `draft` state. AC-5.
func (s *Store) Create(ctx context.Context, req CreateRequest) (FrameworkScope, error) {
	canon, hash, err := Canonicalize(req.Predicate)
	if err != nil {
		return FrameworkScope{}, err
	}
	if err := Validate(canon); err != nil {
		return FrameworkScope{}, err
	}

	var out FrameworkScope
	err = s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		row, err := q.CreateFrameworkScope(ctx, dbx.CreateFrameworkScopeParams{
			ID:                 pgUUID(uuid.New()),
			TenantID:           pgUUID(tenantID),
			FrameworkVersionID: pgUUID(req.FrameworkVersionID),
			Name:               req.Name,
			Predicate:          canon,
			PredicateHash:      hash,
		})
		if err != nil {
			return fmt.Errorf("create framework_scope: %w", err)
		}
		out = rowToScope(row)
		return nil
	})
	return out, err
}

// UpdatePredicate edits the predicate. The DB-level BEFORE UPDATE trigger
// bounces the row back to `draft` and nulls approval columns if the row was
// in `review` or `approved`. The handler reads `invalidated` to surface the
// `approval_invalidated` banner per AC-9.
func (s *Store) UpdatePredicate(ctx context.Context, id uuid.UUID, predicate []byte) (scope FrameworkScope, invalidated bool, err error) {
	canon, hash, cerr := Canonicalize(predicate)
	if cerr != nil {
		return FrameworkScope{}, false, cerr
	}
	if verr := Validate(canon); verr != nil {
		return FrameworkScope{}, false, verr
	}

	err = s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		before, gerr := q.GetFrameworkScopeByID(ctx, dbx.GetFrameworkScopeByIDParams{
			TenantID: pgUUID(tenantID),
			ID:       pgUUID(id),
		})
		if gerr != nil {
			if errors.Is(gerr, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("get framework_scope: %w", gerr)
		}
		row, uerr := q.UpdateFrameworkScopePredicate(ctx, dbx.UpdateFrameworkScopePredicateParams{
			TenantID:      pgUUID(tenantID),
			ID:            pgUUID(id),
			Predicate:     canon,
			PredicateHash: hash,
		})
		if uerr != nil {
			return fmt.Errorf("update predicate: %w", uerr)
		}
		scope = rowToScope(row)
		// The trigger flips state to draft + nulls approval cols only when
		// the hash differs AND prior state was review/approved. Report
		// that so the handler can emit the banner.
		if before.PredicateHash != hash &&
			(before.State == StateReview || before.State == StateApproved) &&
			row.State == StateDraft {
			invalidated = true
		}
		return nil
	})
	return scope, invalidated, err
}

// Submit transitions draft -> review. AC-6.
func (s *Store) Submit(ctx context.Context, id uuid.UUID) (FrameworkScope, error) {
	return s.guardedTransition(ctx, id, func(q *dbx.Queries, tenantID uuid.UUID) (dbx.FrameworkScope, error) {
		return q.SubmitFrameworkScope(ctx, dbx.SubmitFrameworkScopeParams{
			TenantID: pgUUID(tenantID),
			ID:       pgUUID(id),
		})
	})
}

// ApproveRequest is the input for Approve. EvidenceFileURL/EvidenceFileHash
// are optional (ADR-0001: in-app attestation is primary, file upload is
// optional). When empty, the columns stay NULL.
type ApproveRequest struct {
	ID               uuid.UUID
	ApproverUserID   string // free-form id from the calling credential
	EvidenceFileURL  string
	EvidenceFileHash string
}

// Approve transitions review -> approved. Records approver + approved_at +
// predicate_hash_at_approval and optionally the evidence-file URL+hash. AC-7.
//
// ApproverUserID is the caller's free-form credential id ("key_…"). When it
// happens to parse as a UUID the column is populated; otherwise it stays
// NULL and the audit-log + credential id (set elsewhere) carries the
// attribution. Slice 034 will graduate this to real user UUIDs.
func (s *Store) Approve(ctx context.Context, req ApproveRequest) (FrameworkScope, error) {
	return s.guardedTransition(ctx, req.ID, func(q *dbx.Queries, tenantID uuid.UUID) (dbx.FrameworkScope, error) {
		var url, hash *string
		if req.EvidenceFileURL != "" {
			u := req.EvidenceFileURL
			url = &u
		}
		if req.EvidenceFileHash != "" {
			h := req.EvidenceFileHash
			hash = &h
		}
		var approverPG pgtype.UUID
		if u, perr := uuid.Parse(req.ApproverUserID); perr == nil {
			approverPG = pgUUID(u)
		}
		return q.ApproveFrameworkScope(ctx, dbx.ApproveFrameworkScopeParams{
			TenantID:                 pgUUID(tenantID),
			ID:                       pgUUID(req.ID),
			ApproverUserID:           approverPG,
			ApprovalEvidenceFileUrl:  url,
			ApprovalEvidenceFileHash: hash,
		})
	})
}

// Activate transitions approved -> activated AND atomically supersedes the
// prior `activated` row for the same (tenant, framework_version). AC-8.
//
// The two writes happen in one tx so the partial unique index never sees
// two `activated` rows simultaneously.
func (s *Store) Activate(ctx context.Context, id uuid.UUID, effectiveFrom time.Time) (FrameworkScope, error) {
	var out FrameworkScope
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		before, gerr := q.GetFrameworkScopeByID(ctx, dbx.GetFrameworkScopeByIDParams{
			TenantID: pgUUID(tenantID),
			ID:       pgUUID(id),
		})
		if gerr != nil {
			if errors.Is(gerr, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("get framework_scope: %w", gerr)
		}
		if before.State != StateApproved {
			return ErrWrongState
		}
		// Supersede the prior activated row (if any) FIRST so the partial
		// unique index doesn't fire when the activation lands.
		if serr := q.SupersedePreviousActivated(ctx, dbx.SupersedePreviousActivatedParams{
			TenantID:           pgUUID(tenantID),
			FrameworkVersionID: before.FrameworkVersionID,
			SupersededBy:       pgUUID(id),
		}); serr != nil {
			return fmt.Errorf("supersede prior: %w", serr)
		}
		row, aerr := q.ActivateFrameworkScope(ctx, dbx.ActivateFrameworkScopeParams{
			TenantID:      pgUUID(tenantID),
			ID:            pgUUID(id),
			EffectiveFrom: pgtype.Timestamptz{Time: effectiveFrom, Valid: true},
		})
		if aerr != nil {
			var pgErr *pgconn.PgError
			if errors.As(aerr, &pgErr) && pgErr.Code == pgErrUniqueViolation {
				return ErrAnotherActivated
			}
			return fmt.Errorf("activate: %w", aerr)
		}
		out = rowToScope(row)
		return nil
	})
	return out, err
}

// Get returns a single scope by id.
func (s *Store) Get(ctx context.Context, id uuid.UUID) (FrameworkScope, error) {
	var out FrameworkScope
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		row, err := q.GetFrameworkScopeByID(ctx, dbx.GetFrameworkScopeByIDParams{
			TenantID: pgUUID(tenantID),
			ID:       pgUUID(id),
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("get framework_scope: %w", err)
		}
		out = rowToScope(row)
		return nil
	})
	return out, err
}

// ListFilters constrain List. Zero values mean "no filter on that column".
type ListFilters struct {
	FrameworkVersionID *uuid.UUID
	State              string
}

// List enumerates framework_scopes for the active tenant, optionally
// filtered by framework_version and/or state. AC-10.
func (s *Store) List(ctx context.Context, f ListFilters) ([]FrameworkScope, error) {
	var out []FrameworkScope
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		var rows []dbx.FrameworkScope
		var err error
		if f.FrameworkVersionID != nil {
			rows, err = q.ListFrameworkScopesByFrameworkVersion(ctx, dbx.ListFrameworkScopesByFrameworkVersionParams{
				TenantID:           pgUUID(tenantID),
				FrameworkVersionID: pgUUID(*f.FrameworkVersionID),
			})
		} else {
			rows, err = q.ListFrameworkScopes(ctx, pgUUID(tenantID))
		}
		if err != nil {
			return fmt.Errorf("list framework_scopes: %w", err)
		}
		out = make([]FrameworkScope, 0, len(rows))
		for _, r := range rows {
			if f.State != "" && r.State != f.State {
				continue
			}
			out = append(out, rowToScope(r))
		}
		return nil
	})
	return out, err
}

// Activated returns the currently-active framework_scope for a given
// framework version, or ErrNotFound if no row is in state `activated`. AC-10.
func (s *Store) Activated(ctx context.Context, frameworkVersionID uuid.UUID) (FrameworkScope, error) {
	var out FrameworkScope
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		row, err := q.GetActivatedFrameworkScope(ctx, dbx.GetActivatedFrameworkScopeParams{
			TenantID:           pgUUID(tenantID),
			FrameworkVersionID: pgUUID(frameworkVersionID),
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("get activated: %w", err)
		}
		out = rowToScope(row)
		return nil
	})
	return out, err
}

// AsOf returns the framework_scope that was active at `t`. AC-13.
func (s *Store) AsOf(ctx context.Context, frameworkVersionID uuid.UUID, t time.Time) (FrameworkScope, error) {
	var out FrameworkScope
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		row, err := q.GetFrameworkScopeAsOf(ctx, dbx.GetFrameworkScopeAsOfParams{
			TenantID:           pgUUID(tenantID),
			FrameworkVersionID: pgUUID(frameworkVersionID),
			EffectiveFrom:      pgtype.Timestamptz{Time: t, Valid: true},
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("get as_of: %w", err)
		}
		out = rowToScope(row)
		return nil
	})
	return out, err
}

// guardedTransition runs a single-row UPDATE that may return zero rows
// because the state guard failed. On pgx.ErrNoRows, probe the row to
// disambiguate ErrNotFound from ErrWrongState.
func (s *Store) guardedTransition(
	ctx context.Context,
	id uuid.UUID,
	op func(q *dbx.Queries, tenantID uuid.UUID) (dbx.FrameworkScope, error),
) (FrameworkScope, error) {
	var out FrameworkScope
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		row, err := op(q, tenantID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				_, getErr := q.GetFrameworkScopeByID(ctx, dbx.GetFrameworkScopeByIDParams{
					TenantID: pgUUID(tenantID),
					ID:       pgUUID(id),
				})
				if errors.Is(getErr, pgx.ErrNoRows) {
					return ErrNotFound
				}
				return ErrWrongState
			}
			return fmt.Errorf("transition: %w", err)
		}
		out = rowToScope(row)
		return nil
	})
	return out, err
}

// inTx opens a tx, applies the tenant GUC, runs fn, commits if fn returns nil.
// Same shape as internal/scope.Store.inTx; the GUC lives on the tx so RLS is
// honest (canvas §5.4).
func (s *Store) inTx(ctx context.Context, fn func(context.Context, *dbx.Queries, uuid.UUID) error) error {
	tenantStr, err := tenancy.TenantFromContext(ctx)
	if err != nil {
		return err
	}
	tenantID, err := uuid.Parse(tenantStr)
	if err != nil {
		return fmt.Errorf("frameworkscope: parse tenant id: %w", err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("frameworkscope: begin tx: %w", err)
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
		return fmt.Errorf("frameworkscope: commit: %w", err)
	}
	return nil
}

// rowToScope converts the sqlc-generated FrameworkScope row to the public
// type. Nullable pgtype columns are reflected as Go pointers.
func rowToScope(r dbx.FrameworkScope) FrameworkScope {
	out := FrameworkScope{
		ID:                 uuid.UUID(r.ID.Bytes),
		TenantID:           uuid.UUID(r.TenantID.Bytes),
		FrameworkVersionID: uuid.UUID(r.FrameworkVersionID.Bytes),
		Name:               r.Name,
		State:              r.State,
		Predicate:          append([]byte(nil), r.Predicate...),
		PredicateHash:      r.PredicateHash,
	}
	if r.CreatedAt.Valid {
		out.CreatedAt = r.CreatedAt.Time
	}
	if r.UpdatedAt.Valid {
		out.UpdatedAt = r.UpdatedAt.Time
	}
	if r.ApproverUserID.Valid {
		u := uuid.UUID(r.ApproverUserID.Bytes)
		out.ApproverUserID = &u
	}
	if r.ApprovedAt.Valid {
		t := r.ApprovedAt.Time
		out.ApprovedAt = &t
	}
	out.PredicateHashAtApproval = r.PredicateHashAtApproval
	out.ApprovalEvidenceFileURL = r.ApprovalEvidenceFileUrl
	out.ApprovalEvidenceFileHash = r.ApprovalEvidenceFileHash
	if r.EffectiveFrom.Valid {
		t := r.EffectiveFrom.Time
		out.EffectiveFrom = &t
	}
	if r.SupersededBy.Valid {
		u := uuid.UUID(r.SupersededBy.Bytes)
		out.SupersededBy = &u
	}
	if r.SupersededAt.Valid {
		t := r.SupersededAt.Time
		out.SupersededAt = &t
	}
	return out
}

func pgUUID(u uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: u, Valid: true}
}
