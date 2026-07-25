// Unit tests for EvaluateFixture (fixture.go) — the DB-free seam the
// control-bundle test runner (slice 496) dispatches through. These prove the
// fixture path produces the SAME result the live engine would, over an
// in-memory record set, with NO database (AC-9).
package eval

import (
	"context"
	"errors"
	"testing"
	"time"
)

var fixtureNow = time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

func obs(daysAgo int) time.Time { return fixtureNow.Add(-time.Duration(daysAgo) * 24 * time.Hour) }

// No declared query → per-record evidence rollup, identical to the live
// engine's no-query fallback.
func TestEvaluateFixture_NoQuery_PerRecordRollup(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		records []FixtureRecord
		want    string
	}{
		{"all-pass", []FixtureRecord{{Result: ResultPass, ObservedAt: obs(0)}}, ResultPass},
		{"one-fail", []FixtureRecord{{Result: ResultPass, ObservedAt: obs(0)}, {Result: ResultFail, ObservedAt: obs(0)}}, ResultFail},
		{"empty-inconclusive", nil, ResultInconclusive},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := EvaluateFixture(context.Background(), "daily", nil, tc.records, fixtureNow, nil)
			if err != nil {
				t.Fatalf("EvaluateFixture: %v", err)
			}
			if got.Result != tc.want {
				t.Fatalf("result = %q, want %q", got.Result, tc.want)
			}
		})
	}
}

// A Rego query evaluates fully in-process (no DB) and rolls up correctly.
func TestEvaluateFixture_RegoQuery_NoDB(t *testing.T) {
	t.Parallel()
	policy := `package evidence.query
default result := "fail"
result := "pass" if {
  count(input.records) > 0
  every r in input.records { r.result == "pass" }
}`
	queries := []FixtureQuery{{Language: "rego", Expression: policy}}

	pass, err := EvaluateFixture(context.Background(), "daily", queries,
		[]FixtureRecord{{Result: ResultPass, ObservedAt: obs(0)}}, fixtureNow, nil)
	if err != nil {
		t.Fatalf("pass case: %v", err)
	}
	if pass.Result != ResultPass {
		t.Fatalf("all-pass result = %q, want pass", pass.Result)
	}

	fail, err := EvaluateFixture(context.Background(), "daily", queries,
		[]FixtureRecord{{Result: ResultPass, ObservedAt: obs(0)}, {Result: ResultFail, ObservedAt: obs(0)}}, fixtureNow, nil)
	if err != nil {
		t.Fatalf("fail case: %v", err)
	}
	if fail.Result != ResultFail {
		t.Fatalf("one-fail result = %q, want fail", fail.Result)
	}
}

// The no_evidence override: ZERO records (no ledger evidence at all) yields
// inconclusive regardless of the query's default branch — the exact live
// engine behaviour (engine.go computeRow). This is distinct from "records exist
// but aged out of the window" — see the stale test below.
func TestEvaluateFixture_NoEvidenceOverride(t *testing.T) {
	t.Parallel()
	policy := `package evidence.query
default result := "fail"
result := "pass" if { count(input.records) > 0 }`
	queries := []FixtureQuery{{Language: "rego", Expression: policy}}
	res, err := EvaluateFixture(context.Background(), "daily", queries, nil, fixtureNow, nil)
	if err != nil {
		t.Fatalf("EvaluateFixture: %v", err)
	}
	if res.Result != ResultInconclusive {
		t.Fatalf("no-evidence result = %q, want inconclusive", res.Result)
	}
	if res.FreshnessStatus != FreshnessNoEvidence {
		t.Fatalf("freshness = %q, want no_evidence", res.FreshnessStatus)
	}
	if res.EvidenceCountInWindow != 0 {
		t.Fatalf("in-window count = %d, want 0", res.EvidenceCountInWindow)
	}
}

// Records that exist but aged OUT of the freshness window are `stale` (not
// no_evidence). The no_evidence override therefore does NOT fire: the query
// runs over an empty in-window set and returns its default branch. This pins
// the engine-faithful semantics — a subtle but load-bearing distinction the
// runner must reproduce, not "fix".
func TestEvaluateFixture_StaleRecords_QueryDefaultApplies(t *testing.T) {
	t.Parallel()
	policy := `package evidence.query
default result := "fail"
result := "pass" if { count(input.records) > 0 }`
	queries := []FixtureQuery{{Language: "rego", Expression: policy}}
	// daily window is 7d; a record 30 days old exists but is out of window.
	res, err := EvaluateFixture(context.Background(), "daily", queries,
		[]FixtureRecord{{Result: ResultPass, ObservedAt: obs(30)}}, fixtureNow, nil)
	if err != nil {
		t.Fatalf("EvaluateFixture: %v", err)
	}
	if res.FreshnessStatus != FreshnessStale {
		t.Fatalf("freshness = %q, want stale", res.FreshnessStatus)
	}
	// in-window is empty so the Rego query returns its default ("fail").
	if res.Result != ResultFail {
		t.Fatalf("stale result = %q, want fail (query default over empty in-window set)", res.Result)
	}
	if res.EvidenceCountInWindow != 0 {
		t.Fatalf("in-window count = %d, want 0", res.EvidenceCountInWindow)
	}
}

