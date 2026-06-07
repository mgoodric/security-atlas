// engine.go — the evaluation engine orchestration.
//
// EvaluateControl computes derived state for one control across every scope
// cell its applicability_expr resolves to, and appends one
// control_evaluations row per (control, cell). EvaluateAll iterates every
// active control. Replay is EvaluateAll with trigger=replay — the AC-7
// recompute-from-ledger path.
//
// The engine is a READ-ONLY consumer of the evidence ledger. It reads
// evidence_records and controls; it writes ONLY control_evaluations
// (constitutional invariant #2). It holds no hidden state — every output is
// a deterministic function of the immutable ledger slice, so deleting
// control_evaluations and re-running reproduces identical state (AC-7).
package eval

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/scope"
)

// Trigger values mirror the control_evaluations.trigger CHECK vocabulary.
const (
	TriggerIngest    = "ingest"
	TriggerScheduled = "scheduled"
	TriggerManual    = "manual"
	TriggerReplay    = "replay"
)

// cellResolver is the slice-017 hook the engine uses to enumerate the scope
// cells a control applies to. scope.Store satisfies it. Declaring it as an
// interface keeps the engine unit-testable and decoupled from scope.Store's
// concrete type.
type cellResolver interface {
	ControlApplicability(ctx context.Context, controlID uuid.UUID) ([]scope.Cell, error)
}

// Engine is the evaluation stage. Construct with NewEngine.
type Engine struct {
	store *Store
	cells cellResolver
	// now is the wall-clock source. Overridable in tests so the
	// freshness-window cutoff is deterministic. It feeds ONLY the freshness
	// computation and the evaluated_at stamp — never the pass/fail result.
	now func() time.Time
}

// NewEngine wires an Engine over a Store and a scope-cell resolver (slice
// 017's scope.Store).
func NewEngine(store *Store, cells cellResolver) *Engine {
	return &Engine{store: store, cells: cells, now: func() time.Time { return time.Now().UTC() }}
}

