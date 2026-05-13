// Package auditor owns the slice 025 auditor-role bookkeeping: which
// users hold the auditor role, which audit_periods they're assigned to,
// and the OPA AttrsResolver hook that hydrates
// `input.user.attrs.audit_period_ids` per request.
//
// An auditor is a tenant user with the `auditor` role (slice 035's
// canonical 5-role enum). The role itself is granted via slice 035's
// user_roles table; this package adds the period-assignment overlay --
// a row in `auditor_assignments` says "user U in tenant T is the
// auditor of record for period P". OPA's auditor.rego ABAC rules use
// the resulting `audit_period_ids` attribute to gate period-scoped
// reads and audit-note writes.
//
// Constitutional invariants honored:
//
//	#6  Tenant isolation. Every query is tenant-scoped via the tenant
//	    GUC applied at transaction start (tenancy.ApplyTenant). RLS is
//	    the defense-in-depth layer.
//	#10 Audit-period freezing. Assignment is to a specific
//	    audit_period_id; the period's freeze horizon flows through the
//	    existing slice-026/028 read path. This package does NOT add a
//	    new horizon predicate.
package auditor

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// ErrNotFound is returned by lookups that resolve to zero rows. Currently
// only used by the /v1/me/audit-period (singular) handler.
var ErrNotFound = errors.New("auditor: not found")

// Assignment is the joined view of an auditor_assignments row + its
// referenced audit_periods row. The /v1/me/audit-period(s) endpoints
// return slices of these.
type Assignment struct {
	AuditPeriodID      uuid.UUID
	TenantID           uuid.UUID
	Name               string
	FrameworkVersionID uuid.UUID
	PeriodStart        time.Time
	PeriodEnd          time.Time
	Status             string
	FrozenAt           *time.Time
	PeriodCreatedAt    time.Time
	GrantedAt          time.Time
	GrantedBy          string
}

// Store is the entry point for slice-025 assignment read/write
// operations.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore constructs a Store over pool. The pool is held but not owned --
// callers (typically internal/api.New) close it.
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// Assign grants userID auditor-of-record status for periodID in the
// current tenant. Idempotent: re-assigning the same triple is a no-op.
func (s *Store) Assign(ctx context.Context, userID string, periodID uuid.UUID, grantedBy string) error {
	if userID == "" {
		return fmt.Errorf("auditor: user_id must be non-empty")
	}
	return s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		return q.AssignAuditor(ctx, dbx.AssignAuditorParams{
			TenantID:      pgUUID(tenantID),
			UserID:        userID,
			AuditPeriodID: pgUUID(periodID),
			GrantedBy:     grantedBy,
		})
	})
}

// AuditPeriodIDsFor returns the period UUIDs userID is assigned to in
// the current tenant. The OPA AttrsResolver calls this on every
// auditor-role request to populate
// `input.user.attrs.audit_period_ids`. Empty slice (not nil) is
// returned when the user has no assignments -- ABAC predicates then
// default-deny period-scoped reads, matching the slice-doc P0
// "auditor without assignment cannot see data in that period".
func (s *Store) AuditPeriodIDsFor(ctx context.Context, userID string) ([]uuid.UUID, error) {
	if userID == "" {
		return nil, fmt.Errorf("auditor: user_id must be non-empty")
	}
	var out []uuid.UUID
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		rows, err := q.GetAuditPeriodIDsForUser(ctx, dbx.GetAuditPeriodIDsForUserParams{
			TenantID: pgUUID(tenantID),
			UserID:   userID,
		})
		if err != nil {
			return fmt.Errorf("auditor: list assignment ids: %w", err)
		}
		out = make([]uuid.UUID, 0, len(rows))
		for _, r := range rows {
			if r.Valid {
				out = append(out, uuid.UUID(r.Bytes))
			}
		}
		return nil
	})
	return out, err
}

// ListAssignmentsFor returns every Assignment held by userID in the
// current tenant, joined with the period metadata. Drives the
// /v1/me/audit-period(s) endpoints.
func (s *Store) ListAssignmentsFor(ctx context.Context, userID string) ([]Assignment, error) {
	if userID == "" {
		return nil, fmt.Errorf("auditor: user_id must be non-empty")
	}
	var out []Assignment
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		rows, err := q.ListAuditorAssignmentsForUser(ctx, dbx.ListAuditorAssignmentsForUserParams{
			TenantID: pgUUID(tenantID),
			UserID:   userID,
		})
		if err != nil {
			return fmt.Errorf("auditor: list assignments: %w", err)
		}
		out = make([]Assignment, 0, len(rows))
		for _, r := range rows {
			a := Assignment{
				AuditPeriodID:      uuid.UUID(r.AuditPeriodID.Bytes),
				TenantID:           uuid.UUID(r.TenantID.Bytes),
				Name:               r.Name,
				FrameworkVersionID: uuid.UUID(r.FrameworkVersionID.Bytes),
				Status:             r.Status,
				GrantedBy:          r.GrantedBy,
			}
			if r.PeriodStart.Valid {
				a.PeriodStart = r.PeriodStart.Time
			}
			if r.PeriodEnd.Valid {
				a.PeriodEnd = r.PeriodEnd.Time
			}
			if r.FrozenAt.Valid {
				t := r.FrozenAt.Time
				a.FrozenAt = &t
			}
			if r.PeriodCreatedAt.Valid {
				a.PeriodCreatedAt = r.PeriodCreatedAt.Time
			}
			if r.GrantedAt.Valid {
				a.GrantedAt = r.GrantedAt.Time
			}
			out = append(out, a)
		}
		return nil
	})
	return out, err
}

// inTx is the shared transaction helper -- mirrors the slice-028
// internal/audit/period.Store.inTx pattern. Applies the tenant GUC at
// transaction start so RLS sees the right tenant id, and commits on the
// happy path.
func (s *Store) inTx(ctx context.Context, fn func(context.Context, *dbx.Queries, uuid.UUID) error) error {
	tenantStr, err := tenancy.TenantFromContext(ctx)
	if err != nil {
		return err
	}
	tenantID, err := uuid.Parse(tenantStr)
	if err != nil {
		return fmt.Errorf("auditor: parse tenant id: %w", err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("auditor: begin tx: %w", err)
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
		return fmt.Errorf("auditor: commit: %w", err)
	}
	return nil
}

func pgUUID(u uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: u, Valid: true}
}