// A JSON-path query evaluates over payloads in-process (slice 495), no DB.
func TestEvaluateFixture_JSONPathQuery_NoDB(t *testing.T) {
	t.Parallel()
	queries := []FixtureQuery{{Language: "jsonpath", Expression: "$.encrypted"}}
	res, err := EvaluateFixture(context.Background(), "daily", queries,
		[]FixtureRecord{{Result: ResultPass, ObservedAt: obs(0), Payload: []byte(`{"encrypted":true}`)}}, fixtureNow, nil)
	if err != nil {
		t.Fatalf("EvaluateFixture: %v", err)
	}
	if res.Result != ResultPass {
		t.Fatalf("encrypted=true result = %q, want pass", res.Result)
	}

	res2, err := EvaluateFixture(context.Background(), "daily", queries,
		[]FixtureRecord{{Result: ResultPass, ObservedAt: obs(0), Payload: []byte(`{"encrypted":false}`)}}, fixtureNow, nil)
	if err != nil {
		t.Fatalf("EvaluateFixture: %v", err)
	}
	if res2.Result != ResultFail {
		t.Fatalf("encrypted=false result = %q, want fail", res2.Result)
	}
}

// A SQL query with no Tx returns ErrFixtureSQLNeedsDB — never a false pass.
func TestEvaluateFixture_SQLWithoutTx_ReturnsTypedError(t *testing.T) {
	t.Parallel()
	queries := []FixtureQuery{{Language: "sql", Expression: "SELECT true FROM evidence"}}
	_, err := EvaluateFixture(context.Background(), "daily", queries,
		[]FixtureRecord{{Result: ResultPass, ObservedAt: obs(0)}}, fixtureNow, nil)
	if !errors.Is(err, ErrFixtureSQLNeedsDB) {
		t.Fatalf("err = %v, want ErrFixtureSQLNeedsDB", err)
	}
}

// An unsupported language fails loud (FixtureQueryError) — never silently
// skipped.
func TestEvaluateFixture_UnsupportedLanguage_FailsLoud(t *testing.T) {
	t.Parallel()
	queries := []FixtureQuery{{Language: "sigma", Expression: "anything"}}
	_, err := EvaluateFixture(context.Background(), "daily", queries,
		[]FixtureRecord{{Result: ResultPass, ObservedAt: obs(0)}}, fixtureNow, nil)
	var fqe *FixtureQueryError
	if !errors.As(err, &fqe) {
		t.Fatalf("err = %v, want a *FixtureQueryError", err)
	}
	if fqe.Language != "sigma" {
		t.Fatalf("FixtureQueryError.Language = %q, want sigma", fqe.Language)
	}
}

// An empty expression is rejected loud.
func TestEvaluateFixture_EmptyExpression_FailsLoud(t *testing.T) {
	t.Parallel()
	queries := []FixtureQuery{{Language: "rego", Expression: ""}}
	_, err := EvaluateFixture(context.Background(), "daily", queries,
		[]FixtureRecord{{Result: ResultPass, ObservedAt: obs(0)}}, fixtureNow, nil)
	if err == nil {
		t.Fatal("empty expression must fail loud")
	}
}

// A broken Rego query surfaces as a FixtureQueryError wrapping ErrRegoQuery.
func TestEvaluateFixture_BrokenRego_WrapsRegoError(t *testing.T) {
	t.Parallel()
	queries := []FixtureQuery{{Language: "rego", Expression: "this is not @@@ valid"}}
	_, err := EvaluateFixture(context.Background(), "daily", queries,
		[]FixtureRecord{{Result: ResultPass, ObservedAt: obs(0)}}, fixtureNow, nil)
	if !errors.Is(err, ErrRegoQuery) {
		t.Fatalf("err = %v, want it to wrap ErrRegoQuery", err)
	}
}

// FixtureQueryError.Error and Unwrap behave.
func TestFixtureQueryError_String(t *testing.T) {
	t.Parallel()
	base := errors.New("boom")
	e := &FixtureQueryError{Language: "rego", Err: base}
	if got := e.Error(); got == "" {
		t.Fatal("Error() empty")
	}
	if !errors.Is(e, base) {
		t.Fatal("Unwrap must expose the base error")
	}
}
