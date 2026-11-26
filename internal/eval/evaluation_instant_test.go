// Unit tests for the evaluation instant (#2911, #2890): SQL and Rego evidence
// queries judge time windows against the engine's evaluation instant, never
// the wall clock. The SQL half needs Postgres and lives in
// evaluation_instant_integration_test.go.
package eval

import (
	"context"
	"strings"
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

func TestSQLUnguardedTimestampCasts(t *testing.T) {
	t.Parallel()
	guardOn := func(v string) string {
		return v + " ~ '" + RFC3339Pattern + "' AND pg_input_is_valid(" + v + ", 'timestamptz')"
	}
	guard := func(f string) string { return guardOn("payload->>'" + f + "'") }
	prefix := `payload->>'last_seen_at' ~ '^[0-9]{4}-[0-9]{2}-[0-9]{2}[T ][0-9]{2}:[0-9]{2}'`
	ls := "payload->>'last_seen_at'"
	for _, tc := range []struct {
		name string
		expr string
		want []string
	}{
		{"no cast", "SELECT bool_and(observed_at >= evaluated_at) FROM evidence", nil},
		{"bare cast", "SELECT max((payload->>'last_seen_at')::timestamptz) FROM evidence", []string{ls}},
		{"start-anchored prefix guard", "SELECT CASE WHEN " + prefix + " THEN (payload->>'last_seen_at')::timestamptz END FROM evidence", []string{ls}},
		{"regex without pg_input_is_valid", "SELECT CASE WHEN payload->>'last_seen_at' ~ '" + RFC3339Pattern + "' THEN (payload->>'last_seen_at')::timestamptz END FROM evidence", []string{ls}},
		{"pg_input_is_valid without regex", "SELECT CASE WHEN pg_input_is_valid(payload->>'last_seen_at', 'timestamptz') THEN (payload->>'last_seen_at')::timestamptz END FROM evidence", []string{ls}},
		{"full guard", "SELECT CASE WHEN " + guard("last_seen_at") + " THEN (payload->>'last_seen_at')::timestamptz END FROM evidence", nil},
		{"guard on another field", "SELECT CASE WHEN " + guard("created_at") + " THEN (payload->>'last_seen_at')::timestamptz END FROM evidence", []string{ls}},
		{"date cast, spaced", "SELECT ( payload ->> 'due' ) :: DATE FROM evidence", []string{"payload->>'due'"}},
		{"one guarded, one not", "SELECT CASE WHEN " + guard("a") + " THEN (payload->>'a')::timestamptz END, (payload->>'b')::timestamp FROM evidence", []string{"payload->>'b'"}},

		// #2973: the other conversion forms.
		{"CAST AS", "SELECT max(CAST(payload->>'t' AS timestamptz)) FROM evidence", []string{"payload->>'t'"}},
		{"CAST AS, guarded", "SELECT CASE WHEN " + guard("t") + " THEN CAST(payload->>'t' AS timestamptz) END FROM evidence", nil},
		{"CAST to text is not a time cast", "SELECT CAST(payload->>'t' AS text) FROM evidence", nil},
		{"to_timestamp", "SELECT to_timestamp(payload->>'t', 'YYYY-MM-DD') FROM evidence", []string{"payload->>'t'"}},
		{"to_date", "SELECT to_date(payload->>'d', 'YYYY-MM-DD') FROM evidence", []string{"payload->>'d'"}},
		{"chained cast", "SELECT (payload->>'t')::text::timestamptz FROM evidence", []string{"payload->>'t'"}},
		{"double parens", "SELECT ((payload->>'t'))::date FROM evidence", []string{"payload->>'t'"}},
		{"non-time cast", "SELECT (payload->>'n')::int FROM evidence", nil},
		{"literal-only cast", "SELECT payload->>'t'::text FROM evidence", nil},
		// Aliased, #>> and nested paths.
		{"aliased", "SELECT (e.payload->>'t')::timestamptz FROM evidence AS e", []string{"e.payload->>'t'"}},
		{"aliased, guarded on the alias", "SELECT CASE WHEN " + guardOn("e.payload->>'t'") + " THEN (e.payload->>'t')::timestamptz END FROM evidence AS e", nil},
		{"aliased, guarded without the alias", "SELECT CASE WHEN " + guard("t") + " THEN (e.payload->>'t')::timestamptz END FROM evidence AS e", []string{"e.payload->>'t'"}},
		{"#>> path", "SELECT (payload#>>'{a,b}')::timestamptz FROM evidence", []string{"payload#>>'{a,b}'"}},
		{"#>> path, guarded", "SELECT CASE WHEN " + guardOn("payload #>> '{a,b}'") + " THEN (payload#>>'{a,b}')::timestamptz END FROM evidence", nil},
		{"nested ->", "SELECT (payload->'a'->>'b')::timestamptz FROM evidence", []string{"payload->'a'->>'b'"}},
		{"nested, guarded on the leaf only", "SELECT CASE WHEN " + guard("b") + " THEN (payload->'a'->>'b')::timestamptz END FROM evidence", []string{"payload->'a'->>'b'"}},
		{"array index", "SELECT (payload->'xs'->0->>'t')::date FROM evidence", []string{"payload->'xs'->0->>'t'"}},
		// Structure: a guard elsewhere does not cover a cast.
		{"guard in another CASE", "SELECT CASE WHEN " + guard("t") + " THEN 1 END, (payload->>'t')::timestamptz FROM evidence", []string{"payload->>'t'"}},
		{"cast in the ELSE branch", "SELECT CASE WHEN " + guard("t") + " THEN 1 ELSE (payload->>'t')::timestamptz END FROM evidence", []string{"payload->>'t'"}},
		{"cast in a later WHEN", "SELECT CASE WHEN " + guard("t") + " THEN 1 WHEN true THEN (payload->>'t')::timestamptz END FROM evidence", []string{"payload->>'t'"}},
		{"cast beside the guard in the condition", "SELECT CASE WHEN " + guard("t") + " AND (payload->>'t')::timestamptz > evaluated_at THEN 1 END FROM evidence", []string{"payload->>'t'"}},
		{"guard in WHERE", "SELECT max((payload->>'t')::timestamptz) FROM evidence WHERE " + guard("t"), []string{"payload->>'t'"}},
		{"guard OR-ed", "SELECT CASE WHEN payload->>'t' ~ '" + RFC3339Pattern + "' OR pg_input_is_valid(payload->>'t', 'timestamptz') THEN (payload->>'t')::timestamptz END FROM evidence", []string{"payload->>'t'"}},
		{"guard negated", "SELECT CASE WHEN NOT (" + guard("t") + ") THEN (payload->>'t')::timestamptz END FROM evidence", []string{"payload->>'t'"}},
		{"guard inside a nested CASE in the condition", "SELECT CASE WHEN CASE WHEN " + guard("t") + " THEN true END THEN (payload->>'t')::timestamptz END FROM evidence", []string{"payload->>'t'"}},
		{"nested CASE under the guard", "SELECT CASE WHEN " + guard("t") + " THEN CASE WHEN (payload->>'t')::timestamptz <= evaluated_at THEN (payload->>'t')::timestamptz END END FROM evidence", nil},
		{"guard text inside a string literal", "SELECT CASE WHEN 'x' = '" + strings.ReplaceAll(guard("t"), "'", "''") + "' THEN (payload->>'t')::timestamptz END FROM evidence", []string{"payload->>'t'"}},
		{"guard in a comment", "SELECT CASE WHEN true /* " + guard("t") + " */ THEN (payload->>'t')::timestamptz END FROM evidence", []string{"payload->>'t'"}},
	} {
		got, err := SQLUnguardedTimestampCasts(tc.expr)
		if err != nil {
			t.Errorf("%s: SQLUnguardedTimestampCasts error: %v", tc.name, err)
			continue
		}
		if strings.Join(got, ",") != strings.Join(tc.want, ",") {
			t.Errorf("%s: SQLUnguardedTimestampCasts = %v, want %v", tc.name, got, tc.want)
		}
	}
	if _, err := SQLUnguardedTimestampCasts("SELECT 'unterminated FROM evidence"); err == nil {
		t.Error("unterminated literal: want an error, got nil")
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
