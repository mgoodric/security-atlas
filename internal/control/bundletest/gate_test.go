package bundletest

import (
	"context"
	"testing"

	"github.com/mgoodric/security-atlas/internal/eval"
)

// jsonpathQueries is the query set the in-memory tests evaluate against — a
// single JSON-path query that resolves $.encrypted on each record's payload.
// JSON-path evaluates fully in-process (no DB), so these tests need no tx.
func jsonpathQueries() []eval.FixtureQuery {
	return QueriesFromManifest([]ManifestQuery{
		{Language: "jsonpath", Expression: "$.encrypted"},
	})
}

const passingTestFile = `cases:
  - name: all-encrypted
    expected_state: pass
    evaluated_at: 2026-06-01T12:00:00Z
    records:
      - result: pass
        observed_at: 2026-06-01T00:00:00Z
        payload: { bucket: prod, encrypted: true }
`

const failingTestFile = `cases:
  - name: one-unencrypted
    expected_state: pass
    evaluated_at: 2026-06-01T12:00:00Z
    records:
      - result: pass
        observed_at: 2026-06-01T00:00:00Z
        payload: { bucket: dev, encrypted: false }
`

func TestRunFromFiles_AllPass(t *testing.T) {
	t.Parallel()
	files := map[string][]byte{"a.yaml": []byte(passingTestFile)}
	rep, err := RunFromFiles(context.Background(), "b", "daily", jsonpathQueries(), files, Options{})
	if err != nil {
		t.Fatalf("RunFromFiles: %v", err)
	}
	if !rep.AllPassed() {
		t.Fatalf("expected all-pass, got %+v", rep)
	}
	if rep.Passed != 1 || rep.Failed != 0 || rep.Errored != 0 {
		t.Fatalf("unexpected rollup: %+v", rep)
	}
}

func TestRunFromFiles_FailingCaseBlocks(t *testing.T) {
	t.Parallel()
	// The case expects pass but the query resolves the record to fail
	// (encrypted=false) → actual != expected → a failing test.
	files := map[string][]byte{"a.yaml": []byte(failingTestFile)}
	rep, err := RunFromFiles(context.Background(), "b", "daily", jsonpathQueries(), files, Options{})
	if err != nil {
		t.Fatalf("RunFromFiles: %v", err)
	}
	if rep.AllPassed() {
		t.Fatalf("expected a failing case, got all-pass: %+v", rep)
	}
	if rep.Failed != 1 {
		t.Fatalf("expected 1 failed, got %+v", rep)
	}
	if rep.Cases[0].ExpectedState != "pass" || rep.Cases[0].ActualState != "fail" {
		t.Fatalf("unexpected case detail: %+v", rep.Cases[0])
	}
}

func TestRunFromFiles_NoFilesYieldsEmptyReport(t *testing.T) {
	t.Parallel()
	rep, err := RunFromFiles(context.Background(), "b", "daily", jsonpathQueries(), nil, Options{})
	if err != nil {
		t.Fatalf("RunFromFiles: %v", err)
	}
	if len(rep.Cases) != 0 {
		t.Fatalf("expected zero cases, got %d", len(rep.Cases))
	}
	if !rep.AllPassed() {
		t.Fatalf("an empty report is vacuously all-passed")
	}
}

func TestRunFromFiles_SQLQueryWithNoTxErrors(t *testing.T) {
	t.Parallel()
	// A SQL query with no tx must surface as a per-case ERROR (never a false
	// pass) — the gate treats ERROR as a block.
	q := QueriesFromManifest([]ManifestQuery{{Language: "sql", Expression: "SELECT 'pass'::text AS result"}})
	files := map[string][]byte{"a.yaml": []byte(passingTestFile)}
	rep, err := RunFromFiles(context.Background(), "b", "daily", q, files, Options{})
	if err != nil {
		t.Fatalf("RunFromFiles: %v", err)
	}
	if rep.Errored != 1 {
		t.Fatalf("expected 1 errored case for SQL-with-no-tx, got %+v", rep)
	}
	if rep.AllPassed() {
		t.Fatalf("an errored case is not all-passed")
	}
}

func TestLoadTestCasesFromBytes_DeterministicOrder(t *testing.T) {
	t.Parallel()
	files := map[string][]byte{
		"b.yaml": []byte("cases:\n  - name: second\n    expected_state: pass\n"),
		"a.yaml": []byte("cases:\n  - name: first\n    expected_state: pass\n"),
	}
	cases, err := loadTestCasesFromBytes(files)
	if err != nil {
		t.Fatalf("loadTestCasesFromBytes: %v", err)
	}
	if len(cases) != 2 || cases[0].Name != "first" || cases[1].Name != "second" {
		t.Fatalf("expected lexical file order (a before b), got %+v", cases)
	}
}

func TestLoadTestCasesFromBytes_DuplicateNameAcrossFiles(t *testing.T) {
	t.Parallel()
	files := map[string][]byte{
		"a.yaml": []byte("cases:\n  - name: dup\n    expected_state: pass\n"),
		"b.yaml": []byte("cases:\n  - name: dup\n    expected_state: pass\n"),
	}
	if _, err := loadTestCasesFromBytes(files); err == nil {
		t.Fatalf("expected a duplicate-name error")
	}
}

func TestLoadTestCasesFromBytes_UnknownFieldIsLoud(t *testing.T) {
	t.Parallel()
	files := map[string][]byte{"a.yaml": []byte("cases:\n  - name: typo\n    expectd_state: pass\n")}
	if _, err := loadTestCasesFromBytes(files); err == nil {
		t.Fatalf("expected a KnownFields parse error for the typo'd key")
	}
}

func TestLoadTestCasesFromBytes_InvalidExpectedStateRejected(t *testing.T) {
	t.Parallel()
	files := map[string][]byte{"a.yaml": []byte("cases:\n  - name: bad\n    expected_state: maybe\n")}
	if _, err := loadTestCasesFromBytes(files); err == nil {
		t.Fatalf("expected a validation error for an out-of-enum expected_state")
	}
}
