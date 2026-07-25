// fixture.go — the exported, DB-free entry point the control-bundle test
// runner (slice 496) feeds fixture evidence through.
//
// The control-bundle test runner lets a control author unit-test a bundle's
// evidence queries: declare a set of fixture evidence records + an expected
// pass/fail, and assert the control evaluates as expected. The whole value of
// the feature is that a bundle that passes its tests behaves IDENTICALLY when
// uploaded and evaluated live — so the runner must dispatch through the SAME
// evaluation code the live path uses, not a separate "test interpreter"
// (anti-criterion P0-496-1).
//
// EvaluateFixture is that shared seam. The live engine's computeRow
// (engine.go) does exactly three things to turn a ledger slice into a result:
//
//  1. filter to the freshness window (inWindowRecords),
//  2. run every declared evidence query and roll the results up (evalQueries),
//  3. let no_evidence override the result to inconclusive (freshness gate).
//
// EvaluateFixture performs the identical three steps over an IN-MEMORY record
// set instead of a ledger read. It calls the SAME unexported helpers — there
// is no parallel evaluation logic to drift. The only difference is the source
// of the records: the live path reads them from the append-only ledger; the
// fixture path receives them as plain Go structs the author declared. No live
// ledger, no tenant data, no Store, no Postgres (constitutional invariant #2 /
// anti-criterion P0-496-2 / AC-9).
//
// The Rego and JSON-path query languages evaluate fully in-process here. The
// SQL language requires a Postgres transaction (the SQL sandbox materialises
// the records into a CTE and runs the author SELECT inside a read-only
// subtransaction — see sql.go); when the caller supplies no *pgx.Tx,
// EvaluateFixture reports a typed "needs DB" error for a SQL query rather than
// silently passing it (fail-loud, mirroring the engine's never-skip-a-query
// posture). The runner surfaces that as a test error, so AC-9 ("succeeds with
// no Postgres available") holds for the Rego + JSON-path languages while SQL
// fixtures degrade to an explicit, actionable error instead of a false pass.
package eval

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// ErrFixtureSQLNeedsDB is returned by EvaluateFixture when a bundle declares a
// SQL evidence query but the caller supplied no *pgx.Tx. SQL evaluation runs
// inside a read-only Postgres subtransaction (sql.go); it cannot run purely
// in-memory. This is a typed, actionable error — never a silent pass — so the
// runner reports the SQL test case as an error and the author knows to point
// the runner at a database.
var ErrFixtureSQLNeedsDB = errors.New("eval: sql evidence query requires a database transaction; rerun the control test runner with a Postgres connection")

// FixtureRecord is one fixture evidence record an author declares for a
// control-bundle test case. It is the in-memory analogue of one
// evidence_records row, carrying ONLY the fields the evaluation engine reads:
//
//   - Result     — the per-record evidence result (pass|fail|na|inconclusive);
//     used by the Rego path and by the no-declared-query per-record rollup.
//   - ObservedAt — the record's observation time; drives the freshness-window
//     filter exactly as observed_at does on a live row.
//   - Payload    — the record's JSONB payload bytes; the SQL + JSON-path query
//     evaluators read it. nil/empty is fine for Rego-only fixtures.
//
// It deliberately omits tenant id, scope id, hash, and every other ingestion-
// side column: a fixture is author-supplied test data, never a ledger row, and
// the evaluator never needs those fields to compute a result.
type FixtureRecord struct {
	Result     string
	ObservedAt time.Time
	Payload    []byte
}

// FixtureQuery is one declared evidence query the runner evaluates over the
// fixture record set. It mirrors the (language, expression) pair the engine
// reads from a control's evidence_queries[] — the runner extracts these from
// the parsed bundle manifest and hands them here, so the same dispatch
// (rego|sql|jsonpath, fail-loud on anything else) applies.
type FixtureQuery struct {
	Language   string
	Expression string
}

// FixtureResult is the outcome of evaluating one fixture record set against a
// control's queries. Result is the rolled-up control state
// (pass|fail|na|inconclusive); FreshnessStatus is the freshness classification
// (fresh|stale|no_evidence) computed from the same record set. The runner
// asserts Result against the author's expected_state; FreshnessStatus is
// surfaced for the report so an author can see WHY a no-evidence fixture came
// back inconclusive.
type FixtureResult struct {
	Result          string
	FreshnessStatus string
	// EvidenceCountInWindow is how many fixture records fell inside the
	// freshness window — surfaced so an author debugging an unexpected
	// inconclusive can see whether their fixtures aged out of the window.
	EvidenceCountInWindow int
}

