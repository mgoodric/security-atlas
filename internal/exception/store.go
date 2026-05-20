// Package exception implements the slice-021 exception/waiver workflow.
//
// An Exception is a time-bounded, scope-bounded waiver of a control's normal
// evaluation. Canvas §6.3 sets the rules; CONTEXT.md captures the precise
// domain definition.
//
// The state machine has five states:
//
//	requested -> approved -> active -> expired   (happy path)
//	requested -> denied                          (terminal; file a new exception to retry)
//	active    -> expired                         (system; daily auto-expiry tick)
//
// Every transition writes one row to exception_audit_log (append-only). The
// auto-expiry tick is NOT silent (anti-criterion P0).
//
// expires_at is capped at requested_at + 365 days by both an application
// check at request time AND a DB CHECK constraint (defense in depth).
// Auto-renewal is forbidden -- no UPDATE path exists that extends
// expires_at past its initial value. To re-attempt a waiver, file a new
// Exception (this is also why `denied` and `expired` are terminal).
package exception

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/audit/sink"
	"github.com/mgoodric/security-atlas/internal/audit/unifiedlog"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// emitSinkException is the slice-126 fanout helper. Builds the canonical
// unifiedlog.Entry for an exception-audit row + forwards to the package-
// level sink. Non-blocking; never returns an error to its caller.
func emitSinkException(ctx context.Context, tenantID, exceptionID, auditID uuid.UUID, action, actor, fromState, toState, reason string) {
	payload, _ := json.Marshal(map[string]any{
		"from_state": fromState,
		"to_state":   toState,
		"reason":     reason,
	})
	sink.EmitDefault(ctx, unifiedlog.Entry{
		OccurredAt:    time.Now().UTC(),
		ActorID:       actor,
		TenantID:      tenantID,
		Kind:          unifiedlog.KindException,
		TargetType:    "exception",
		TargetID:      exceptionID.String(),
		Action:        action,
		RowID:         auditID,
		SubjectModule: unifiedlog.SubjectModuleCore,
		PayloadJSON:   payload,
	})
}

// MaxLifetime is the hard ceiling on exception duration. Anti-criterion P0:
// expires_at MUST NOT exceed requested_at + 365 days. Application rejects
// at write time; DB CHECK is defense-in-depth.
const MaxLifetime = 365 * 24 * time.Hour

// State constants mirror the CHECK enum on exceptions.status.
const (
	StateRequested = "requested"
	StateApproved  = "approved"
	StateDenied    = "denied"
	StateActive    = "active"
	StateExpired   = "expired"
)

// Audit-log action vocabulary.
const (
	ActionRequested = "requested"
	ActionApproved  = "approved"
	ActionDenied    = "denied"
	ActionActivated = "activated"
	ActionExpired   = "expired"
)

// SystemActor is the actor name used when the auto-expiry cron writes audit
// log rows. Distinct from any credential id so audit-trail review can
// segregate system-driven transitions from human ones.
const SystemActor = "system:exception-expiry"

// Errors surfaced by Store. Handlers map these to HTTP status codes.
var (
	// ErrNotFound is returned when an id doesn't resolve under the active
	// tenant context. RLS-friendly: same shape as "in another tenant".
	ErrNotFound = errors.New("exception: not found")
	// ErrWrongState is returned when a transition is requested from a
	// state that doesn't permit it (e.g. approve on a denied row).
	// HTTP 409.
	ErrWrongState = errors.New("exception: not in expected state")
	// ErrExpiresAtRequired is returned when create input lacks expires_at.
	ErrExpiresAtRequired = errors.New("exception: expires_at is required")
	// ErrExpiresAtExceedsCap is returned when expires_at > now + 365d
	// (anti-criterion P0).
	ErrExpiresAtExceedsCap = errors.New("exception: expires_at exceeds 365-day cap")
	// ErrExpiresAtInPast is returned when expires_at <= now.
	ErrExpiresAtInPast = errors.New("exception: expires_at must be in the future")
	// ErrControlRequired is returned when create input lacks control_id.
	ErrControlRequired = errors.New("exception: control_id is required")
	// ErrJustificationRequired is returned when create input lacks
	// justification.
	ErrJustificationRequired = errors.New("exception: justification is required")
	// ErrRequesterRequired is returned when create input lacks requested_by.
	ErrRequesterRequired = errors.New("exception: requested_by is required")
	// ErrSegregationOfDuties is returned when the approver/denier credential
	// matches the requester. Same credential cannot file and adjudicate.
	ErrSegregationOfDuties = errors.New("exception: approver must differ from requester (segregation of duties)")
)

