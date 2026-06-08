// gate.go — in-memory entry points the slice-574 upload gate uses.
//
// The slice-496 runner (runner.go) loads a bundle's test cases from a
// filesystem `tests/` directory (LoadTestCases) and evaluates them. That is the
// right shape for the local authoring CLI, which always has a directory on disk.
// The upload gate does NOT: the upload handler receives a bundle as a parsed
// *control.Bundle (its tests/*.yaml bytes captured in Bundle.TestFiles by
// ParseTarball), with no directory to read.
//
// RunFromFiles is the in-memory counterpart of Run: same loader semantics, same
// per-case evaluation through eval.EvaluateFixture, but the test-case source is
// a map of already-read file bytes rather than a directory. The two paths share
// every downstream helper (parseTestFileBytes, runCase, the Report rollup) so
// the gate's verdict is identical to what `atlas-cli controls test` would
// report on the same bundle (anti-criterion P0-496-1 holds transitively).
package bundletest

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/mgoodric/security-atlas/internal/eval"
)

// RunFromFiles evaluates a bundle's test cases from in-memory file bytes.
//
//   - bundleID, freshnessClass, and queries come from the parsed manifest (the
//     caller extracts them; QueriesFromManifest is the convenience helper).
//   - testFiles maps a tests/*.yaml basename to its verbatim bytes — exactly
//     what control.Bundle.TestFiles holds after ParseTarball.
//   - opts.Tx is the OPTIONAL tenant transaction required only for SQL-language
//     fixtures (the upload handler has one; the CLI directory path usually does
//     not).
//
// An empty testFiles map yields an empty report (zero cases) — the caller's
// no-tests policy decides whether that is acceptable, exactly as Run does for a
// missing tests/ directory. A malformed test file or a duplicate case name is a
// load error (returned, not a per-case failure) so a structurally broken test
// suite is loud, not silently green.
func RunFromFiles(ctx context.Context, bundleID, freshnessClass string, queries []eval.FixtureQuery, testFiles map[string][]byte, opts Options) (*Report, error) {
	cases, err := loadTestCasesFromBytes(testFiles)
	if err != nil {
		return nil, err
	}

	now := opts.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}

	report := &Report{BundleID: bundleID, Cases: make([]CaseResult, 0, len(cases))}
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

// QueriesFromManifest extracts the (language, expression) pairs the runner
// evaluates from a bundle's parsed evidence_queries. languages and expressions
// are taken verbatim; an unsupported language surfaces as a per-case ERROR at
// evaluation (eval.evalFixtureQueries is fail-loud), never a silent skip.
func QueriesFromManifest(qs []ManifestQuery) []eval.FixtureQuery {
	out := make([]eval.FixtureQuery, 0, len(qs))
	for _, q := range qs {
		out = append(out, eval.FixtureQuery{Language: q.Language, Expression: q.Expression})
	}
	return out
}

// ManifestQuery is the minimal shape QueriesFromManifest reads — a (language,
// expression) pair. It lets the gate hand the runner the manifest's evidence
// queries without bundletest importing internal/control (which would create an
// import cycle, since control would then import bundletest for the gate).
type ManifestQuery struct {
	Language   string
	Expression string
}

// loadTestCasesFromBytes is the in-memory counterpart of LoadTestCases: it
// parses every provided tests/*.yaml byte blob, validates each case, and
// enforces the same cross-file unique-name rule. Files are processed in
// lexical basename order so the resulting case order — and thus the report — is
// deterministic, matching the directory loader.
func loadTestCasesFromBytes(testFiles map[string][]byte) ([]TestCase, error) {
	if len(testFiles) == 0 {
		return nil, nil
	}

	names := make([]string, 0, len(testFiles))
	for name := range testFiles {
		names = append(names, name)
	}
	sort.Strings(names)

	var cases []TestCase
	seen := make(map[string]string) // case name → file it first appeared in
	for _, fname := range names {
		tf, err := parseTestFileBytes(fname, testFiles[fname])
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
