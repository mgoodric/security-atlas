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
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mgoodric/security-atlas/internal/actionplan"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/scope"
)

// Trigger values mirror the control_evaluations.trigger CHECK vocabulary.
const (
	TriggerIngest    = "ingest"
	TriggerScheduled = "scheduled"
	TriggerManual    = "manual"
	TriggerReplay    = "replay"
	// TriggerExceptionChange: an exception on the control was activated,
	// revoked or expired (#2269).
	TriggerExceptionChange = "exception_change"
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
	// clock reads the evaluation time inside the evaluation's transaction.
	// It is the database clock (dbClock): evidence_records.ingested_at is
	// stamped by the database (DEFAULT now()), and a replay of an
	// evaluation bounds ingested_at by its evaluated_at, so both must come
	// from one clock (#3038). Tests may swap it to simulate an app clock
	// that disagrees with the database.
	clock func(ctx context.Context, q *dbx.Queries) (time.Time, error)
}

// NewEngine wires an Engine over a Store and a scope-cell resolver (slice
// 017's scope.Store).
func NewEngine(store *Store, cells cellResolver) *Engine {
	return &Engine{store: store, cells: cells, clock: dbClock}
}

// dbClock is the database's current time, clock_timestamp() rather than the
// transaction start, so it is read after every row the transaction's next
// statement can see was committed.
func dbClock(ctx context.Context, q *dbx.Queries) (time.Time, error) {
	now, err := q.DBClockNow(ctx)
	if err != nil {
		return time.Time{}, fmt.Errorf("database clock: %w", err)
	}
	return now.Time.UTC(), nil
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
	return e.EvaluateControlBy(ctx, controlID, trigger, "", asOf)
}

// ErrActorNotManual is returned when an actor is passed for a trigger other
// than manual. Only a manual run has a caller to record (#2479); the
// control_evaluations_triggered_by_manual_only CHECK enforces the same rule.
var ErrActorNotManual = errors.New("eval: an actor is recorded only on a manual evaluation")

// ErrUnevaluable marks an evaluation failure that comes from what is stored,
// not from the database or the clock: a malformed applicability_expr,
// evidence_queries or exception predicate, an evidence query that does not
// compile or pass the SQL guard, or a query whose result is not a valid
// result value. Evaluating again reads the same rows and fails the same way,
// so the ingest subscriber terms the message instead of redelivering it
// (#3005). Connection errors, statement timeouts and query runtime errors are
// never wrapped with it: they may pass on a retry.
var ErrUnevaluable = errors.New("eval: control cannot be evaluated as stored")

// unevaluableError marks err as ErrUnevaluable without changing its message.
type unevaluableError struct{ err error }

func (e unevaluableError) Error() string   { return e.err.Error() }
func (e unevaluableError) Unwrap() []error { return []error{e.err, ErrUnevaluable} }

// unevaluable wraps err so errors.Is(err, ErrUnevaluable) holds and every
// sentinel err already wraps still matches.
func unevaluable(err error) error { return unevaluableError{err: err} }

