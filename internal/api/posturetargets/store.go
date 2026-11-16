package posturetargets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// ErrVersionNotFound: the framework version does not exist or is another
// tenant's.
var ErrVersionNotFound = errors.New("framework version not found")

// ErrScopeNotSet: the tenant has no activated scope for the version.
var ErrScopeNotSet = errors.New("framework version has no activated scope")

// ErrNoTarget: no target is set for the version.
var ErrNoTarget = errors.New("no posture target set")

// Store reads and writes posture targets under the caller's tenant (RLS).
type Store struct {
	pool *pgxpool.Pool
}

// NewStore wires a Store over the application pool (atlas_app, RLS).
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

func (s *Store) inTx(ctx context.Context, fn func(*dbx.Queries, pgtype.UUID) error) error {
	tenantStr, err := tenancy.TenantFromContext(ctx)
	if err != nil {
		return err
	}
	tenantID, err := uuid.Parse(tenantStr)
	if err != nil {
		return fmt.Errorf("posturetargets: parse tenant id: %w", err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("posturetargets: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		return err
	}
	if err := fn(dbx.New(tx), pgtype.UUID{Bytes: tenantID, Valid: true}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("posturetargets: commit: %w", err)
	}
	return nil
}

// Get returns the version's target, ErrNoTarget when none is set.
func (s *Store) Get(ctx context.Context, versionID uuid.UUID) (Target, error) {
	var out Target
	err := s.inTx(ctx, func(q *dbx.Queries, tenant pgtype.UUID) error {
		row, err := q.GetFrameworkPostureTarget(ctx, dbx.GetFrameworkPostureTargetParams{
			TenantID:           tenant,
			FrameworkVersionID: pgtype.UUID{Bytes: versionID, Valid: true},
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNoTarget
		}
		if err != nil {
			return fmt.Errorf("posturetargets: get: %w", err)
		}
		out = FromRow(row)
		return nil
	})
	return out, err
}

// Set upserts the version's target and logs the change, before -> after,
// in the same transaction. The values must already be validated.
func (s *Store) Set(ctx context.Context, versionID uuid.UUID, direction string, target, warning, critical float64, actor string) (Target, error) {
	var out Target
	err := s.inTx(ctx, func(q *dbx.Queries, tenant pgtype.UUID) error {
		fv := pgtype.UUID{Bytes: versionID, Valid: true}
		state, err := q.FrameworkVersionScopeState(ctx, dbx.FrameworkVersionScopeStateParams{
			TenantID:           tenant,
			FrameworkVersionID: fv,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrVersionNotFound
		}
		if err != nil {
			return fmt.Errorf("posturetargets: version: %w", err)
		}
		if !state.ScopeActivated {
			return ErrScopeNotSet
		}

		var before []byte
		prev, err := q.GetFrameworkPostureTargetForUpdate(ctx, dbx.GetFrameworkPostureTargetForUpdateParams{
			TenantID:           tenant,
			FrameworkVersionID: fv,
		})
		switch {
		case errors.Is(err, pgx.ErrNoRows):
		case err != nil:
			return fmt.Errorf("posturetargets: read current: %w", err)
		default:
			if before, err = json.Marshal(logState(FromRow(prev))); err != nil {
				return err
			}
		}

		row, err := q.UpsertFrameworkPostureTarget(ctx, dbx.UpsertFrameworkPostureTargetParams{
			TenantID:           tenant,
			FrameworkVersionID: fv,
			TargetValue:        numeric(target),
			WarningThreshold:   numeric(warning),
			CriticalThreshold:  numeric(critical),
			Direction:          direction,
			UpdatedBy:          actor,
		})
		if err != nil {
			return fmt.Errorf("posturetargets: upsert: %w", err)
		}
		out = FromRow(row)
		after, err := json.Marshal(logState(out))
		if err != nil {
			return err
		}
		if err := q.InsertFrameworkPostureTargetAudit(ctx, dbx.InsertFrameworkPostureTargetAuditParams{
			TenantID:           tenant,
			FrameworkVersionID: fv,
			Before:             before,
			After:              after,
			Actor:              actor,
		}); err != nil {
			return fmt.Errorf("posturetargets: audit: %w", err)
		}
		return nil
	})
	return out, err
}

// logState is the before / after shape on an audit row: the four values,
// without who or when (the row carries those).
func logState(t Target) map[string]any {
	return map[string]any{
		"target_value":       t.TargetValue,
		"warning_threshold":  t.WarningThreshold,
		"critical_threshold": t.CriticalThreshold,
		"direction":          t.Direction,
	}
}

// FromRow projects a table row onto the wire shape.
func FromRow(r dbx.FrameworkPostureTarget) Target {
	out := Target{
		FrameworkVersionID: uuid.UUID(r.FrameworkVersionID.Bytes).String(),
		TargetValue:        float(r.TargetValue),
		WarningThreshold:   float(r.WarningThreshold),
		CriticalThreshold:  float(r.CriticalThreshold),
		Direction:          r.Direction,
		UpdatedBy:          r.UpdatedBy,
	}
	if r.UpdatedAt.Valid {
		out.UpdatedAt = r.UpdatedAt.Time.UTC().Format(time.RFC3339)
	}
	return out
}

func numeric(f float64) pgtype.Numeric {
	var n pgtype.Numeric
	_ = n.Scan(strconv.FormatFloat(f, 'f', 2, 64))
	return n
}

func float(n pgtype.Numeric) float64 {
	f, err := n.Float64Value()
	if err != nil || !f.Valid {
		return 0
	}
	return f.Float64
}
