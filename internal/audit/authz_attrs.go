package audit

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/scope"
)

// #2157: the lookups the authz hydrator (internal/api/audit.ResourceAttrs)
// needs to put an audit period on input.resource.attrs, so the auditor rules
// in audit_periods.rego can compare it with the caller's assigned periods.
// Every lookup reads under the caller's tenant (inTx applies the GUC), so a
// row of another tenant is simply absent. Absent means "no period", which
// the rules deny.

// PopulationPeriod returns the audit period population id is attached to.
// ok is false when the population is absent or attached to no period.
func (s *Store) PopulationPeriod(ctx context.Context, id uuid.UUID) (period uuid.UUID, ok bool, err error) {
	err = s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		row, err := q.GetPopulationByID(ctx, dbx.GetPopulationByIDParams{TenantID: pgUUID(tenantID), ID: pgUUID(id)})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("population period: %w", err)
		}
		period, ok = uuidFromPG(row.AuditPeriodID)
		return nil
	})
	return period, ok, err
}

// SamplePeriod returns the audit period of the population sample id was
// drawn from. ok is false when the sample is absent or its population is
// attached to no period.
func (s *Store) SamplePeriod(ctx context.Context, id uuid.UUID) (period uuid.UUID, ok bool, err error) {
	err = s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		p, err := q.GetSamplePopulationPeriod(ctx, dbx.GetSamplePopulationPeriodParams{TenantID: pgUUID(tenantID), ID: pgUUID(id)})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("sample period: %w", err)
		}
		period, ok = uuidFromPG(p)
		return nil
	})
	return period, ok, err
}

// EvidenceScope is what the authz hydrator knows about one evidence record.
type EvidenceScope struct {
	// Periods are the audit periods with an attached population that
	// contains the record: the same control, observed inside the
	// population's window and at or before its frozen_at, and in a scope
	// cell its predicate selects. Sorted, without duplicates.
	Periods []uuid.UUID
	// ScopeCellID is the record's scope cell, nil when it has none.
	ScopeCellID *uuid.UUID
}

// EvidencePeriods resolves the EvidenceScope of evidence record id. ok is
// false when the record is absent.
func (s *Store) EvidencePeriods(ctx context.Context, id uuid.UUID) (out EvidenceScope, ok bool, err error) {
	err = s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		rec, err := q.GetEvidenceRecordByID(ctx, dbx.GetEvidenceRecordByIDParams{ID: pgUUID(id), TenantID: pgUUID(tenantID)})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("evidence periods: get record: %w", err)
		}
		ok = true
		if cell, valid := uuidFromPG(rec.ScopeID); valid {
			out.ScopeCellID = &cell
		}
		if !rec.ObservedAt.Valid {
			return nil
		}
		out.Periods, err = periodsCovering(ctx, q, tenantID, uuid.UUID(rec.ControlID.Bytes), &rec.ObservedAt.Time, out.ScopeCellID)
		return err
	})
	return out, ok, err
}

// ControlPeriods returns the audit periods with an attached population that
// covers the whole of controlID: a match-every-cell predicate, any window.
func (s *Store) ControlPeriods(ctx context.Context, controlID uuid.UUID) ([]uuid.UUID, error) {
	var out []uuid.UUID
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		var err error
		out, err = periodsCovering(ctx, q, tenantID, controlID, nil, nil)
		return err
	})
	return out, err
}

// periodsCovering lists the periods of the period-attached populations on
// controlID that cover (observedAt, cellID). A nil observedAt skips the
// time test; a nil cellID is covered only by a match-every-cell predicate.
func periodsCovering(
	ctx context.Context, q *dbx.Queries, tenantID, controlID uuid.UUID,
	observedAt *time.Time, cellID *uuid.UUID,
) ([]uuid.UUID, error) {
	pops, err := q.ListPeriodPopulationsForControl(ctx, dbx.ListPeriodPopulationsForControlParams{
		TenantID: pgUUID(tenantID), ControlID: pgUUID(controlID),
	})
	if err != nil {
		return nil, fmt.Errorf("list period populations: %w", err)
	}
	var universe []scope.Cell
	loaded := false
	seen := map[uuid.UUID]bool{}
	var out []uuid.UUID
	for _, pop := range pops {
		period, ok := uuidFromPG(pop.AuditPeriodID)
		if !ok || seen[period] || !populationWindowCovers(pop, observedAt) {
			continue
		}
		if !isTrivialPredicate(pop.ScopePredicate) {
			if cellID == nil {
				continue
			}
			if !loaded {
				if universe, err = listCellUniverse(ctx, q, tenantID); err != nil {
					return nil, err
				}
				loaded = true
			}
			cells, err := matchingCellIDs(pop.ScopePredicate, universe)
			if err != nil {
				return nil, err
			}
			if _, in := cells[*cellID]; !in {
				continue
			}
		}
		seen[period] = true
		out = append(out, period)
	}
	slices.SortFunc(out, func(a, b uuid.UUID) int { return bytes.Compare(a[:], b[:]) })
	return out, nil
}

// populationWindowCovers reports whether observedAt falls inside pop's
// time window and at or before its frozen_at horizon, the same bounds
// ListPopulationEvidenceIDs draws with. A nil observedAt is covered.
func populationWindowCovers(pop dbx.Population, observedAt *time.Time) bool {
	if observedAt == nil {
		return true
	}
	t := *observedAt
	if pop.TimeWindowStart.Valid && t.Before(pop.TimeWindowStart.Time) {
		return false
	}
	if pop.TimeWindowEnd.Valid && t.After(pop.TimeWindowEnd.Time) {
		return false
	}
	if pop.FrozenAt.Valid && t.After(pop.FrozenAt.Time) {
		return false
	}
	return true
}

func uuidFromPG(p pgtype.UUID) (uuid.UUID, bool) {
	if !p.Valid {
		return uuid.UUID{}, false
	}
	return uuid.UUID(p.Bytes), true
}
