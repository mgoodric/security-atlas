// Package bundletest is the control-bundle test runner (slice 496): it
// executes a control bundle's declared test cases — fixture evidence records +
// an expected pass/fail — through the SAME evaluation engine the live path uses
// (internal/eval), and asserts the produced control state matches the
// expectation. This gives control authors the control-as-code analogue of
// "tests before code": a fast `atlas-cli controls test <bundle-dir>` loop that
// proves a bundle's evidence query does what the author thinks BEFORE the
// bundle is uploaded and silently mis-states compliance.
//
// The runner is a READ-ONLY, in-memory verification tool. It never touches the
// live evidence ledger or any tenant data: each test case's fixtures are
// presented to the engine as an in-memory record set (constitutional invariant
// #2 / anti-criterion P0-496-2). It reuses eval.EvaluateFixture — the exact
// dispatch the live engine uses — so a bundle that passes its tests behaves
// identically when evaluated for real (anti-criterion P0-496-1).
//
// fixture.go defines the on-disk test-fixture format and its loader. The
// runner itself lives in runner.go.
//
// # Fixture format (JUDGMENT call — decisions log 496 D-FMT-1)
//
// Test cases live in a `tests/` directory beside the bundle's control.yaml.
// Each `*.yaml` file in that directory holds one or more test cases:
//
//	# tests/mfa.yaml
//	cases:
//	  - name: all-users-have-mfa
//	    expected_state: pass
//	    records:
//	      - result: pass
//	        observed_at: 2026-06-01T00:00:00Z
//	        payload: { iam_users: [{ mfa_enabled: true }] }
//	  - name: one-user-without-mfa
//	    expected_state: fail
//	    records:
//	      - result: fail
//	        observed_at: 2026-06-01T00:00:00Z
//	        payload: { iam_users: [{ mfa_enabled: false }] }
//
// This pattern-matches the existing internal/control/testdata/ layout (a
// directory of YAML beside the manifest) and the slice-009 promise of "a tests
// directory with fixture evidence + expected pass/fail".
//
// `expected_state` is one of pass | fail | na | inconclusive. AC-7's semantics
// read naturally: a test case whose query SHOULD return `fail` declares
// `expected_state: fail` and is a PASSING test when the query returns fail —
// expected-fail is a pass for the runner.
//
// `observed_at` is optional; when omitted it defaults to the case's evaluation
// instant (now), so a record is in-window by default and an author writing a
// simple pass/fail case need not reason about freshness windows. An author
// testing freshness/staleness sets observed_at explicitly.
package bundletest

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// TestsDirName is the conventional sub-directory, beside control.yaml, that
// holds a bundle's test-case files. Slice 009's promised "tests directory".
const TestsDirName = "tests"

// maxFixtureRecordsPerCase bounds the number of fixture records a single test
// case may declare (threat-model D — a pathological fixture set must not hang
// the runner). Generous relative to any realistic control test; a case needing
// more is almost certainly testing the wrong thing.
const maxFixtureRecordsPerCase = 1000

// maxTestFileBytes caps a single tests/*.yaml file so a malicious or
// accidental huge file cannot OOM the runner.
const maxTestFileBytes = 4 * 1024 * 1024 // 4 MB

// allowedExpectedStates is the closed set of values expected_state may take.
// Mirrors the evidence_result enum the engine produces.
var allowedExpectedStates = map[string]struct{}{
	"pass":         {},
	"fail":         {},
	"na":           {},
	"inconclusive": {},
}

// TestFile is the parsed shape of one tests/*.yaml file: a list of test cases.
type TestFile struct {
	Cases []TestCase `yaml:"cases" json:"cases"`
}

// TestCase is one author-declared test: a named set of fixture evidence
// records and the control state the author expects the engine to produce.
type TestCase struct {
	// Name identifies the case in the runner's report. Required, must be
	// unique within a bundle.
	Name string `yaml:"name" json:"name"`
	// Description is optional human context for the case.
	Description string `yaml:"description,omitempty" json:"description,omitempty"`
	// ExpectedState is the control state the author asserts: pass | fail | na |
	// inconclusive.
	ExpectedState string `yaml:"expected_state" json:"expected_state"`
	// Records are the fixture evidence records fed to the control's evidence
	// query as the in-window record set.
	Records []FixtureRecord `yaml:"records,omitempty" json:"records,omitempty"`
	// EvaluatedAt optionally pins the evaluation instant the freshness window
	// is measured against. When zero, the runner uses its own deterministic
	// `now`. An author testing staleness sets this so observed_at offsets are
	// reproducible.
	EvaluatedAt *time.Time `yaml:"evaluated_at,omitempty" json:"evaluated_at,omitempty"`

	// sourceFile records which tests/*.yaml the case came from, for error
	// reporting. Populated by the loader; not part of the YAML surface.
	sourceFile string
}

// FixtureRecord is one fixture evidence record in a test case. It mirrors the
// fields the evaluation engine reads from a live evidence_records row — and
// nothing else (no tenant id, no hash): a fixture is author test data, never a
// ledger row.
type FixtureRecord struct {
	// Result is the per-record evidence result (pass|fail|na|inconclusive).
	Result string `yaml:"result" json:"result"`
	// ObservedAt is the record's observation time; drives the freshness-window
	// filter. Optional — defaults to the case's evaluation instant so a record
	// is in-window by default.
	ObservedAt *time.Time `yaml:"observed_at,omitempty" json:"observed_at,omitempty"`
	// Payload is the record's evidence payload — an arbitrary YAML/JSON object
	// the SQL + JSON-path query evaluators read. Decoded from YAML and
	// re-marshalled to JSON bytes for the engine.
	Payload map[string]any `yaml:"payload,omitempty" json:"payload,omitempty"`
}