// EvaluateControlBy is EvaluateControl recording who started the run: actor
// is stamped as triggered_by on every row the run appends (#2479). It is the
// caller's principal (credstore.PrincipalOf) for POST
// /v1/controls/{id}/evaluate; "" records no actor. A non-empty actor is
// accepted only with TriggerManual.
func (e *Engine) EvaluateControlBy(ctx context.Context, controlID uuid.UUID, trigger, actor string, asOf time.Time) (int, error) {
	if actor != "" && trigger != TriggerManual {
		return 0, ErrActorNotManual
	}
	// Resolve applicable cells OUTSIDE the eval transaction — scope.Store
	// opens its own tenant-GUC transaction. The two transactions are
	// independent reads; the ledger is append-only so there is no
	// read-skew hazard.
	cells, err := e.cells.ControlApplicability(ctx, controlID)
	if err != nil {
		switch {
		case errors.Is(err, scope.ErrControlNotFound):
			return 0, fmt.Errorf("resolve applicable cells: %w: %w", ErrControlNotFound, err)
		case errors.Is(err, scope.ErrBadApplicability):
			return 0, unevaluable(fmt.Errorf("resolve applicable cells: %w", err))
		}
		return 0, fmt.Errorf("resolve applicable cells: %w", err)
	}

	evalRunID := uuid.New()
	written := 0

	err = e.store.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tx pgx.Tx, tenantID uuid.UUID) error {
		// #3038: evaluated_at is read from the database clock, the clock
		// that stamps ingested_at, before the ledger read below. Every row
		// committed before this point has ingested_at <= evaluatedAt and is
		// visible to that read, so the live result at T and a replay of T
		// both count it whatever the app host's clock says.
		evaluatedAt, err := e.clock(ctx, q)
		if err != nil {
			return err
		}
		instant := queryInstant(evaluatedAt, asOf)

		meta, err := e.store.loadControl(ctx, q, tenantID, controlID)
		if err != nil {
			return err
		}
		// The free-form control_ref used by slice-013 pushes is the control
		// UUID string by default; evidence pushed under that string is found
		// by ListEvidenceForControlAsOf's (control_id = $2 OR control_ref = $3).
		// The unbound records of the kinds the control's queries declare are
		// found alongside them (#2817, internal/evidence/unbound). A routed
		// kind never matches by control_ref (#2931, internal/evidence/routed).
		controlRef := controlID.String()
		records, err := e.store.loadEvidence(ctx, q, tenantID, controlID, controlRef, asOf)
		if err != nil {
			return err
		}
		// #3038: live evaluation applies the same ingested_at horizon a
		// replay does (the SQL bound when asOf is a past instant). A row
		// that committed between the clock read and the ledger read has
		// ingested_at after evaluatedAt; a replay of evaluatedAt would not
		// count it, so the live result does not either. Its own ingest
		// triggers a later evaluation that does.
		//
		// What one clock does not close: a row whose transaction began
		// before evaluatedAt (so ingested_at <= evaluatedAt) but committed
		// after the ledger read. The live result at T misses it and a replay
		// of T counts it. Freeze closes that window with the tenant
		// evidence-freeze lock. Live evaluation does not take it: that would
		// serialise every evaluation against every ingest in the tenant, and
		// the row's own ingest triggers a later evaluation that counts it.
		records = ingestedBy(records, instant)
		// #3037: a replay of an earlier instant applies the exceptions in
		// force then, not today's active set. Live evaluation (instant is
		// the evaluation time) reads the active set as before.
		var exceptions []activeException
		if instant.Before(evaluatedAt) {
			exceptions, err = e.store.loadExceptionsAsOf(ctx, q, tenantID, controlID, instant)
		} else {
			exceptions, err = e.store.loadActiveExceptions(ctx, q, tenantID, controlID)
		}
		if err != nil {
			return err
		}

		// One row per applicable cell. When the control resolves to zero
		// cells we still write one row with a NULL scope_cell_id so the
		// control has an observable state (AC-1 — every control in the
		// catalog has a queryable pass/fail).
		targets := make([]*scope.Cell, 0, len(cells))
		if len(cells) == 0 {
			targets = append(targets, nil)
		} else {
			for i := range cells {
				targets = append(targets, &cells[i])
			}
		}

		for _, cell := range targets {
			row, err := e.computeRow(ctx, tx, meta, records, exceptions, cell, evalRunID, trigger, instant)
			if err != nil {
				return err
			}
			row.triggeredBy = actor
			if err := e.store.appendEvaluation(ctx, q, tenantID, row, evaluatedAt, instant); err != nil {
				return err
			}
			written++
		}
		// #2109: the plans linked to this control move completed <->
		// verified on the evaluation just written, in this transaction.
		// It locks only action_plans rows (in id order), never a risk.
		if _, err := actionplan.ReconcileVerification(ctx, q, controlID); err != nil {
			return err
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
// in-window records of its declared evidence_kind (#2818) and rolling the per-query results up through the same
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
//
// instant is the evaluation instant the queries see (queryInstant): SQL's
// evaluated_at column and Rego's time.now_ns(). The freshness window is
// measured from it too, so a replay of T judges evidence age as at T, not as
// at the wall clock (#1699). Live evaluation's instant is the wall clock.
func (e *Engine) computeRow(ctx context.Context, tx pgx.Tx, meta controlMeta, records []allRecord, exceptions []activeException, cell *scope.Cell, evalRunID uuid.UUID, trigger string, instant time.Time) (evaluationRow, error) {
	inWindow := inWindowRecords(records, meta.freshnessClass, instant)
	freshness := computeFreshness(records, meta.freshnessClass, instant)

	// #2111: only the latest attestation in the window speaks for the
	// attestation outcome; automated evidence keeps "any fail fails".
	result, err := e.evalQueries(ctx, tx, meta, latestAttestationWins(inWindow), instant)
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

	// #2112: an active exception covering this cell waives the normal
	// result. It is applied last so it holds whatever the evidence said,
	// and only while the exception is in force: once it expires or leaves
	// status 'active', the next evaluation is the normal result again. The
	// window is judged at instant, so a replay judges it as at the replayed
	// instant (#3037); for live evaluation instant is the wall clock.
	excepted, err := exceptedIn(exceptions, cell, instant)
	if err != nil {
		// A stored exception predicate that does not parse fails every time.
		return evaluationRow{}, unevaluable(fmt.Errorf("control %s: %w", meta.id, err))
	}
	if excepted {
		result = ResultExcepted
	}

	var cellID *uuid.UUID
	if cell != nil {
		id := cell.ID
		cellID = &id
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
func (e *Engine) evalQueries(ctx context.Context, tx pgx.Tx, meta controlMeta, inWindow []inWindowRecord, instant time.Time) (string, error) {
	if len(meta.queries) == 0 {
		// No declared query: the per-record evidence rollup IS the result.
		return computeResult(inWindow), nil
	}

	perQuery := make([]inWindowRecord, 0, len(meta.queries))
	for _, q := range meta.queries {
		if q.Expression == "" {
			return "", unevaluable(fmt.Errorf("control %s evidence query (language %q): expression is empty", meta.id, q.Language))
		}
		if !SupportedQueryLanguages[q.Language] {
			// FAIL LOUD — never silently skip a persisted query.
			return "", unevaluable(fmt.Errorf("control %s evidence query: unsupported language %q (engine supports rego|sql|jsonpath)", meta.id, q.Language))
		}

		// #2818: each query sees only the records of its declared kind.
		records := recordsOfKind(inWindow, q.EvidenceKind, false)
		var (
			res string
			err error
		)
		switch q.Language {
		case "rego":
			res, err = evalRegoQuery(ctx, q.Expression, records, instant)
		case "sql":
			res, err = evalSQLQuery(ctx, tx, q.Expression, records, instant)
		case "jsonpath":
			res, err = evalJSONPathQuery(ctx, q.Expression, records)
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
//
// One control's failure does not stop the rest (#1859): a broken
// tenant-authored bundle would otherwise leave every control after it in list
// order unevaluated. Each failure is logged with its control and tenant ids,
// and the failures come back as one errors.Join alongside the rows that were
// written.
func (e *Engine) EvaluateAll(ctx context.Context, trigger string, asOf time.Time) (int, error) {
	var (
		controlIDs []uuid.UUID
		tenant     uuid.UUID
	)
	err := e.store.inTx(ctx, func(ctx context.Context, q *dbx.Queries, _ pgx.Tx, tenantID uuid.UUID) error {
		tenant = tenantID
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
	var errs []error
	for _, id := range controlIDs {
		// A cancelled sweep stops here rather than logging and joining one
		// context error per remaining control (#1865).
		if ctxErr := ctx.Err(); ctxErr != nil {
			errs = append(errs, ctxErr)
			break
		}
		n, err := e.EvaluateControl(ctx, id, trigger, asOf)
		if err != nil {
			slog.ErrorContext(ctx, "eval: evaluate control", "control_id", id, "tenant", tenant, "trigger", trigger, "err", err.Error())
			errs = append(errs, fmt.Errorf("evaluate control %s: %w", id, err))
			continue
		}
		total += n
	}
	return total, errors.Join(errs...)
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

// queryInstant is the evaluation instant a SQL or Rego evidence query sees
// (#2911): the evaluation's own time, or the as-of horizon when that is
// earlier, so a replay or an audit-period re-evaluation judges a time window
// as of that instant rather than as of the wall clock. Live evaluation passes
// FarFuture, which is never earlier.
func queryInstant(evaluatedAt, asOf time.Time) time.Time {
	if asOf.Before(evaluatedAt) {
		return asOf.UTC()
	}
	return evaluatedAt.UTC()
}

// ingestedBy keeps the records whose ingested_at is at or before horizon,
// in order (#3038).
func ingestedBy(records []allRecord, horizon time.Time) []allRecord {
	out := records[:0:0]
	for _, r := range records {
		if !r.ingestedAt.After(horizon) {
			out = append(out, r)
		}
	}
	return out
}

// FarFuture is the sentinel horizon for live evaluation — "all evidence up to
// now and then some". Callers that want live state pass this; callers doing
// as-of / replay pass a specific instant.
var FarFuture = time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC)

// evidenceQueryManifest mirrors the slice-009 EvidenceQuery shape stored in
// controls.evidence_queries (JSONB). The engine needs language, expression
// and evidence_kind (#2818: the query sees only records of that kind; empty
// means every kind); other fields are ignored.
type evidenceQueryManifest struct {
	Language     string `json:"language"`
	Expression   string `json:"expression"`
	EvidenceKind string `json:"evidence_kind,omitempty"`
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
		return nil, unevaluable(fmt.Errorf("parse evidence_queries: %w", err))
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