// pgErrForeignKeyViolation is the SQLSTATE Postgres returns when a
// composite FK references a (tenant_id, control_id) tuple that does not
// resolve to an existing controls row.
const pgErrForeignKeyViolation = "23503"

// pgErrCheckViolation is the SQLSTATE Postgres returns when a CHECK
// constraint fails. exceptions_max_365d, exceptions_expires_after_request,
// and exceptions_sod all surface here.
const pgErrCheckViolation = "23514"

// Exception is the domain shape returned from store calls.
type Exception struct {
	ID                   uuid.UUID
	TenantID             uuid.UUID
	ControlID            uuid.UUID
	ScopeCellPredicate   []byte
	Justification        string
	CompensatingControls []string
	RequestedBy          string
	RequestedAt          time.Time
	ApprovedBy           *string
	ApprovedAt           *time.Time
	DeniedBy             *string
	DeniedAt             *time.Time
	ActivatedBy          *string
	ActivatedAt          *time.Time
	EffectiveFrom        *time.Time
	ExpiresAt            time.Time
	ExpiredAt            *time.Time
	Status               string
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

// AuditEntry is the public shape of an exception_audit_log row.
type AuditEntry struct {
	ID          uuid.UUID
	TenantID    uuid.UUID
	ExceptionID uuid.UUID
	Action      string
	Actor       string
	FromState   *string
	ToState     string
	Reason      string
	OccurredAt  time.Time
}

// Store wraps the sqlc Queries with the tenancy plumbing required for RLS.
// Same shape as risk.Store / frameworkscope.Store: every method opens a tx,
// applies the tenant GUC, and runs queries inside that transaction.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore constructs a Store over an existing pgx pool. The pool must be
// connected as `atlas_app` (NOSUPERUSER NOBYPASSRLS) for RLS to fire.
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// CreateInput is the API shape for POST /v1/exceptions. The store
// re-validates every rule the handler should have validated already;
// defense in depth.
type CreateInput struct {
	ControlID            uuid.UUID
	ScopeCellPredicate   []byte
	Justification        string
	CompensatingControls []string
	RequestedBy          string
	ExpiresAt            time.Time
	// Now is the request-time clock used to enforce the 365-day cap.
	// Defaults to time.Now() when zero -- callers override only in tests.
	Now time.Time
}

// Create inserts a new exception in `requested` state and writes the
// matching audit-log row. AC-1 + AC-2 + AC-7.
func (s *Store) Create(ctx context.Context, in CreateInput) (Exception, error) {
	if in.Now.IsZero() {
		in.Now = time.Now().UTC()
	}
	if in.ControlID == uuid.Nil {
		return Exception{}, ErrControlRequired
	}
	if in.Justification == "" {
		return Exception{}, ErrJustificationRequired
	}
	if in.RequestedBy == "" {
		return Exception{}, ErrRequesterRequired
	}
	if in.ExpiresAt.IsZero() {
		return Exception{}, ErrExpiresAtRequired
	}
	if !in.ExpiresAt.After(in.Now) {
		return Exception{}, ErrExpiresAtInPast
	}
	// AC-2 + anti-criterion P0: 365-day cap. Computed from the request-time
	// clock, NOT requested_at-from-the-DB (which defaults to now()) -- the
	// two could drift by milliseconds and tip a borderline create over.
	if in.ExpiresAt.After(in.Now.Add(MaxLifetime)) {
		return Exception{}, ErrExpiresAtExceedsCap
	}
	if in.CompensatingControls == nil {
		in.CompensatingControls = []string{}
	}
	predicate := in.ScopeCellPredicate
	if len(predicate) == 0 {
		predicate = []byte("{}") // empty predicate means "every cell"
	}

	var out Exception
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		id := uuid.New()
		row, err := q.CreateException(ctx, dbx.CreateExceptionParams{
			ID:                   pgUUID(id),
			TenantID:             pgUUID(tenantID),
			ControlID:            pgUUID(in.ControlID),
			ScopeCellPredicate:   predicate,
			Justification:        in.Justification,
			CompensatingControls: in.CompensatingControls,
			RequestedBy:          in.RequestedBy,
			RequestedAt:          pgTimestamptz(in.Now),
			ExpiresAt:            pgTimestamptz(in.ExpiresAt),
		})
		if err != nil {
			return mapCreateError(err)
		}
		out = exceptionFromRow(row)
		// AC-7: write audit-log row for the requested transition.
		auditID := uuid.New()
		if _, alErr := q.WriteExceptionAuditLog(ctx, dbx.WriteExceptionAuditLogParams{
			ID:          pgUUID(auditID),
			TenantID:    pgUUID(tenantID),
			ExceptionID: row.ID,
			Action:      ActionRequested,
			Actor:       in.RequestedBy,
			FromState:   nil,
			ToState:     StateRequested,
			Reason:      "",
		}); alErr != nil {
			return fmt.Errorf("write requested audit log: %w", alErr)
		}
		// Slice 126: fan out to the external sink.
		emitSinkException(ctx, tenantID, uuid.UUID(row.ID.Bytes), auditID,
			ActionRequested, in.RequestedBy, "", StateRequested, "")
		return nil
	})
	return out, err
}

