// querylang_test.go — pure-Go unit tests for the slice-495 SQL + JSON-path
// evidence-query evaluation: the JSON-path result classification, the SQL
// static guard, the SQL value classification, and the in-window-records JSON
// marshalling. No Postgres — the SQL exec path itself is integration-tested
// (integration_test.go AC-8/10/11); these cover the pure branches that gate it
// (the fast loop, slice 353 Q-2 convention).

package eval

import (
	"context"
	"strings"
	"testing"
)

// ===== AC-12: JSON-path per-record classification + rollup =====

func TestEvalJSONPathQuery_TruthyScalarPasses(t *testing.T) {
	t.Parallel()
	recs := []inWindowRecord{{result: ResultPass, payload: []byte(`{"encrypted":true}`)}}
	got, err := evalJSONPathQuery(context.Background(), "$.encrypted", recs)
	if err != nil {
		t.Fatalf("evalJSONPathQuery: %v", err)
	}
	if got != ResultPass {
		t.Fatalf("truthy scalar = %q, want pass", got)
	}
}

func TestEvalJSONPathQuery_FalsyScalarFails(t *testing.T) {
	t.Parallel()
	recs := []inWindowRecord{{result: ResultPass, payload: []byte(`{"encrypted":false}`)}}
	got, err := evalJSONPathQuery(context.Background(), "$.encrypted", recs)
	if err != nil {
		t.Fatalf("evalJSONPathQuery: %v", err)
	}
	if got != ResultFail {
		t.Fatalf("falsy scalar = %q, want fail", got)
	}
}

func TestEvalJSONPathQuery_MissingPathIsFail(t *testing.T) {
	t.Parallel()
	// A path that resolves to nothing is a NON-MATCH (fail), never a silent
	// pass and never a query error — the missing key is the asserted-absent
	// condition.
	recs := []inWindowRecord{{result: ResultPass, payload: []byte(`{"other":1}`)}}
	got, err := evalJSONPathQuery(context.Background(), "$.encrypted", recs)
	if err != nil {
		t.Fatalf("evalJSONPathQuery: %v", err)
	}
	if got != ResultFail {
		t.Fatalf("missing path = %q, want fail", got)
	}
}

func TestEvalJSONPathQuery_FilterMatchPasses(t *testing.T) {
	t.Parallel()
	recs := []inWindowRecord{{result: ResultPass, payload: []byte(`{"checks":[{"passed":true},{"passed":true}]}`)}}
	got, err := evalJSONPathQuery(context.Background(), "$.checks[?(@.passed==true)]", recs)
	if err != nil {
		t.Fatalf("evalJSONPathQuery: %v", err)
	}
	if got != ResultPass {
		t.Fatalf("non-empty filter match = %q, want pass", got)
	}
}

func TestEvalJSONPathQuery_RollupAnyFailIsFail(t *testing.T) {
	t.Parallel()
	// One record matches (pass), one does not (fail). Rollup precedence: any
	// fail -> fail (AC-6 consistency).
	recs := []inWindowRecord{
		{result: ResultPass, payload: []byte(`{"encrypted":true}`)},
		{result: ResultPass, payload: []byte(`{"encrypted":false}`)},
	}
	got, err := evalJSONPathQuery(context.Background(), "$.encrypted", recs)
	if err != nil {
		t.Fatalf("evalJSONPathQuery: %v", err)
	}
	if got != ResultFail {
		t.Fatalf("mixed match rollup = %q, want fail", got)
	}
}

func TestEvalJSONPathQuery_EmptyExpressionFailsLoud(t *testing.T) {
	t.Parallel()
	_, err := evalJSONPathQuery(context.Background(), "", []inWindowRecord{{result: ResultPass}})
	if err == nil {
		t.Fatal("empty jsonpath expression must error (fail loud), got nil")
	}
}

