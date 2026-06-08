// Unit tests for the control-bundle test runner (slice 496).
//
// These tests are the slice's own proof that the runner catches a wrong query
// (AC-6) and that expected-fail reads correctly (AC-7), and that the runner
// uses NO live DB (AC-9 — every test here runs with opts.Tx == nil and no
// Postgres). The Rego + JSON-path languages evaluate fully in-process, so the
// whole runner is exercised without a database.
package bundletest

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const fixedNow = "2026-06-01T12:00:00Z"

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("parse time %q: %v", s, err)
	}
	return ts
}

func runBundle(t *testing.T, dir string) *Report {
	t.Helper()
	// AC-9: opts.Tx is nil — no Postgres, no live ledger. The Rego + JSON-path
	// paths evaluate purely in-process.
	rep, err := Run(context.Background(), dir, Options{Now: mustTime(t, fixedNow)})
	if err != nil {
		t.Fatalf("Run(%s): unexpected error: %v", dir, err)
	}
	return rep
}

// AC-6 + AC-7: the Rego bundle's three cases — all-pass (pass), one-fail
// (expected fail), stale (inconclusive) — must ALL be passing TESTS when the
// query is correct.
func TestRun_RegoBundle_AllCasesPass(t *testing.T) {
	t.Parallel()
	rep := runBundle(t, "testdata/rego-bundle")
	if !rep.AllPassed() {
		t.Fatalf("expected all cases to pass; report: %+v", rep)
	}
	if rep.Passed != 4 || rep.Failed != 0 || rep.Errored != 0 {
		t.Fatalf("want 4 passed / 0 failed / 0 errored; got %d/%d/%d", rep.Passed, rep.Failed, rep.Errored)
	}
	byName := indexByName(rep)
	if got := byName["one-record-fails"].ActualState; got != "fail" {
		t.Fatalf("expected-fail case actual state = %q, want fail", got)
	}
	if !byName["one-record-fails"].Passed {
		t.Fatal("AC-7: an expected-fail case where the query returns fail must be a PASSING test")
	}
	if got := byName["no-evidence-inconclusive"].ActualState; got != "inconclusive" {
		t.Fatalf("no-evidence case actual state = %q, want inconclusive", got)
	}
	if got := byName["no-evidence-inconclusive"].FreshnessStatus; got != "no_evidence" {
		t.Fatalf("no-evidence case freshness = %q, want no_evidence", got)
	}
	if got := byName["stale-evidence-query-default"].FreshnessStatus; got != "stale" {
		t.Fatalf("stale case freshness = %q, want stale", got)
	}
}

// AC-4: the JSON-path bundle drives the slice-495 JSON-path evaluator end to
// end over fixture payloads.
func TestRun_JSONPathBundle_DrivesPayloadQuery(t *testing.T) {
	t.Parallel()
	rep := runBundle(t, "testdata/jsonpath-bundle")
	if !rep.AllPassed() {
		t.Fatalf("expected all jsonpath cases to pass; report: %+v", rep)
	}
	byName := indexByName(rep)
	if got := byName["all-encrypted-pass"].ActualState; got != "pass" {
		t.Fatalf("all-encrypted actual = %q, want pass", got)
	}
	if got := byName["one-unencrypted-fail"].ActualState; got != "fail" {
		t.Fatalf("one-unencrypted actual = %q, want fail", got)
	}
}

// A bundle with no declared query falls back to the per-record evidence rollup
// — identical to the live engine's no-query behaviour.
func TestRun_NoQueryBundle_PerRecordRollup(t *testing.T) {
	t.Parallel()
	rep := runBundle(t, "testdata/noquery-bundle")
	if !rep.AllPassed() {
		t.Fatalf("expected all noquery cases to pass; report: %+v", rep)
	}
}

// AC-6: the runner CATCHES a wrong query. We point the runner at the Rego
// bundle but flip one case's expectation; the runner must report that case as a
// FAILURE (actual != expected), proving it does not rubber-stamp.
func TestRun_CatchesWrongExpectation(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	copyBundle(t, "testdata/rego-bundle/control.yaml", filepath.Join(dir, "control.yaml"))
	// One case that asserts the WRONG state: an all-pass fixture but the author
	// (wrongly) expects fail. The runner must flag the mismatch.
	writeFile(t, filepath.Join(dir, "tests", "wrong.yaml"), `cases:
  - name: wrong-expectation
    expected_state: fail
    evaluated_at: 2026-06-01T12:00:00Z
    records:
      - result: pass
        observed_at: 2026-06-01T00:00:00Z
`)
	rep := runBundle(t, dir)
	if rep.AllPassed() {
		t.Fatal("AC-6: a case whose expectation is wrong must NOT pass")
	}
	if rep.Failed != 1 {
		t.Fatalf("want 1 failed case, got %d (report %+v)", rep.Failed, rep)
	}
	byName := indexByName(rep)
	c := byName["wrong-expectation"]
	if c.Passed {
		t.Fatal("wrong-expectation case must be reported as failing")
	}
	if c.ActualState != "pass" || c.ExpectedState != "fail" {
		t.Fatalf("want actual=pass expected=fail, got actual=%q expected=%q", c.ActualState, c.ExpectedState)
	}
}