// Approve transitions requested -> approved. AC-3 + AC-7.
// The handler validates IsApprover before calling here; defense-in-depth is
// the segregation-of-duties check below + the DB CHECK exceptions_sod.
func (s *Store) Approve(ctx context.Context, id uuid.UUID, approver string) (Exception, error) {
	if approver == "" {
		return Exception{}, ErrRequesterRequired
	}
	return s.transition(ctx, id, transitionParams{
		expectedPrior: StateRequested,
		newState:      StateApproved,
		actor:         approver,
		action:        ActionApproved,
		run: func(q *dbx.Queries, tenantID uuid.UUID) (dbx.Exception, error) {
			return q.ApproveException(ctx, dbx.ApproveExceptionParams{
				TenantID:   pgUUID(tenantID),
				ID:         pgUUID(id),
				ApprovedBy: stringPtr(approver),
			})
		},
		requesterCheck: approver,
	})
}

// Deny transitions requested -> denied (terminal). AC-7.
// Same SoD invariant as Approve.
func (s *Store) Deny(ctx context.Context, id uuid.UUID, denier, reason string) (Exception, error) {
	if denier == "" {
		return Exception{}, ErrRequesterRequired
	}
	return s.transition(ctx, id, transitionParams{
		expectedPrior: StateRequested,
		newState:      StateDenied,
		actor:         denier,
		action:        ActionDenied,
		reason:        reason,
		run: func(q *dbx.Queries, tenantID uuid.UUID) (dbx.Exception, error) {
			return q.DenyException(ctx, dbx.DenyExceptionParams{
				TenantID: pgUUID(tenantID),
				ID:       pgUUID(id),
				DeniedBy: stringPtr(denier),
			})
		},
		requesterCheck: denier,
	})
}

// Activate transitions approved -> active. AC-4 enable + AC-7.
// effective_from is operator-supplied; defaults to now() when zero.
func (s *Store) Activate(ctx context.Context, id uuid.UUID, activator string, effectiveFrom time.Time) (Exception, error) {
	if activator == "" {
		return Exception{}, ErrRequesterRequired
	}
	if effectiveFrom.IsZero() {
		effectiveFrom = time.Now().UTC()
	}
	return s.transition(ctx, id, transitionParams{
		expectedPrior: StateApproved,
		newState:      StateActive,
		actor:         activator,
		action:        ActionActivated,
		run: func(q *dbx.Queries, tenantID uuid.UUID) (dbx.Exception, error) {
			return q.ActivateException(ctx, dbx.ActivateExceptionParams{
				TenantID:      pgUUID(tenantID),
				ID:            pgUUID(id),
				ActivatedBy:   stringPtr(activator),
				EffectiveFrom: pgTimestamptz(effectiveFrom),
			})
		},
	})
}

// Get returns a single exception by id. ErrNotFound when absent.
func (s *Store) Get(ctx context.Context, id uuid.UUID) (Exception, error) {
	var out Exception
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		row, err := q.GetExceptionByID(ctx, dbx.GetExceptionByIDParams{
			TenantID: pgUUID(tenantID),
			ID:       pgUUID(id),
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("get exception: %w", err)
		}
		out = exceptionFromRow(row)
		return nil
	})
	return out, err
}

// ListFilter narrows the result set of List. Empty fields are ignored.
type ListFilter struct {
	Status string
}

// List returns every exception for the active tenant, newest first.
// Status filter is applied in-memory; cardinality is small for v1.
func (s *Store) List(ctx context.Context, filter ListFilter) ([]Exception, error) {
	var out []Exception
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		rows, err := q.ListExceptions(ctx, pgUUID(tenantID))
		if err != nil {
			return fmt.Errorf("list exceptions: %w", err)
		}
		out = make([]Exception, 0, len(rows))
		for _, r := range rows {
			if filter.Status != "" && r.Status != filter.Status {
				continue
			}
			out = append(out, exceptionFromRow(r))
		}
		return nil
	})
	return out, err
}