func TestEvalJSONPathQuery_UnparseableExpressionFailsLoud(t *testing.T) {
	t.Parallel()
	recs := []inWindowRecord{{result: ResultPass, payload: []byte(`{"a":1}`)}}
	_, err := evalJSONPathQuery(context.Background(), "$.[[[bad", recs)
	if err == nil {
		t.Fatal("unparseable jsonpath must error (fail loud), got nil")
	}
}

func TestEvalJSONPathQuery_NoRecordsIsInconclusive(t *testing.T) {
	t.Parallel()
	got, err := evalJSONPathQuery(context.Background(), "$.encrypted", nil)
	if err != nil {
		t.Fatalf("evalJSONPathQuery: %v", err)
	}
	if got != ResultInconclusive {
		t.Fatalf("no records = %q, want inconclusive", got)
	}
}

func TestJSONPathMatchIsTruthy(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   interface{}
		want bool
	}{
		{"nil", nil, false},
		{"true", true, true},
		{"false", false, false},
		{"nonempty-string", "x", true},
		{"empty-string", "", false},
		{"nonzero-float", 1.5, true},
		{"zero-float", float64(0), false},
		{"nonzero-int", 3, true},
		{"zero-int", 0, false},
		{"nonempty-array", []interface{}{1}, true},
		{"empty-array", []interface{}{}, false},
		{"nonempty-object", map[string]interface{}{"a": 1}, true},
		{"empty-object", map[string]interface{}{}, false},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := jsonPathMatchIsTruthy(c.in); got != c.want {
				t.Fatalf("jsonPathMatchIsTruthy(%v) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}

// ===== AC-2 (static half): SQL guard rejects non-read-only shapes =====

func TestValidateSQLEvidenceQuery_AcceptsSelect(t *testing.T) {
	t.Parallel()
	for _, q := range []string{
		"SELECT 'pass' FROM evidence",
		"select result from evidence",
		"WITH x AS (SELECT result FROM evidence) SELECT result FROM x",
		"SELECT bool_and(result = 'pass') FROM evidence;",       // single trailing semicolon ok
		"SELECT evidence.result FROM evidence",                  // CTE-qualified column is fine
		"SELECT (payload->>'encrypted')::boolean FROM evidence", // ->> / :: are not schema-qualified
	} {
		if err := validateSQLEvidenceQuery(q); err != nil {
			t.Fatalf("validateSQLEvidenceQuery(%q) = %v, want nil", q, err)
		}
	}
}

func TestValidateSQLEvidenceQuery_RejectsWritesAndDDL(t *testing.T) {
	t.Parallel()
	bad := []string{
		"",
		"   ",
		"INSERT INTO evidence VALUES ('x')",
		"UPDATE evidence SET result='pass'",
		"DELETE FROM evidence",
		"DROP TABLE evidence_records",
		"SELECT 1; DROP TABLE controls",         // multi-statement
		"SELECT result FROM evidence; SELECT 1", // multi-statement
		"TRUNCATE evidence",
		"COPY evidence TO '/tmp/x'",
		"SELECT pg_sleep(10)",
		"SET statement_timeout = 0",
		"GRANT ALL ON evidence_records TO public",
		"SELECT lo_export(1, '/tmp/x')",
		"VALUES ('pass')",                              // not a SELECT/WITH prefix
		"SELECT result FROM public.evidence_records",   // schema-qualified reach
		"SELECT * FROM pg_catalog.pg_authid",           // schema-qualified reach
		"SELECT result FROM information_schema.tables", // schema-qualified reach
	}
	for _, q := range bad {
		if err := validateSQLEvidenceQuery(q); err == nil {
			t.Fatalf("validateSQLEvidenceQuery(%q) = nil, want rejection", q)
		}
	}
}

// ===== SQL value classification =====

func TestClassifySQLValue(t *testing.T) {
	t.Parallel()
	mustResult := func(in interface{}, want string) {
		got, err := classifySQLValue(in)
		if err != nil {
			t.Fatalf("classifySQLValue(%v) error: %v", in, err)
		}
		if got != want {
			t.Fatalf("classifySQLValue(%v) = %q, want %q", in, got, want)
		}
	}
	mustResult(true, ResultPass)
	mustResult(false, ResultFail)
	mustResult("pass", ResultPass)
	mustResult("fail", ResultFail)
	mustResult("na", ResultNA)
	mustResult("inconclusive", ResultInconclusive)

	for _, in := range []interface{}{nil, "maybe", 42, 3.14} {
		if _, err := classifySQLValue(in); err == nil {
			t.Fatalf("classifySQLValue(%v) = nil error, want rejection", in)
		}
	}
}

func TestClassifySQLError_Timeout(t *testing.T) {
	t.Parallel()
	err := classifySQLError(&fakeErr{"ERROR: canceling statement due to statement timeout (SQLSTATE 57014)"})
	if err == nil || !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("classifySQLError timeout = %v, want a timeout error", err)
	}
}

type fakeErr struct{ s string }

func (e *fakeErr) Error() string { return e.s }

// ===== inWindowRecordsToJSON: payload nests as an object, not a string =====

func TestInWindowRecordsToJSON_PayloadNests(t *testing.T) {
	t.Parallel()
	recs := []inWindowRecord{{result: ResultPass, payload: []byte(`{"encrypted":true}`)}}
	b, err := inWindowRecordsToJSON(recs)
	if err != nil {
		t.Fatalf("inWindowRecordsToJSON: %v", err)
	}
	s := string(b)
	if !strings.Contains(s, `"payload":{"encrypted":true}`) {
		t.Fatalf("payload did not nest as object: %s", s)
	}
	if strings.Contains(s, `"payload":"{`) {
		t.Fatalf("payload was double-encoded as a string: %s", s)
	}
}

func TestInWindowRecordsToJSON_NilPayloadBecomesNull(t *testing.T) {
	t.Parallel()
	recs := []inWindowRecord{{result: ResultFail}}
	b, err := inWindowRecordsToJSON(recs)
	if err != nil {
		t.Fatalf("inWindowRecordsToJSON: %v", err)
	}
	if !strings.Contains(string(b), `"payload":null`) {
		t.Fatalf("nil payload did not become null: %s", string(b))
	}
}

// ===== parseEvidenceQueries: full list, in order =====

func TestParseEvidenceQueries(t *testing.T) {
	t.Parallel()
	in := []byte(`[{"language":"rego","expression":"a"},{"language":"sql","expression":"b"},{"language":"jsonpath","expression":"c"}]`)
	qs, err := parseEvidenceQueries(in)
	if err != nil {
		t.Fatalf("parseEvidenceQueries: %v", err)
	}
	if len(qs) != 3 {
		t.Fatalf("expected 3 queries, got %d", len(qs))
	}
	if qs[0].Language != "rego" || qs[1].Language != "sql" || qs[2].Language != "jsonpath" {
		t.Fatalf("queries out of order: %+v", qs)
	}
}

func TestParseEvidenceQueries_Empty(t *testing.T) {
	t.Parallel()
	for _, in := range [][]byte{nil, []byte(""), []byte("[]")} {
		qs, err := parseEvidenceQueries(in)
		if err != nil {
			t.Fatalf("parseEvidenceQueries(%q): %v", in, err)
		}
		if len(qs) != 0 {
			t.Fatalf("parseEvidenceQueries(%q) = %d queries, want 0", in, len(qs))
		}
	}
}

func TestSupportedQueryLanguages(t *testing.T) {
	t.Parallel()
	for _, lang := range []string{"rego", "sql", "jsonpath"} {
		if !SupportedQueryLanguages[lang] {
			t.Fatalf("%q should be supported", lang)
		}
	}
	for _, lang := range []string{"sigma", "kyverno", "", "REGO"} {
		if SupportedQueryLanguages[lang] {
			t.Fatalf("%q must NOT be supported (fail-loud language)", lang)
		}
	}
}