// A query that cannot compile (a Rego syntax error) must be reported as a test
// ERROR, not a pass and not a hang. This is the fail-loud posture: a broken
// query never silently yields a state.
func TestRun_BrokenQueryReportedAsError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "control.yaml"), `bundle_schema_version: "1"
bundle_id: broken_query_test
title: "Broken Rego query"
scf_anchor_id: IAC-06
implementation_type: automated
freshness_class: daily
evidence_queries:
  - id: broken
    language: rego
    expression: |
      package evidence.query
      result := "pass" if { this is not valid rego @@@ }
`)
	writeFile(t, filepath.Join(dir, "tests", "c.yaml"), `cases:
  - name: any
    expected_state: pass
    evaluated_at: 2026-06-01T12:00:00Z
    records:
      - result: pass
        observed_at: 2026-06-01T00:00:00Z
`)
	rep := runBundle(t, dir)
	if rep.Errored != 1 {
		t.Fatalf("want 1 errored case, got %d (report %+v)", rep.Errored, rep)
	}
	if rep.AllPassed() {
		t.Fatal("a bundle with a broken query must not pass")
	}
	if c := indexByName(rep)["any"]; c.Err == "" {
		t.Fatal("broken-query case must carry an error")
	}
}

// AC-8 / threat-model D: a cancelled context surfaces as a test error, not a
// hang. We cancel before Run and assert the JSON-path case errors out promptly.
func TestRun_CancelledContextErrors(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled
	rep, err := Run(ctx, "testdata/jsonpath-bundle", Options{Now: mustTime(t, fixedNow)})
	if err != nil {
		t.Fatalf("Run returned a top-level error; cases should carry the error instead: %v", err)
	}
	if rep.Errored == 0 {
		t.Fatalf("want at least one errored case under a cancelled context; report %+v", rep)
	}
}

// A SQL query with no Tx must be reported as an error (ErrFixtureSQLNeedsDB),
// never a false pass. This proves AC-9's contract holds even when a bundle
// declares SQL: the runner degrades to an explicit, actionable error.
func TestRun_SQLWithoutDB_ReportedAsError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "control.yaml"), `bundle_schema_version: "1"
bundle_id: sql_needs_db_test
title: "SQL query without a DB"
scf_anchor_id: IAC-06
implementation_type: automated
freshness_class: daily
evidence_queries:
  - id: sql_all_pass
    language: sql
    expression: "SELECT bool_and(result = 'pass') FROM evidence"
`)
	writeFile(t, filepath.Join(dir, "tests", "c.yaml"), `cases:
  - name: sql-case
    expected_state: pass
    evaluated_at: 2026-06-01T12:00:00Z
    records:
      - result: pass
        observed_at: 2026-06-01T00:00:00Z
`)
	rep := runBundle(t, dir)
	if rep.Errored != 1 {
		t.Fatalf("want 1 errored case (SQL needs DB), got %d (report %+v)", rep.Errored, rep)
	}
	c := indexByName(rep)["sql-case"]
	if !strings.Contains(c.Err, "requires a database") {
		t.Fatalf("SQL-no-DB error = %q, want a 'requires a database' message", c.Err)
	}
}

// A bundle with no tests/ directory is not an error — the report is empty and
// AllPassed is true (nothing failed).
func TestRun_NoTestsDirectory(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	copyBundle(t, "testdata/rego-bundle/control.yaml", filepath.Join(dir, "control.yaml"))
	rep := runBundle(t, dir)
	if len(rep.Cases) != 0 {
		t.Fatalf("want 0 cases for a bundle with no tests dir, got %d", len(rep.Cases))
	}
	if !rep.AllPassed() {
		t.Fatal("a bundle with no tests must report AllPassed (nothing failed)")
	}
}

// AC-1: the fixture format is VALIDATED — an invalid expected_state is a loud
// load error, not a silently-ignored field.
func TestLoadTestCases_RejectsInvalidExpectedState(t *testing.T) {
	t.Parallel()
	_, err := LoadTestCases("testdata/badtests-bundle")
	if err == nil {
		t.Fatal("LoadTestCases must reject an invalid expected_state")
	}
	if !strings.Contains(err.Error(), "expected_state") {
		t.Fatalf("error should point at expected_state, got %q", err)
	}
}

