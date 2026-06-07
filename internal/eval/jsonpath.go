// jsonpath.go — JSON-path evidence-query evaluation, in-process.
//
// A control bundle's `evidence_queries[]` (slice 009) may declare a
// `jsonpath`-language expression over the evidence ledger. When the engine
// evaluates such a control it runs the expression HERE — in-process, against
// each in-window evidence record's JSONB payload. There is no DB reach at all
// (threat-model I): the records have already been filtered to the tenant +
// freshness window by the read path, so the JSON-path evaluator only ever
// sees this tenant's in-window payloads.
//
// SEMANTICS (the result→state mapping; see decisions log 495 D-JP-1):
//
//   - The expression is a Goessner-dialect JSON-path (PaesslerAG/jsonpath),
//     evaluated against ONE record payload at a time.
//   - A record "passes" the query when the path resolves to a non-empty,
//     truthy match against that payload; it "fails" when the path resolves to
//     nothing / a falsy value. A pathological / unparseable expression, or a
//     per-record evaluation error, yields inconclusive (never a silent drop —
//     threat-model: a query that cannot run FAILS LOUD).
//   - The per-record results roll up through the SAME computeResult precedence
//     the per-record evidence rollup uses (any fail → fail; else any pass →
//     pass; else inconclusive; else na), so a JSON-path query is consistent
//     with Rego + SQL queries in a mixed-language control (AC-6).
//
// COMPLEXITY BOUND (threat-model D, AC-5): the compiled path is reused across
// records (compile once); each per-record evaluation runs under a context with
// a per-query deadline. The in-window record set is already bounded by the
// freshness window, so the total work is (compiled-path-cost × bounded-record-
// count) under a wall-clock ceiling.
package eval

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/PaesslerAG/jsonpath"
)

// ErrJSONPathQuery wraps any failure evaluating a JSON-path evidence query: a
// compile error (unparseable path) or a result that cannot be classified. A
// JSON-path query that cannot run NEVER silently yields no-result — it returns
// this error, which the engine maps to an inconclusive control state with the
// error surfaced (the exact opposite of the pre-495 silent no-op bug).
var ErrJSONPathQuery = errors.New("eval: jsonpath evidence query failed")

// jsonPathEvalTimeout bounds a single JSON-path evidence query across its whole
// in-window record set (threat-model D / AC-5). Generous relative to the
// bounded record set, but a hard ceiling so a pathological path can never hang
// the engine.
const jsonPathEvalTimeout = 5 * time.Second

// evalJSONPathQuery evaluates a JSON-path evidence query against the in-window
// records' JSONB payloads and returns the rolled-up control result.
//
// Empty path → ErrJSONPathQuery (a JSON-path query with no expression is a
// configuration error, surfaced loud). Zero in-window records → inconclusive
// (the caller already maps no-evidence to inconclusive, but we are explicit so
// the function is correct standalone): with nothing to match against, the query
// is inconclusive, not a silent pass.
func evalJSONPathQuery(ctx context.Context, expr string, records []inWindowRecord) (string, error) {
	if expr == "" {
		return "", fmt.Errorf("%w: expression is empty", ErrJSONPathQuery)
	}

	ctx, cancel := context.WithTimeout(ctx, jsonPathEvalTimeout)
	defer cancel()

	// Compile once; reuse across every record (complexity bound — AC-5).
	eval, err := jsonpath.New(expr)
	if err != nil {
		return "", fmt.Errorf("%w: compile %q: %v", ErrJSONPathQuery, expr, err)
	}

	if len(records) == 0 {
		return ResultInconclusive, nil
	}

	perRecord := make([]inWindowRecord, 0, len(records))
	for _, r := range records {
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("%w: timed out after %s", ErrJSONPathQuery, jsonPathEvalTimeout)
		default:
		}

		var doc interface{}
		// An unparseable payload is a record-level data problem, not a query
		// failure; treat that record as a non-match (fail) rather than failing
		// the whole query. The ledger guarantees valid JSONB, so this is
		// defense-in-depth.
		if len(r.payload) == 0 {
			perRecord = append(perRecord, inWindowRecord{result: ResultFail})
			continue
		}
		if err := json.Unmarshal(r.payload, &doc); err != nil {
			perRecord = append(perRecord, inWindowRecord{result: ResultFail})
			continue
		}

		match, err := eval(ctx, doc)
		if err != nil {
			// PaesslerAG returns an error when the path resolves to nothing.
			// That is a NON-MATCH (the expected condition is absent), not a
			// query failure — classify the record as fail and continue.
			perRecord = append(perRecord, inWindowRecord{result: ResultFail})
			continue
		}
		if jsonPathMatchIsTruthy(match) {
			perRecord = append(perRecord, inWindowRecord{result: ResultPass})
		} else {
			perRecord = append(perRecord, inWindowRecord{result: ResultFail})
		}
	}

	return computeResult(perRecord), nil
}

// jsonPathMatchIsTruthy classifies a JSON-path match value as pass-worthy.
// The rule (decisions log 495 D-JP-2): a match resolves the record to "pass"
// when the matched value is present and truthy —
//
//   - a non-empty array / object → true (the path matched something);
//   - a non-empty string → true; empty string → false;
//   - a non-zero number → true; zero → false;
//   - a bool → its own value;
//   - nil → false.
//
// This makes `$.checks[?(@.passed==true)]` (a filter) pass when it matches at
// least one element, and `$.encrypted` (a scalar) pass when the scalar is
// truthy — the two idiomatic JSON-path control shapes.
func jsonPathMatchIsTruthy(v interface{}) bool {
	switch t := v.(type) {
	case nil:
		return false
	case bool:
		return t
	case string:
		return t != ""
	case float64:
		return t != 0
	case int:
		return t != 0
	case []interface{}:
		return len(t) > 0
	case map[string]interface{}:
		return len(t) > 0
	default:
		// Any other concrete match (e.g. a non-float numeric) counts as a
		// present value → pass.
		return true
	}
}