// Active returns every active exception for a given control. AC-4 read
// accessor: downstream evaluation engine (slice 020/012) intersects each
// row's scope_cell_predicate with the cell under evaluation to decide
// whether to flip the result to `excepted`.
func (s *Store) Active(ctx context.Context, controlID uuid.UUID) ([]Exception, error) {
	var out []Exception
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		rows, err := q.ListExceptionsByControl(ctx, dbx.ListExceptionsByControlParams{
			TenantID:  pgUUID(tenantID),
			ControlID: pgUUID(controlID),
		})
		if err != nil {
			return fmt.Errorf("list active exceptions: %w", err)
		}
		out = make([]Exception, len(rows))
		for i, r := range rows {
			out[i] = exceptionFromRow(r)
		}
		return nil
	})
	return out, err
}

// Expiring returns active exceptions whose expires_at falls within the
// supplied window from `from`. AC-6 calendar surface.
func (s *Store) Expiring(ctx context.Context, from time.Time, within time.Duration) ([]Exception, error) {
	if within <= 0 {
		return nil, fmt.Errorf("exception: within must be positive")
	}
	threshold := from.Add(within)
	var out []Exception
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		rows, err := q.ListExpiringExceptions(ctx, dbx.ListExpiringExceptionsParams{
			TenantID:  pgUUID(tenantID),
			ExpiresAt: pgTimestamptz(threshold),
		})
		if err != nil {
			return fmt.Errorf("list expiring exceptions: %w", err)
		}
		out = make([]Exception, len(rows))
		for i, r := range rows {
			out[i] = exceptionFromRow(r)
		}
		return nil
	})
	return out, err
}

// ListAuditLog returns the audit-log for a single exception, oldest first.
func (s *Store) ListAuditLog(ctx context.Context, exceptionID uuid.UUID) ([]AuditEntry, error) {
	var out []AuditEntry
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		rows, err := q.ListExceptionAuditLog(ctx, dbx.ListExceptionAuditLogParams{
			TenantID:    pgUUID(tenantID),
			ExceptionID: pgUUID(exceptionID),
		})
		if err != nil {
			return fmt.Errorf("list exception audit log: %w", err)
		}
		out = make([]AuditEntry, len(rows))
		for i, r := range rows {
			out[i] = auditFromRow(r)
		}
		return nil
	})
	return out, err
}

// ----- transition plumbing -----

type transitionParams struct {
	expectedPrior  string
	newState       string
	actor          string
	action         string
	reason         string
	run            func(q *dbx.Queries, tenantID uuid.UUID) (dbx.Exception, error)
	requesterCheck string // when non-empty, asserts actor != row.RequestedBy (segregation of duties)
}

// transition runs a state-changing UPDATE that may return zero rows because
// the WHERE status=... guard didn't match. On pgx.ErrNoRows, probe the row
// to disambiguate ErrNotFound from ErrWrongState.
func (s *Store) transition(ctx context.Context, id uuid.UUID, p transitionParams) (Exception, error) {
	var out Exception
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		// Segregation-of-duties pre-check (only for approve/deny). Fetch
		// the row first so we can compare actor against requested_by.
		var priorRequester string
		if p.requesterCheck != "" {
			before, gErr := q.GetExceptionByID(ctx, dbx.GetExceptionByIDParams{
				TenantID: pgUUID(tenantID),
				ID:       pgUUID(id),
			})
			if gErr != nil {
				if errors.Is(gErr, pgx.ErrNoRows) {
					return ErrNotFound
				}
				return fmt.Errorf("get exception (sod): %w", gErr)
			}
			priorRequester = before.RequestedBy
			if priorRequester == p.requesterCheck {
				return ErrSegregationOfDuties
			}
		}

		row, err := p.run(q, tenantID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				// Either missing row or wrong prior state. Probe to
				// disambiguate.
				_, gErr := q.GetExceptionByID(ctx, dbx.GetExceptionByIDParams{
					TenantID: pgUUID(tenantID),
					ID:       pgUUID(id),
				})
				if errors.Is(gErr, pgx.ErrNoRows) {
					return ErrNotFound
				}
				return ErrWrongState
			}
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == pgErrCheckViolation {
				return ErrSegregationOfDuties
			}
			return fmt.Errorf("transition: %w", err)
		}
		out = exceptionFromRow(row)
		// AC-7: write audit-log row for every transition. Source the
		// from_state from the priorRequester probe when available; fall
		// back to the expected-prior constant otherwise.
		fromState := p.expectedPrior
		auditID := uuid.New()
		if _, alErr := q.WriteExceptionAuditLog(ctx, dbx.WriteExceptionAuditLogParams{
			ID:          pgUUID(auditID),
			TenantID:    pgUUID(tenantID),
			ExceptionID: row.ID,
			Action:      p.action,
			Actor:       p.actor,
			FromState:   stringPtr(fromState),
			ToState:     p.newState,
			Reason:      p.reason,
		}); alErr != nil {
			return fmt.Errorf("write %s audit log: %w", p.action, alErr)
		}
		// Slice 126: fan out to the external sink.
		emitSinkException(ctx, tenantID, uuid.UUID(row.ID.Bytes), auditID,
			p.action, p.actor, fromState, p.newState, p.reason)
		return nil
	})
	return out, err
}

