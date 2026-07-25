// runner.go — the control-bundle test runner: feed each test case's fixtures
// through the live evaluation engine (internal/eval) and assert the produced
// state equals the case's expected_state.
//
// The runner is deliberately thin. All evaluation logic lives in
// eval.EvaluateFixture, which dispatches through the SAME per-language
// evaluators (rego|sql|jsonpath) the live engine uses (anti-criterion
// P0-496-1). The runner's job is orchestration: load the bundle's queries +
// freshness class, build the in-memory record set for each case, call the
// engine, and compare. It performs no writes and uses no live ledger
// (invariant #2 / P0-496-2 / AC-9).
package bundletest

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mgoodric/security-atlas/internal/control"
	"github.com/mgoodric/security-atlas/internal/eval"
)

// CaseResult is the outcome of running one test case.
type CaseResult struct {
	Name          string `json:"name"`
	SourceFile    string `json:"source_file"`
	ExpectedState string `json:"expected_state"`
	// ActualState is the control state the engine produced. Empty when Err is
	// non-empty (the query errored before producing a state).
	ActualState string `json:"actual_state,omitempty"`
	// FreshnessStatus is the freshness classification the engine computed
	// (fresh|stale|no_evidence) — surfaced so an author can see WHY a
	// no-evidence fixture came back inconclusive.
	FreshnessStatus string `json:"freshness_status,omitempty"`
	// Passed is true when the case ran AND ActualState == ExpectedState.
	Passed bool `json:"passed"`
	// Err, when non-empty, is the evaluation error (a query that could not run,
	// e.g. a Rego compile error or a SQL fixture with no DB). A case with a
	// non-empty Err is NOT passing.
	Err string `json:"error,omitempty"`
}

// Report is the aggregate result of running every test case in a bundle.
type Report struct {
	BundleID string       `json:"bundle_id"`
	Cases    []CaseResult `json:"cases"`
	Passed   int          `json:"passed"`
	Failed   int          `json:"failed"`
	Errored  int          `json:"errored"`
}

// AllPassed reports whether every case passed (no failures, no errors). The CLI
// uses this to set the process exit code (AC-5 / P0-496-6: exit non-zero on any
// failure).
func (r *Report) AllPassed() bool { return r.Failed == 0 && r.Errored == 0 }

// Options configures a run.
type Options struct {
	// Now is the evaluation instant used when a case does not pin its own
	// evaluated_at. Defaults to time.Now().UTC() when zero. Injectable so the
	// runner's own tests are deterministic.
	Now time.Time
	// Tx is an OPTIONAL Postgres transaction, required only to evaluate SQL
	// evidence queries (the SQL sandbox runs inside a read-only subtransaction
	// — see internal/eval/sql.go). Leave nil for Rego/JSON-path bundles; the
	// run then needs no database at all (AC-9). A bundle with a SQL query and a
	// nil Tx reports those cases as errors, never false passes.
	Tx pgx.Tx
}

// Run loads a bundle's test cases and evaluates every one, returning the
// aggregate report. bundleDir is the directory containing control.yaml.
//
// Run reuses control.ParseDirectory to read + structurally validate the bundle
// (so a malformed manifest is caught here, not mid-evaluation), then
// LoadTestCases to read tests/*.yaml. A bundle with no test cases yields an
// empty report (the caller decides whether "no tests" is an error — the CLI
// treats it as a warning, not a failure).
func Run(ctx context.Context, bundleDir string, opts Options) (*Report, error) {
	bundle, err := control.ParseDirectory(bundleDir)
	if err != nil {
		return nil, fmt.Errorf("bundletest: load bundle: %w", err)
	}

	cases, err := LoadTestCases(bundleDir)
	if err != nil {
		return nil, err
	}

	queries := bundleQueries(bundle)
	freshnessClass := bundle.Manifest.FreshnessClass

	now := opts.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}

	report := &Report{BundleID: bundle.Manifest.BundleID, Cases: make([]CaseResult, 0, len(cases))}
	for i := range cases {
		cr := runCase(ctx, &cases[i], queries, freshnessClass, now, opts.Tx)
		report.Cases = append(report.Cases, cr)
		switch {
		case cr.Err != "":
			report.Errored++
		case cr.Passed:
			report.Passed++
		default:
			report.Failed++
		}
	}
	return report, nil
}

// runCase evaluates one test case through eval.EvaluateFixture and compares the
// produced state to the case's expected_state.
func runCase(ctx context.Context, c *TestCase, queries []eval.FixtureQuery, freshnessClass string, defaultNow time.Time, tx pgx.Tx) CaseResult {
	cr := CaseResult{
		Name:          c.Name,
		SourceFile:    c.sourceFile,
		ExpectedState: c.ExpectedState,
	}

	now := defaultNow
	if c.EvaluatedAt != nil {
		now = c.EvaluatedAt.UTC()
	}

	records, err := toFixtureRecords(c.Records, now)
	if err != nil {
		cr.Err = err.Error()
		return cr
	}

	res, err := eval.EvaluateFixture(ctx, freshnessClass, queries, records, now, tx)
	if err != nil {
		cr.Err = err.Error()
		return cr
	}

	cr.ActualState = res.Result
	cr.FreshnessStatus = res.FreshnessStatus
	cr.Passed = res.Result == c.ExpectedState
	return cr
}

// bundleQueries extracts the (language, expression) pairs from a parsed
// bundle's manifest — the same set the engine reads from evidence_queries[].
func bundleQueries(b *control.Bundle) []eval.FixtureQuery {
	out := make([]eval.FixtureQuery, 0, len(b.Manifest.EvidenceQueries))
	for _, q := range b.Manifest.EvidenceQueries {
		out = append(out, eval.FixtureQuery{Language: q.Language, Expression: q.Expression})
	}
	return out
}

// toFixtureRecords converts a case's author-declared records into the engine's
// eval.FixtureRecord shape: defaulting a missing observed_at to the case's
// evaluation instant (so a record is in-window by default), and marshalling the
// YAML payload map to JSON bytes for the SQL / JSON-path evaluators.
func toFixtureRecords(records []FixtureRecord, now time.Time) ([]eval.FixtureRecord, error) {
	out := make([]eval.FixtureRecord, 0, len(records))
	for i, r := range records {
		observedAt := now
		if r.ObservedAt != nil {
			observedAt = r.ObservedAt.UTC()
		}
		var payload []byte
		if len(r.Payload) > 0 {
			b, err := json.Marshal(r.Payload)
			if err != nil {
				return nil, fmt.Errorf("records[%d].payload: marshal to JSON: %w", i, err)
			}
			payload = b
		}
		out = append(out, eval.FixtureRecord{
			Result:     r.Result,
			ObservedAt: observedAt,
			Payload:    payload,
		})
	}
	return out, nil
}
