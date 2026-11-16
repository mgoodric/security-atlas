package auditor

// #2118: an admin assigns and removes the auditors of one audit period.
//
// Each change runs in one transaction with its audit_period_audit_log row
// (auditor_assigned / auditor_removed), so a grant never lands unlogged.
// The assignee must be an active user of the caller's tenant holding the
// auditor role; the composite FK on auditor_assignments already refuses
// a period of another tenant, and RLS hides one.
//
// Removal is read on the next request: the OPA attrs resolver reads
// auditor_assignments per request (no cache), so a removed auditor loses
// the period at once.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// ErrPeriodNotFound: the period is absent from the caller's tenant.
var ErrPeriodNotFound = errors.New("auditor: audit period not found")

// ErrNotTenantAuditor: the user is not an active user of the caller's
// tenant holding the auditor role.
var ErrNotTenantAuditor = errors.New("auditor: user is not an active auditor in this tenant")

// PeriodAuditor is one auditor assigned to a period.
type PeriodAuditor struct {
	UserID      string
	DisplayName string
	Email       string
	GrantedAt   time.Time
	GrantedBy   string
}

// AssignToPeriod grants userID the period. Returns created=false when
// the grant already existed; only a new grant writes a log row.
func (s *Store) AssignToPeriod(ctx context.Context, periodID, userID uuid.UUID, actor string) (bool, error) {
	var created bool
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		if err := periodExists(ctx, q, tenantID, periodID); err != nil {
			return err
		}
		ok, err := q.IsActiveTenantAuditor(ctx, dbx.IsActiveTenantAuditorParams{
			TenantID: pgUUID(tenantID),
			ID:       pgUUID(userID),
		})
		if err != nil {
			return fmt.Errorf("auditor: check assignee: %w", err)
		}
		if !ok {
			return ErrNotTenantAuditor
		}
		n, err := q.AssignAuditor(ctx, dbx.AssignAuditorParams{
			TenantID:      pgUUID(tenantID),
			UserID:        userID.String(),
			AuditPeriodID: pgUUID(periodID),
			GrantedBy:     actor,
		})
		if err != nil {
			return fmt.Errorf("auditor: assign: %w", err)
		}
		if n == 0 {
			return nil
		}
		created = true
		return writePeriodLog(ctx, q, tenantID, periodID, "auditor_assigned", actor, userID.String())
	})
	return created, err
}

// RemoveFromPeriod drops userID's grant on the period. Returns
// removed=false when there was no grant; only a removal writes a log row.
// userID is the stored user_id text, so a grant whose user has since been
// deleted can still be removed.
func (s *Store) RemoveFromPeriod(ctx context.Context, periodID uuid.UUID, userID, actor string) (bool, error) {
	var removed bool
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		if err := periodExists(ctx, q, tenantID, periodID); err != nil {
			return err
		}
		n, err := q.RemoveAuditor(ctx, dbx.RemoveAuditorParams{
			TenantID:      pgUUID(tenantID),
			UserID:        userID,
			AuditPeriodID: pgUUID(periodID),
		})
		if err != nil {
			return fmt.Errorf("auditor: remove: %w", err)
		}
		if n == 0 {
			return nil
		}
		removed = true
		return writePeriodLog(ctx, q, tenantID, periodID, "auditor_removed", actor, userID)
	})
	return removed, err
}

// Candidate is an active auditor of the tenant not yet assigned to the
// period: the choices an admin picks from.
type Candidate struct {
	UserID      string
	DisplayName string
	Email       string
}

// ListForPeriod returns the auditors assigned to the period and the
// tenant's other active auditors, who could be assigned.
func (s *Store) ListForPeriod(ctx context.Context, periodID uuid.UUID) ([]PeriodAuditor, []Candidate, error) {
	var out []PeriodAuditor
	var candidates []Candidate
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		if err := periodExists(ctx, q, tenantID, periodID); err != nil {
			return err
		}
		rows, err := q.ListAuditorsForPeriod(ctx, dbx.ListAuditorsForPeriodParams{
			TenantID:      pgUUID(tenantID),
			AuditPeriodID: pgUUID(periodID),
		})
		if err != nil {
			return fmt.Errorf("auditor: list period auditors: %w", err)
		}
		out = make([]PeriodAuditor, 0, len(rows))
		for _, r := range rows {
			a := PeriodAuditor{
				UserID:      r.UserID,
				DisplayName: r.DisplayName,
				Email:       r.Email,
				GrantedBy:   r.GrantedBy,
			}
			if r.GrantedAt.Valid {
				a.GrantedAt = r.GrantedAt.Time
			}
			out = append(out, a)
		}
		crows, err := q.ListAuditorCandidatesForPeriod(ctx, dbx.ListAuditorCandidatesForPeriodParams{
			TenantID:      pgUUID(tenantID),
			AuditPeriodID: pgUUID(periodID),
		})
		if err != nil {
			return fmt.Errorf("auditor: list candidates: %w", err)
		}
		candidates = make([]Candidate, len(crows))
		for i, c := range crows {
			candidates[i] = Candidate{UserID: c.UserID, DisplayName: c.DisplayName, Email: c.Email}
		}
		return nil
	})
	return out, candidates, err
}

func periodExists(ctx context.Context, q *dbx.Queries, tenantID, periodID uuid.UUID) error {
	_, err := q.GetAuditPeriodByID(ctx, dbx.GetAuditPeriodByIDParams{
		TenantID: pgUUID(tenantID),
		ID:       pgUUID(periodID),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrPeriodNotFound
	}
	if err != nil {
		return fmt.Errorf("auditor: get period: %w", err)
	}
	return nil
}

func writePeriodLog(ctx context.Context, q *dbx.Queries, tenantID, periodID uuid.UUID, action, actor, userID string) error {
	detail, err := json.Marshal(map[string]string{"user_id": userID})
	if err != nil {
		return fmt.Errorf("auditor: marshal log detail: %w", err)
	}
	if _, err := q.WriteAuditPeriodLog(ctx, dbx.WriteAuditPeriodLogParams{
		ID:            pgUUID(uuid.New()),
		TenantID:      pgUUID(tenantID),
		AuditPeriodID: pgUUID(periodID),
		Action:        action,
		Actor:         actor,
		Detail:        detail,
	}); err != nil {
		return fmt.Errorf("auditor: write %s log: %w", action, err)
	}
	return nil
}