// ----- tenancy plumbing -----

func (s *Store) inTx(ctx context.Context, fn func(context.Context, *dbx.Queries, uuid.UUID) error) error {
	tenantStr, err := tenancy.TenantFromContext(ctx)
	if err != nil {
		return err
	}
	tenantID, err := uuid.Parse(tenantStr)
	if err != nil {
		return fmt.Errorf("exception: parse tenant id: %w", err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("exception: begin tx: %w", err)
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
		return fmt.Errorf("exception: commit: %w", err)
	}
	return nil
}

// ----- error mapping -----

func mapCreateError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case pgErrForeignKeyViolation:
			return fmt.Errorf("exception: control_id does not exist in tenant: %w", err)
		case pgErrCheckViolation:
			// exceptions_max_365d -> ErrExpiresAtExceedsCap
			// exceptions_sod / exceptions_denied_by_sod -> ErrSegregationOfDuties
			switch pgErr.ConstraintName {
			case "exceptions_max_365d":
				return ErrExpiresAtExceedsCap
			case "exceptions_sod", "exceptions_denied_by_sod":
				return ErrSegregationOfDuties
			}
		}
	}
	return fmt.Errorf("create exception: %w", err)
}

// ----- row conversion -----

func exceptionFromRow(r dbx.Exception) Exception {
	out := Exception{
		ID:                   uuid.UUID(r.ID.Bytes),
		TenantID:             uuid.UUID(r.TenantID.Bytes),
		ControlID:            uuid.UUID(r.ControlID.Bytes),
		ScopeCellPredicate:   append([]byte(nil), r.ScopeCellPredicate...),
		Justification:        r.Justification,
		CompensatingControls: append([]string(nil), r.CompensatingControls...),
		RequestedBy:          r.RequestedBy,
		Status:               r.Status,
	}
	if r.RequestedAt.Valid {
		out.RequestedAt = r.RequestedAt.Time
	}
	if r.ApprovedBy != nil {
		v := *r.ApprovedBy
		out.ApprovedBy = &v
	}
	if r.ApprovedAt.Valid {
		t := r.ApprovedAt.Time
		out.ApprovedAt = &t
	}
	if r.DeniedBy != nil {
		v := *r.DeniedBy
		out.DeniedBy = &v
	}
	if r.DeniedAt.Valid {
		t := r.DeniedAt.Time
		out.DeniedAt = &t
	}
	if r.ActivatedBy != nil {
		v := *r.ActivatedBy
		out.ActivatedBy = &v
	}
	if r.ActivatedAt.Valid {
		t := r.ActivatedAt.Time
		out.ActivatedAt = &t
	}
	if r.EffectiveFrom.Valid {
		t := r.EffectiveFrom.Time
		out.EffectiveFrom = &t
	}
	if r.ExpiresAt.Valid {
		out.ExpiresAt = r.ExpiresAt.Time
	}
	if r.ExpiredAt.Valid {
		t := r.ExpiredAt.Time
		out.ExpiredAt = &t
	}
	if r.CreatedAt.Valid {
		out.CreatedAt = r.CreatedAt.Time
	}
	if r.UpdatedAt.Valid {
		out.UpdatedAt = r.UpdatedAt.Time
	}
	return out
}

func auditFromRow(r dbx.ExceptionAuditLog) AuditEntry {
	out := AuditEntry{
		ID:          uuid.UUID(r.ID.Bytes),
		TenantID:    uuid.UUID(r.TenantID.Bytes),
		ExceptionID: uuid.UUID(r.ExceptionID.Bytes),
		Action:      r.Action,
		Actor:       r.Actor,
		ToState:     r.ToState,
		Reason:      r.Reason,
	}
	if r.FromState != nil {
		v := *r.FromState
		out.FromState = &v
	}
	if r.OccurredAt.Valid {
		out.OccurredAt = r.OccurredAt.Time
	}
	return out
}

func pgUUID(u uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: u, Valid: true}
}

func pgTimestamptz(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t, Valid: true}
}

func stringPtr(s string) *string {
	if s == "" {
		return nil
	}
	v := s
	return &v
}
