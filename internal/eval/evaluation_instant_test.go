// Unit tests for the evaluation instant (#2911, #2890): SQL and Rego evidence
// queries judge time windows against the engine's evaluation instant, never
// the wall clock. The SQL half needs Postgres and lives in
// evaluation_instant_integration_test.go.
package eval

import (
	"context"
	"testing"
	"time"
)

// regoWithin24h passes when every record was observed within 24 h of
// time.now_ns(), which the sandbox sets to the evaluation instant.
const regoWithin24h = `package evidence.query
import rego.v1
default result := "fail"
result := "pass" if {
	count(input.records) > 0
	every r in input.records {
		time.now_ns() - time.parse_rfc3339_ns(r.observed_at) < 24 * 60 * 60 * 1000000000
	}
}`

var pinned = time.Date(2020, 1, 1, 12, 0, 0, 0, time.UTC)

func TestEvalRegoQuery_TimeNowIsTheEvaluationInstant(t *testing.T) {
	t.Parallel()
	records := []inWindowRecord{{result: ResultPass, observedAt: pinned.Add(-time.Hour)}}
	for _, tc := range []struct {
		name string
		at   time.Time
		want string
	}{
		// 2020 is years before the wall clock: only EvalTime can make this pass.
		{"one hour after the record", pinned, ResultPass},
		{"two days after the record", pinned.Add(48 * time.Hour), ResultFail},
	} {
		got, err := evalRegoQuery(context.Background(), regoWithin24h, records, tc.at)
		if err != nil {
			t.Fatalf("%s: evalRegoQuery: %v", tc.name, err)
		}
		if got != tc.want {
			t.Errorf("%s: result = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestEvalRegoQuery_ObservedAtIsUTC(t *testing.T) {
	t.Parallel()
	const policy = `package evidence.query
import rego.v1
default result := "fail"
result := "pass" if {
	every r in input.records { endswith(r.observed_at, "Z") }
}`
	plus5 := time.FixedZone("UTC+5", 5*60*60)
	records := []inWindowRecord{{result: ResultPass, observedAt: pinned.In(plus5)}}
	got, err := evalRegoQuery(context.Background(), policy, records, pinned)
	if err != nil {
		t.Fatalf("evalRegoQuery: %v", err)
	}
	if got != ResultPass {
		t.Fatalf("result = %q, want pass: observed_at should reach Rego in UTC", got)
	}
}

// A bundle test whose dates are pinned keeps passing however old they get.
func TestEvaluateFixture_RegoPinnedDatePassesIndefinitely(t *testing.T) {
	t.Parallel()
	queries := []FixtureQuery{{Language: "rego", Expression: regoWithin24h}}
	records := []FixtureRecord{{Result: ResultPass, ObservedAt: pinned.Add(-time.Hour)}}
	got, err := EvaluateFixture(context.Background(), "daily", queries, records, pinned, nil)
	if err != nil {
		t.Fatalf("EvaluateFixture: %v", err)
	}
	if got.Result != ResultPass {
		t.Fatalf("result = %q, want pass", got.Result)
	}
}

func TestQueryInstant(t *testing.T) {
	t.Parallel()
	evaluatedAt := time.Date(2026, 11, 21, 9, 0, 0, 0, time.UTC)
	asOf := time.Date(2026, 3, 31, 23, 59, 59, 0, time.UTC)
	for _, tc := range []struct {
		name string
		asOf time.Time
		want time.Time
	}{
		{"live evaluation uses the evaluation time", FarFuture, evaluatedAt},
		{"an earlier as-of horizon wins", asOf, asOf},
		{"an as-of equal to the evaluation time", evaluatedAt, evaluatedAt},
		{"a later as-of horizon does not move the instant forward", evaluatedAt.Add(time.Hour), evaluatedAt},
	} {
		if got := queryInstant(evaluatedAt, tc.asOf); !got.Equal(tc.want) {
			t.Errorf("%s: queryInstant = %s, want %s", tc.name, got, tc.want)
		}
	}
}

func TestSQLUsesWallClock(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		expr string
		want bool
	}{
		{"SELECT bool_and(observed_at >= now() - interval '1 day') FROM evidence", true},
		{"SELECT bool_and(observed_at >= NOW () - interval '1 day') FROM evidence", true},
		{"SELECT bool_and(observed_at >= current_timestamp - interval '1 day') FROM evidence", true},
		{"SELECT bool_and(observed_at >= clock_timestamp() - interval '1 day') FROM evidence", true},
		{"SELECT bool_and(observed_at >= statement_timestamp()) FROM evidence", true},
		{"SELECT bool_and(observed_at >= transaction_timestamp()) FROM evidence", true},
		{"SELECT bool_and(observed_at::date = current_date) FROM evidence", true},
		{"SELECT bool_and(observed_at >= localtimestamp) FROM evidence", true},
		{"SELECT bool_and(observed_at >= 'now'::timestamptz) FROM evidence", true},
		{"SELECT bool_and(observed_at::date >= 'today'::date) FROM evidence", true},
		{"SELECT bool_and(observed_at >= evaluated_at - interval '24 hours') FROM evidence", false},
		{"SELECT bool_and(observed_at >= evidence.evaluated_at - interval '1 day') FROM evidence", false},
		{"SELECT bool_and((payload->>'known_hosts')::int > 0) FROM evidence", false},
		{"SELECT bool_and(payload->>'snow' = 'x') FROM evidence", false},
	} {
		if got := SQLUsesWallClock(tc.expr); got != tc.want {
			t.Errorf("SQLUsesWallClock(%q) = %v, want %v", tc.expr, got, tc.want)
		}
	}
}