// EvaluateControl computes and appends control state for one control.
//
// For each scope cell the control's applicability_expr resolves to (or one
// row with a NULL cell when it resolves to none — the whole-tenant
// degenerate case), the engine:
//
//  1. reads the evidence ledger for the control bounded by `asOf`,
//  2. filters to the freshness window (anti-criterion P0-2: out-of-window
//     evidence never reaches the result),
//  3. computes result + freshness_status deterministically,
//  4. appends one control_evaluations row.
//
// Every row from a single EvaluateControl call shares one eval_run_id.
// `asOf` is the point-in-time horizon — pass a far-future time for live
// evaluation, or a historical instant for replay / as-of queries.
// Idempotent: running twice over the same ledger slice produces identical
// computed columns (AC-3).
func (e *Engine) EvaluateControl(ctx context.Context, controlID uuid.UUID, trigger string, asOf time.Time) (int, error) {
	// Resolve applicable cells OUTSIDE the eval transaction — scope.Store
	// opens its own tenant-GUC transaction. The two transactions are
	// independent reads; the ledger is append-only so there is no
	// read-skew hazard.
	cells, err := e.cells.ControlApplicability(ctx, controlID)
	if err != nil {
		return 0, fmt.Errorf("resolve applicable cells: %w", err)
	}

	evalRunID := uuid.New()
	evaluatedAt := e.now()
	written := 0

	err = e.store.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tx pgx.Tx, tenantID uuid.UUID) error {
		meta, err := e.store.loadControl(ctx, q, tenantID, controlID)
		if err != nil {
			return err
		}
		// The free-form control_ref used by slice-013 pushes is the control
		// UUID string by default; evidence pushed under that string is found
		// by ListEvidenceForControlAsOf's (control_id = $2 OR control_ref = $3).
		controlRef := controlID.String()
		records, err := e.store.loadEvidence(ctx, q, tenantID, controlID, controlRef, asOf)
		if err != nil {
			return err
		}

		// One row per applicable cell. When the control resolves to zero
		// cells we still write one row with a NULL scope_cell_id so the
		// control has an observable state (AC-1 — every control in the
		// catalog has a queryable pass/fail).
		targets := make([]*uuid.UUID, 0, len(cells))
		if len(cells) == 0 {
			targets = append(targets, nil)
		} else {
			for i := range cells {
				id := cells[i].ID
				targets = append(targets, &id)
			}
		}

		for _, cellID := range targets {
			row, err := e.computeRow(ctx, tx, meta, records, cellID, evalRunID, trigger, evaluatedAt)
			if err != nil {
				return err
			}
			if err := e.store.appendEvaluation(ctx, q, tenantID, row, evaluatedAt); err != nil {
				return err
			}
			written++
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return written, nil
}

// computeRow turns a control's metadata + its ledger slice into one
// evaluationRow for a given scope cell. The result is computed by evaluating
// EVERY declared evidence query (rego, sql, jsonpath — slice 495) over the
// in-window record set and rolling the per-query results up through the same
// result-precedence logic the per-record rollup uses. A control with no
// declared query falls back to the per-record evidence rollup. Freshness is
// always computed from raw observed_at.
//
// NOTE: in v1 the evidence ledger's `scope_id` is not yet wired to scope
// cells (slice 017's scope_cells are a separate table from slice 002's
// `scopes`). The engine therefore evaluates the SAME ledger slice for each
// applicable cell — the cell dimension records WHICH cells the state applies
// to, per the control's applicability_expr. Per-cell evidence partitioning
// is a documented follow-up (slice 018's effective-scope intersection is the
// natural home). This is faithful to AC-1's "(control × scope_cell × time)"
// shape: state is recorded per applicable cell.
func (e *Engine) computeRow(ctx context.Context, tx pgx.Tx, meta controlMeta, records []allRecord, cellID *uuid.UUID, evalRunID uuid.UUID, trigger string, now time.Time) (evaluationRow, error) {
	inWindow := inWindowRecords(records, meta.freshnessClass, now)
	freshness := computeFreshness(records, meta.freshnessClass, now)

	result, err := e.evalQueries(ctx, tx, meta, inWindow)
	if err != nil {
		return evaluationRow{}, err
	}

	// no_evidence is authoritative for the result: a control with zero
	// in-window evidence is inconclusive regardless of what a query's default
	// branch returned. This keeps the no_evidence-coherent CHECK constraint
	// satisfied and honors "absence of evidence is not failure".
	if freshness == FreshnessNoEvidence {
		result = ResultInconclusive
	}

	row := evaluationRow{
		controlID:             meta.id,
		scopeCellID:           cellID,
		evalRunID:             evalRunID,
		result:                result,
		freshnessStatus:       freshness,
		evidenceCountInWindow: len(inWindow),
		freshnessClass:        meta.freshnessClass,
		trigger:               trigger,
	}
	if latest := latestObservedAt(records); !latest.IsZero() {
		l := latest
		row.lastObservedAt = &l
	}
	return row, nil
}

// evalQueries runs EVERY declared evidence query (slice 495 closes the
// rego-only gap) and rolls the per-query results up through the result-
// precedence logic. The dispatch contract:
//
//   - no declared queries  → fall back to the per-record evidence rollup
//     (computeResult over the in-window record results).
//   - language == rego     → evalRegoQuery (the existing sandbox).
//   - language == sql      → evalSQLQuery (read-only, evidence-only, timeout).
//   - language == jsonpath → evalJSONPathQuery (in-process over each payload).
//   - any other language   → FAIL LOUD. The slice-009 validator already rejects
//     unknown languages at upload, but the engine must never silently skip a
//     persisted query — that silent no-op was the exact pre-495 defect. An
//     unsupported (or empty-expression) query returns an error, which fails
//     the whole control evaluation rather than quietly producing no state.
//
// Per-query results combine via computeResult precedence (any fail → fail;
// else any pass → pass; else inconclusive; else na), so a control with a mix
// of rego + sql + jsonpath queries rolls up consistently (AC-6).
func (e *Engine) evalQueries(ctx context.Context, tx pgx.Tx, meta controlMeta, inWindow []inWindowRecord) (string, error) {
	if len(meta.queries) == 0 {
		// No declared query: the per-record evidence rollup IS the result.
		return computeResult(inWindow), nil
	}

	perQuery := make([]inWindowRecord, 0, len(meta.queries))
	for _, q := range meta.queries {
		if q.Expression == "" {
			return "", fmt.Errorf("control %s evidence query (language %q): expression is empty", meta.id, q.Language)
		}
		if !SupportedQueryLanguages[q.Language] {
			// FAIL LOUD — never silently skip a persisted query.
			return "", fmt.Errorf("control %s evidence query: unsupported language %q (engine supports rego|sql|jsonpath)", meta.id, q.Language)
		}

		var (
			res string
			err error
		)
		switch q.Language {
		case "rego":
			res, err = evalRegoQuery(ctx, q.Expression, inWindow)
		case "sql":
			res, err = evalSQLQuery(ctx, tx, q.Expression, inWindow)
		case "jsonpath":
			res, err = evalJSONPathQuery(ctx, q.Expression, inWindow)
		}
		if err != nil {
			return "", fmt.Errorf("control %s %s query: %w", meta.id, q.Language, err)
		}
		perQuery = append(perQuery, inWindowRecord{result: res})
	}
	return computeResult(perQuery), nil
}

// EvaluateAll evaluates every active (non-superseded) control for the tenant.
// Used by the scheduled time-based recompute and by Replay. Returns the
// total rows written across all controls.
func (e *Engine) EvaluateAll(ctx context.Context, trigger string, asOf time.Time) (int, error) {
	var controlIDs []uuid.UUID
	err := e.store.inTx(ctx, func(ctx context.Context, q *dbx.Queries, _ pgx.Tx, tenantID uuid.UUID) error {
		rows, err := q.ListActiveControls(ctx, pgUUID(tenantID))
		if err != nil {
			return fmt.Errorf("list active controls: %w", err)
		}
		controlIDs = make([]uuid.UUID, len(rows))
		for i, r := range rows {
			controlIDs[i] = uuid.UUID(r.ID.Bytes)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}

	total := 0
	for _, id := range controlIDs {
		n, err := e.EvaluateControl(ctx, id, trigger, asOf)
		if err != nil {
			return total, fmt.Errorf("evaluate control %s: %w", id, err)
		}
		total += n
	}
	return total, nil
}

// Replay re-evaluates every active control from the ledger with
// trigger=replay. The AC-7 property: deleting every control_evaluations row
// and calling Replay reproduces identical computed state, because the engine
// holds no state of its own — everything derives from the immutable ledger.
// `asOf` pins the horizon so a replay reproduces the state AS OF that
// instant.
func (e *Engine) Replay(ctx context.Context, asOf time.Time) (int, error) {
	return e.EvaluateAll(ctx, TriggerReplay, asOf)
}

// FarFuture is the sentinel horizon for live evaluation — "all evidence up to
// now and then some". Callers that want live state pass this; callers doing
// as-of / replay pass a specific instant.
var FarFuture = time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC)

// evidenceQueryManifest mirrors the slice-009 EvidenceQuery shape stored in
// controls.evidence_queries (JSONB). The engine only needs language +
// expression; other fields are ignored.
type evidenceQueryManifest struct {
	Language   string `json:"language"`
	Expression string `json:"expression"`
}

// parseEvidenceQueries decodes a control's evidence_queries JSONB into the
// engine's query list. Slice 495: the engine evaluates EVERY declared query
// (rego, sql, jsonpath), not just the first rego one — so this returns the
// full list in bundle order rather than picking one. An empty / absent list
// means the control has no declared query and falls back to the per-record
// evidence rollup.
func parseEvidenceQueries(evidenceQueriesJSON []byte) ([]evidenceQueryManifest, error) {
	if len(evidenceQueriesJSON) == 0 {
		return nil, nil
	}
	var queries []evidenceQueryManifest
	if err := json.Unmarshal(evidenceQueriesJSON, &queries); err != nil {
		return nil, fmt.Errorf("parse evidence_queries: %w", err)
	}
	return queries, nil
}

// SupportedQueryLanguages enumerates the evidence-query languages the
// evaluation engine can run. Slice 495 closed the rego-only gap: sql and
// jsonpath now evaluate. (sigma is intentionally absent — it is detect-as-code,
// out of scope per the canvas §4.4 boundary.) A language outside this set FAILS
// LOUD at dispatch (evalQuery returns an error) rather than silently producing
// no state — that silent no-op was the exact pre-495 defect.
var SupportedQueryLanguages = map[string]bool{
	"rego":     true,
	"sql":      true,
	"jsonpath": true,
}
