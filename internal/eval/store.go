// store.go — the database surface of the evaluation engine.
//
// The Store wraps the sqlc Queries with the tenancy plumbing required for
// RLS: every method opens a transaction, applies the tenant GUC via
// internal/tenancy, and runs queries inside that transaction so RLS policies
// see the tenant id.
//
// CONSTITUTIONAL INVARIANT #2 — structural enforcement. This Store has
// exactly ONE write method: appendEvaluation, whose sole INSERT target is
// `control_evaluations`. There is NO method that issues INSERT / UPDATE /
// DELETE against `evidence_records` or any other ingestion-side table. The
// read methods (loadEvidence, loadControl, ...) are pure SELECTs. The engine
// physically cannot mutate the evidence ledger because this package never
// generated the code to do so — and slice 013 additionally blocks it at the
// RLS layer (evidence_records has no UPDATE/DELETE policy under FORCE).
package eval

import (
	"context"
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

// ErrControlNotFound is returned when a control id does not resolve within
// the active tenant. The HTTP layer maps it to 404.
var ErrControlNotFound = errors.New("eval: control not found")

// Store is the evaluation engine's database access layer.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore wires a Store over the application pgx pool. The pool must be
// connected as the application role (NOSUPERUSER NOBYPASSRLS) so RLS is
// actually enforced.
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// controlMeta is the slice of a control's bundle the engine needs to
// evaluate it: the freshness class, the implementation type, and the Rego
// evidence query (if the bundle declares one).
type controlMeta struct {
	id                 uuid.UUID
	freshnessClass     string
	implementationType string
	regoQuery          string // empty when the bundle declares no Rego query
}

// evaluationRow is the computed state for one (control, scope_cell) that the
// engine appends to control_evaluations.
type evaluationRow struct {
	controlID             uuid.UUID
	scopeCellID           *uuid.UUID // nil = whole-tenant degenerate cell
	evalRunID             uuid.UUID
	result                string
	freshnessStatus       string
	evidenceCountInWindow int
	lastObservedAt        *time.Time
	freshnessClass        string
	trigger               string
}

// loadControl returns the control's evaluation-relevant metadata. Resolves
// the Rego evidence query from evidence_queries[] when present.
func (s *Store) loadControl(ctx context.Context, q *dbx.Queries, tenantID, controlID uuid.UUID) (controlMeta, error) {
	row, err := q.GetControlByID(ctx, dbx.GetControlByIDParams{
		TenantID: pgUUID(tenantID),
		ID:       pgUUID(controlID),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return controlMeta{}, ErrControlNotFound
		}
		return controlMeta{}, fmt.Errorf("get control: %w", err)
	}
	meta := controlMeta{
		id:                 controlID,
		implementationType: string(row.ImplementationType),
	}
	if row.FreshnessClass != nil {
		meta.freshnessClass = *row.FreshnessClass
	}
	rego, err := firstRegoQuery(row.EvidenceQueries)
	if err != nil {
		return controlMeta{}, err
	}
	meta.regoQuery = rego
	return meta, nil
}

// loadEvidence reads the evidence ledger for one control bounded by the
// point-in-time horizon `asOf`. Pure SELECT — the engine never mutates the
// ledger. `controlRef` is the free-form string form (slice 013 control_ref)
// so a control whose evidence was pushed under an SCF anchor string is also
// found.
func (s *Store) loadEvidence(ctx context.Context, q *dbx.Queries, tenantID, controlID uuid.UUID, controlRef string, asOf time.Time) ([]allRecord, error) {
	rows, err := q.ListEvidenceForControlAsOf(ctx, dbx.ListEvidenceForControlAsOfParams{
		TenantID:   pgUUID(tenantID),
		ControlID:  pgUUID(controlID),
		ControlRef: controlRef,
		ObservedAt: pgTimestamptz(asOf),
	})
	if err != nil {
		return nil, fmt.Errorf("list evidence for control: %w", err)
	}
	out := make([]allRecord, 0, len(rows))
	for _, r := range rows {
		rec := allRecord{result: string(r.Result)}
		if r.ObservedAt.Valid {
			rec.observedAt = r.ObservedAt.Time
		}
		out = append(out, rec)
	}
	return out, nil
}

// appendEvaluation writes ONE row to control_evaluations. This is the
// engine's ONLY write. There is no sibling method that touches
// evidence_records — invariant #2 is enforced by the absence of that code.
func (s *Store) appendEvaluation(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID, row evaluationRow, evaluatedAt time.Time) error {
	var scopeCell pgtype.UUID
	if row.scopeCellID != nil {
		scopeCell = pgUUID(*row.scopeCellID)
	}
	var lastObserved pgtype.Timestamptz
	if row.lastObservedAt != nil {
		lastObserved = pgTimestamptz(*row.lastObservedAt)
	}
	var freshnessClass *string
	if row.freshnessClass != "" {
		fc := row.freshnessClass
		freshnessClass = &fc
	}
	_, err := q.InsertControlEvaluation(ctx, dbx.InsertControlEvaluationParams{
		ID:                    pgUUID(uuid.New()),
		TenantID:              pgUUID(tenantID),
		ControlID:             pgUUID(row.controlID),
		ScopeCellID:           scopeCell,
		EvalRunID:             pgUUID(row.evalRunID),
		EvaluatedAt:           pgTimestamptz(evaluatedAt),
		Result:                dbx.EvidenceResult(row.result),
		FreshnessStatus:       row.freshnessStatus,
		EvidenceCountInWindow: int32(row.evidenceCountInWindow),
		LastObservedAt:        lastObserved,
		FreshnessClass:        freshnessClass,
		Trigger:               row.trigger,
	})
	if err != nil {
		return fmt.Errorf("append control evaluation: %w", err)
	}
	return nil
}

// inTx opens a transaction, applies the tenant GUC, runs fn, commits on
// success. Mirrors scope.Store.inTx / period.Store.inTx.
func (s *Store) inTx(ctx context.Context, fn func(context.Context, *dbx.Queries, uuid.UUID) error) error {
	tenantStr, err := tenancy.TenantFromContext(ctx)
	if err != nil {
		return err
	}
	tenantID, err := uuid.Parse(tenantStr)
	if err != nil {
		return fmt.Errorf("eval: parse tenant id: %w", err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("eval: begin tx: %w", err)
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
		return fmt.Errorf("eval: commit: %w", err)
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