// AC-1: a duplicate case name across files is rejected (deterministic, unique
// report rows).
func TestLoadTestCases_RejectsDuplicateName(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mkdir(t, filepath.Join(dir, "tests"))
	writeFile(t, filepath.Join(dir, "tests", "a.yaml"), "cases:\n  - name: dup\n    expected_state: pass\n")
	writeFile(t, filepath.Join(dir, "tests", "b.yaml"), "cases:\n  - name: dup\n    expected_state: fail\n")
	_, err := LoadTestCases(dir)
	if err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("want a duplicate-name error, got %v", err)
	}
}

// An unknown YAML key is rejected (KnownFields) so a typo'd field never
// silently changes a test's meaning.
func TestLoadTestCases_RejectsUnknownField(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mkdir(t, filepath.Join(dir, "tests"))
	writeFile(t, filepath.Join(dir, "tests", "a.yaml"), "cases:\n  - name: x\n    expectd_state: pass\n")
	_, err := LoadTestCases(dir)
	if err == nil {
		t.Fatal("a typo'd key must be a loud parse error")
	}
}

// AC-1: per-record validation — a record missing result, or with an invalid
// result, is a loud load error; and SourceFile is populated.
func TestLoadTestCases_RecordValidationAndSourceFile(t *testing.T) {
	t.Parallel()

	// Missing record result.
	d1 := t.TempDir()
	mkdir(t, filepath.Join(d1, "tests"))
	writeFile(t, filepath.Join(d1, "tests", "a.yaml"), "cases:\n  - name: x\n    expected_state: pass\n    records:\n      - observed_at: 2026-06-01T00:00:00Z\n")
	if _, err := LoadTestCases(d1); err == nil || !strings.Contains(err.Error(), "result is required") {
		t.Fatalf("want missing-result error, got %v", err)
	}

	// Invalid record result value.
	d2 := t.TempDir()
	mkdir(t, filepath.Join(d2, "tests"))
	writeFile(t, filepath.Join(d2, "tests", "a.yaml"), "cases:\n  - name: x\n    expected_state: pass\n    records:\n      - result: maybe\n")
	if _, err := LoadTestCases(d2); err == nil || !strings.Contains(err.Error(), "not one of") {
		t.Fatalf("want invalid-record-result error, got %v", err)
	}

	// Missing name.
	d3 := t.TempDir()
	mkdir(t, filepath.Join(d3, "tests"))
	writeFile(t, filepath.Join(d3, "tests", "a.yaml"), "cases:\n  - expected_state: pass\n")
	if _, err := LoadTestCases(d3); err == nil || !strings.Contains(err.Error(), "name") {
		t.Fatalf("want missing-name error, got %v", err)
	}

	// Valid case: SourceFile is populated from the filename.
	cases, err := LoadTestCases("testdata/rego-bundle")
	if err != nil {
		t.Fatalf("LoadTestCases(rego-bundle): %v", err)
	}
	if len(cases) == 0 {
		t.Fatal("expected cases from rego-bundle")
	}
	if cases[0].SourceFile() != "cases.yaml" {
		t.Fatalf("SourceFile() = %q, want cases.yaml", cases[0].SourceFile())
	}
}

// A tests/ path that is a FILE (not a dir) is a loud error.
func TestLoadTestCases_TestsPathIsFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "tests"), "not a directory")
	if _, err := LoadTestCases(dir); err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("want not-a-directory error, got %v", err)
	}
}

// Run surfaces a malformed bundle (missing control.yaml) as a top-level error.
func TestRun_MalformedBundleErrors(t *testing.T) {
	t.Parallel()
	dir := t.TempDir() // no control.yaml
	_, err := Run(context.Background(), dir, Options{Now: mustTime(t, fixedNow)})
	if err == nil {
		t.Fatal("Run on a dir with no control.yaml must error")
	}
}

// ----- helpers -----

func indexByName(rep *Report) map[string]CaseResult {
	m := make(map[string]CaseResult, len(rep.Cases))
	for _, c := range rep.Cases {
		m[c.Name] = c
	}
	return m
}

func mkdir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	mkdir(t, filepath.Dir(path))
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func copyBundle(t *testing.T, src, dst string) {
	t.Helper()
	b, err := os.ReadFile(src) //nolint:gosec // test-only read of a fixed testdata path.
	if err != nil {
		t.Fatalf("read %s: %v", src, err)
	}
	writeFile(t, dst, string(b))
}