// LoadTestCases reads every tests/*.yaml file beside a bundle's control.yaml
// and returns the ordered, validated list of test cases. bundleDir is the
// directory that contains control.yaml (the same path passed to
// control.ParseDirectory).
//
// Returns an empty slice (not an error) when the tests/ directory is absent or
// empty — a bundle with no tests is valid; the runner simply reports "no test
// cases". Returns an error when a tests/*.yaml file is malformed or a case
// fails validation.
//
// Files are read in lexical order and cases preserve their in-file order, so
// the runner's report is deterministic.
func LoadTestCases(bundleDir string) ([]TestCase, error) {
	testsDir := filepath.Join(bundleDir, TestsDirName)
	info, err := os.Stat(testsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // no tests directory → no cases (not an error)
		}
		return nil, fmt.Errorf("bundletest: stat %s: %w", testsDir, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("bundletest: %s exists but is not a directory", testsDir)
	}

	entries, err := os.ReadDir(testsDir)
	if err != nil {
		return nil, fmt.Errorf("bundletest: read %s: %w", testsDir, err)
	}

	files := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		low := strings.ToLower(name)
		if strings.HasSuffix(low, ".yaml") || strings.HasSuffix(low, ".yml") {
			files = append(files, name)
		}
	}
	sort.Strings(files) // deterministic order

	var cases []TestCase
	seen := make(map[string]string) // case name → file it first appeared in
	for _, fname := range files {
		fpath := filepath.Join(testsDir, fname)
		tf, err := parseTestFile(fpath)
		if err != nil {
			return nil, err
		}
		for i := range tf.Cases {
			c := tf.Cases[i]
			c.sourceFile = fname
			if err := c.validate(); err != nil {
				return nil, fmt.Errorf("bundletest: %s: %w", fname, err)
			}
			if prev, dup := seen[c.Name]; dup {
				return nil, fmt.Errorf("bundletest: duplicate test case name %q (in %s and %s)", c.Name, prev, fname)
			}
			seen[c.Name] = fname
			cases = append(cases, c)
		}
	}
	return cases, nil
}

// parseTestFile reads and YAML-decodes one tests/*.yaml file. KnownFields is
// enabled so a typo'd key (e.g. `expectd_state`) is a loud parse error rather
// than a silently ignored field that yields a wrong test result.
func parseTestFile(path string) (TestFile, error) {
	f, err := os.Open(path) //nolint:gosec // path is constructed from a caller-supplied bundle dir + a dir-listing entry; this is a local authoring CLI tool, not a network surface.
	if err != nil {
		return TestFile{}, fmt.Errorf("bundletest: open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	st, err := f.Stat()
	if err != nil {
		return TestFile{}, fmt.Errorf("bundletest: stat %s: %w", path, err)
	}
	if st.Size() > maxTestFileBytes {
		return TestFile{}, fmt.Errorf("bundletest: %s exceeds %d bytes", path, maxTestFileBytes)
	}

	buf, err := io.ReadAll(io.LimitReader(f, maxTestFileBytes+1))
	if err != nil {
		return TestFile{}, fmt.Errorf("bundletest: read %s: %w", path, err)
	}
	return parseTestFileBytes(path, buf)
}

// parseTestFileBytes YAML-decodes one tests/*.yaml file already read into
// memory — the seam the slice-574 upload gate uses, where the bundle's test
// files arrive as bytes (control.Bundle.TestFiles) rather than a directory.
// parseTestFile delegates here after a size-capped read, so the directory path
// and the in-memory path share one decode + one set of decoder options
// (KnownFields(true), so a typo'd key is a loud error in BOTH paths). name is
// used only for error messages.
func parseTestFileBytes(name string, data []byte) (TestFile, error) {
	if int64(len(data)) > maxTestFileBytes {
		return TestFile{}, fmt.Errorf("bundletest: %s exceeds %d bytes", name, maxTestFileBytes)
	}
	var tf TestFile
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&tf); err != nil {
		return TestFile{}, fmt.Errorf("bundletest: parse %s: %w", name, err)
	}
	return tf, nil
}

// validate runs the structural checks on one test case (AC-1: the format is
// defined AND validated). It does NOT validate the records against the
// control's evidence_kind schema — that is deferred (a record's payload shape
// is the author's concern, and the engine's query is what asserts over it).
func (c *TestCase) validate() error {
	if strings.TrimSpace(c.Name) == "" {
		return fmt.Errorf("test case is missing a name")
	}
	if c.ExpectedState == "" {
		return fmt.Errorf("test case %q: expected_state is required", c.Name)
	}
	if _, ok := allowedExpectedStates[c.ExpectedState]; !ok {
		return fmt.Errorf("test case %q: expected_state %q is not one of pass|fail|na|inconclusive", c.Name, c.ExpectedState)
	}
	if len(c.Records) > maxFixtureRecordsPerCase {
		return fmt.Errorf("test case %q: %d records exceeds the per-case limit of %d", c.Name, len(c.Records), maxFixtureRecordsPerCase)
	}
	for i, r := range c.Records {
		if r.Result == "" {
			return fmt.Errorf("test case %q: records[%d].result is required", c.Name, i)
		}
		if _, ok := allowedExpectedStates[r.Result]; !ok {
			return fmt.Errorf("test case %q: records[%d].result %q is not one of pass|fail|na|inconclusive", c.Name, i, r.Result)
		}
	}
	return nil
}

// SourceFile reports which tests/*.yaml file the case was loaded from. Exposed
// for the runner's report.
func (c *TestCase) SourceFile() string { return c.sourceFile }