// EvaluateFixture computes the control state for a single test case: the same
// computation computeRow performs on a live ledger slice, run over an
// in-memory fixture record set.
//
//   - freshnessClass bounds the in-window filter (canvas §2.3); pass the
//     control's freshness_class. An empty/unknown class falls back to monthly,
//     identically to the live path.
//   - queries are the control's declared evidence queries (zero queries falls
//     back to the per-record evidence rollup, exactly as the live engine does).
//   - now is the evaluation instant the freshness window is measured against;
//     the runner passes a deterministic now so a test case is reproducible.
//   - tx is OPTIONAL: required only when a query's language is sql. Pass nil
//     for Rego/JSON-path-only bundles (the common case) and the call needs no
//     database at all (AC-9). A SQL query with a nil tx returns
//     ErrFixtureSQLNeedsDB.
//
// The result honors the same no_evidence override the live engine applies: a
// fixture set with zero in-window records yields inconclusive regardless of
// what a query's default branch returned ("absence of evidence is not
// failure").
//
// This function performs NO writes and touches NO live evidence: it is a pure
// consumer of the author's in-memory fixtures (invariant #2 / P0-496-2).
func EvaluateFixture(ctx context.Context, freshnessClass string, queries []FixtureQuery, records []FixtureRecord, now time.Time, tx pgx.Tx) (FixtureResult, error) {
	// Convert the author's fixtures into the engine's internal allRecord
	// shape, then run the IDENTICAL pipeline computeRow uses.
	all := make([]allRecord, 0, len(records))
	for _, r := range records {
		all = append(all, allRecord{
			result:     r.Result,
			observedAt: r.ObservedAt,
			payload:    r.Payload,
		})
	}

	inWindow := inWindowRecords(all, freshnessClass, now)
	freshness := computeFreshness(all, freshnessClass, now)

	result, err := evalFixtureQueries(ctx, queries, inWindow, tx)
	if err != nil {
		return FixtureResult{}, err
	}

	// no_evidence is authoritative — identical to engine.go computeRow.
	if freshness == FreshnessNoEvidence {
		result = ResultInconclusive
	}

	return FixtureResult{
		Result:                result,
		FreshnessStatus:       freshness,
		EvidenceCountInWindow: len(inWindow),
	}, nil
}

// evalFixtureQueries is the fixture-side mirror of engine.go's evalQueries. It
// uses the SAME per-language dispatch (rego|sql|jsonpath, fail-loud on anything
// else) and the SAME computeResult precedence rollup, so a fixture run is
// behaviourally identical to a live evaluation of the same queries over the
// same records. The ONLY divergence: a SQL query with no transaction returns
// ErrFixtureSQLNeedsDB instead of running against the live connection the
// engine always holds.
func evalFixtureQueries(ctx context.Context, queries []FixtureQuery, inWindow []inWindowRecord, tx pgx.Tx) (string, error) {
	if len(queries) == 0 {
		// No declared query: the per-record evidence rollup IS the result —
		// identical to the live engine's no-query fallback.
		return computeResult(inWindow), nil
	}

	perQuery := make([]inWindowRecord, 0, len(queries))
	for _, q := range queries {
		if q.Expression == "" {
			return "", &FixtureQueryError{Language: q.Language, Err: errors.New("expression is empty")}
		}
		if !SupportedQueryLanguages[q.Language] {
			// FAIL LOUD — never silently skip a persisted query (mirrors the
			// engine's exact pre-495 defect avoidance).
			return "", &FixtureQueryError{Language: q.Language, Err: errors.New("unsupported language (engine supports rego|sql|jsonpath)")}
		}

		var (
			res string
			err error
		)
		switch q.Language {
		case "rego":
			res, err = evalRegoQuery(ctx, q.Expression, inWindow)
		case "sql":
			if tx == nil {
				return "", ErrFixtureSQLNeedsDB
			}
			res, err = evalSQLQuery(ctx, tx, q.Expression, inWindow)
		case "jsonpath":
			res, err = evalJSONPathQuery(ctx, q.Expression, inWindow)
		}
		if err != nil {
			return "", &FixtureQueryError{Language: q.Language, Err: err}
		}
		perQuery = append(perQuery, inWindowRecord{result: res})
	}
	return computeResult(perQuery), nil
}

// FixtureQueryError wraps a per-query evaluation failure with the language that
// produced it, so the runner can report "the rego query errored" distinctly
// from a flat assertion miss. It unwraps to the underlying evaluator error
// (ErrRegoQuery / ErrSQLQuery / ErrJSONPathQuery) for errors.Is checks.
type FixtureQueryError struct {
	Language string
	Err      error
}

func (e *FixtureQueryError) Error() string {
	return "eval: " + e.Language + " evidence query failed: " + e.Err.Error()
}

func (e *FixtureQueryError) Unwrap() error { return e.Err }
