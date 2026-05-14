// query.go — the read-side API the HTTP handler consumes.
//
// These are pure SELECTs over control_evaluations: "what is the current
// state of this control?" (AC-1). They never trigger evaluation — the
// engine's EvaluateControl / consumer / scheduler own that. A GET is cheap
// and never has a write side-effect.
package eval

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/scope"
)

// ErrBadScopePredicate wraps a malformed ?scope= JSON-AST predicate so the
// HTTP layer can map it to 400 rather than 500.
var ErrBadScopePredicate = errors.New("eval: scope predicate is malformed")

// State is the AC-1 wire shape: the current evaluated state of a control for
// one scope cell.
type State struct {
	ControlID             uuid.UUID
	ScopeCellID           *uuid.UUID // nil = whole-tenant cell
	Result                string
	FreshnessStatus       string
	EvidenceCountInWindow int
	LastObservedAt        *time.Time
	EvaluatedAt           time.Time
	FreshnessClass        string
	Trigger               string
}

// ControlState returns the current evaluated state of a control.
//
//   - asOf bounds the point-in-time horizon (AC-1 `?as-of=`). Pass FarFuture
//     for live state.
//   - scopePredicate, when non-empty, is a slice-017 JSON-AST predicate; the
//     result is filtered to the scope cells that predicate resolves to
//     (AC-1 `?scope=`). When empty, every cell's latest state is returned.
//
// Returns ErrControlNotFound when the control id does not resolve in-tenant.
func (e *Engine) ControlState(ctx context.Context, controlID uuid.UUID, scopePredicate string, asOf time.Time) ([]State, error) {
	// Resolve the scope filter to a cell-id allowlist BEFORE the read
	// transaction. An empty predicate means "all cells" (nil allowlist).
	var allow map[uuid.UUID]struct{}
	if scopePredicate != "" {
		cells, err := scope.Evaluate([]byte(scopePredicate), mustListCells(ctx, e))
		if err != nil {
			// A malformed predicate is a client error, not a 500.
			return nil, errors.Join(ErrBadScopePredicate, err)
		}
		allow = make(map[uuid.UUID]struct{}, len(cells))
		for _, c := range cells {
			allow[c.ID] = struct{}{}
		}
	}

	var out []State
	err := e.store.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		if _, err := e.store.loadControl(ctx, q, tenantID, controlID); err != nil {
			return err
		}
		rows, err := q.ListLatestControlEvaluations(ctx, dbx.ListLatestControlEvaluationsParams{
			TenantID:    pgUUID(tenantID),
			ControlID:   pgUUID(controlID),
			EvaluatedAt: pgTimestamptz(asOf),
		})
		if err != nil {
			return err
		}
		for _, r := range rows {
			st := stateFromRow(r)
			if allow != nil && st.ScopeCellID != nil {
				if _, ok := allow[*st.ScopeCellID]; !ok {
					continue
				}
			}
			out = append(out, st)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// mustListCells reads the tenant's scope-cell universe via the resolver.
// Falls back to an empty universe when the resolver cannot enumerate (a
// malformed predicate over an empty universe simply matches nothing, which
// is the correct shape).
func mustListCells(ctx context.Context, e *Engine) []scope.Cell {
	type lister interface {
		ListCells(ctx context.Context) ([]scope.Cell, error)
	}
	if l, ok := e.cells.(lister); ok {
		cells, err := l.ListCells(ctx)
		if err == nil {
			return cells
		}
	}
	return nil
}

// stateFromRow converts a dbx.ControlEvaluation into the wire State.
func stateFromRow(r dbx.ControlEvaluation) State {
	st := State{
		ControlID:             uuid.UUID(r.ControlID.Bytes),
		Result:                string(r.Result),
		FreshnessStatus:       r.FreshnessStatus,
		EvidenceCountInWindow: int(r.EvidenceCountInWindow),
		Trigger:               r.Trigger,
	}
	if r.ScopeCellID.Valid {
		id := uuid.UUID(r.ScopeCellID.Bytes)
		st.ScopeCellID = &id
	}
	if r.LastObservedAt.Valid {
		t := r.LastObservedAt.Time
		st.LastObservedAt = &t
	}
	if r.EvaluatedAt.Valid {
		st.EvaluatedAt = r.EvaluatedAt.Time
	}
	if r.FreshnessClass != nil {
		st.FreshnessClass = *r.FreshnessClass
	}
	return st
}

// IsNotFound reports whether err is a not-found condition the HTTP layer
// should map to 404.
func IsNotFound(err error) bool {
	return errors.Is(err, ErrControlNotFound) || errors.Is(err, pgx.ErrNoRows)
}

// IsBadScopePredicate reports whether err is a malformed-?scope= condition
// the HTTP layer should map to 400.
func IsBadScopePredicate(err error) bool {
	if errors.Is(err, ErrBadScopePredicate) {
		return true
	}
	// scope.Evaluate returns plain fmt.Errorf("scope: ...") values without a
	// sentinel; match the prefix as a fallback so a predicate error never
	// surfaces as a 500.
	return err != nil && strings.HasPrefix(err.Error(), "scope: ")
}
