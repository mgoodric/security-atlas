// gate.go — the slice-574 control-bundle upload test-gate.
//
// "Tests must pass to upload." Slice 496 shipped the control-bundle test runner
// (`atlas-cli controls test`) but deliberately did NOT wire it into the upload
// path (P0-496-5). This file closes that loop: when a bundle is uploaded, the
// handler runs the bundle's declared tests/ cases through the SAME
// bundletest runner the CLI uses (anti-criterion P0-496-1) and, under the
// strict policy, REJECTS the upload (400 + per-case report) if any case fails or
// errors — so a control whose evidence query is provably wrong against its own
// fixtures never reaches the catalog.
//
// # Gate policy (the slice-574 JUDGMENT call — see docs/audit-log/574-*-decisions.md)
//
//   - HARD-BLOCK when a bundle ships tests/ and any case fails or errors. The
//     upload is rejected 400 with the per-case report (AC-2). This is the
//     spec's primary intent: "the bundle ships only if its tests are green."
//   - ALLOW (warn) a bundle with NO tests. A bundle that ships no tests/ uploads
//     successfully; the success response carries a gate_warning so the absence
//     is visible. This matches slice 496's CLI (no-tests = warning, not failure)
//     and avoids blocking the inline-JSON path (which structurally cannot carry
//     fixtures) and every existing test-less bundle. A future per-tenant
//     `require_bundle_tests_pass` flag can flip this to mandatory-tests (filed
//     as a spillover — global default for v0).
//   - SQL fixtures RUN on the upload path (AC-4): the handler has a tenant tx
//     (Store.WithReadOnlyTenantTx), so a sql-language fixture evaluates inside a
//     read-only subtransaction and a SQL fixture failure maps to the same 400.
//     Invariant #2 holds: the gate's tx is READ ONLY and always rolled back.
package controls

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/mgoodric/security-atlas/internal/control"
	"github.com/mgoodric/security-atlas/internal/control/bundletest"
)

// gateVerdict is the gate's decision about one uploaded bundle.
type gateVerdict struct {
	// blocked is true when the upload must be rejected (strict policy + a
	// failing/errored case). When false the upload proceeds.
	blocked bool
	// report is the per-case test report (nil when the bundle had no tests).
	// Surfaced on rejection so the uploader sees exactly which fixture failed
	// (AC-5: the CLI renders this report on a 400).
	report *bundletest.Report
	// warning, when non-empty, is a non-fatal note attached to a SUCCESSFUL
	// upload (e.g. "bundle declares no tests").
	warning string
}

// hasTests reports whether the bundle carried any tests/*.yaml files.
func (b gateVerdict) hasTests() bool { return b.report != nil && len(b.report.Cases) > 0 }

// txRunner abstracts the read-only tenant transaction the gate needs to
// evaluate SQL-language fixtures. *control.Store satisfies it via
// WithReadOnlyTenantTx; unit tests pass a no-DB stub (Rego/JSON-path bundles
// need no tx at all).
type txRunner interface {
	WithReadOnlyTenantTx(ctx context.Context, fn func(pgx.Tx) error) error
}

// runGate evaluates the uploaded bundle's tests and returns the gate verdict.
//
// Behaviour:
//   - No tests/ files  → verdict{blocked:false, warning:"…no tests…"} (allow).
//   - Tests, all green → verdict{blocked:false} (allow).
//   - Tests, any red   → verdict{blocked:true, report:…} (reject under strict).
//
// The runner only opens a read-only tenant tx when the bundle actually declares
// a sql-language query — Rego/JSON-path-only bundles evaluate fully in memory
// with no database (matching eval.EvaluateFixture's AC-9 contract). A nil
// runner (unit servers with no pool) degrades gracefully: SQL fixtures then
// surface as a per-case ERROR (ErrFixtureSQLNeedsDB), which the strict policy
// treats as a block — never a silent pass.
func runGate(ctx context.Context, runner txRunner, bundle *control.Bundle) (gateVerdict, error) {
	mqs := manifestQueries(bundle)
	queries := bundletest.QueriesFromManifest(mqs)

	// No tests: allow with a warning. The verdict's report stays nil.
	if len(bundle.TestFiles) == 0 {
		return gateVerdict{
			blocked: false,
			warning: fmt.Sprintf("bundle %q declares no test cases (tests/ is absent); upload accepted but its evidence query is unverified", bundle.Manifest.BundleID),
		}, nil
	}

	freshnessClass := bundle.Manifest.FreshnessClass

	var report *bundletest.Report
	if needsSQL(mqs) && runner != nil {
		// SQL fixtures need a real (read-only) tenant tx. Run the whole suite
		// inside it so every case sees the same connection.
		err := runner.WithReadOnlyTenantTx(ctx, func(tx pgx.Tx) error {
			r, runErr := bundletest.RunFromFiles(ctx, bundle.Manifest.BundleID, freshnessClass, queries, bundle.TestFiles, bundletest.Options{Tx: tx})
			if runErr != nil {
				return runErr
			}
			report = r
			return nil
		})
		if err != nil {
			return gateVerdict{}, err
		}
	} else {
		// Rego/JSON-path-only (or no runner available): no DB needed. A SQL
		// query with no tx surfaces as a per-case ERROR, handled by the policy
		// below as a block.
		r, err := bundletest.RunFromFiles(ctx, bundle.Manifest.BundleID, freshnessClass, queries, bundle.TestFiles, bundletest.Options{})
		if err != nil {
			return gateVerdict{}, err
		}
		report = r
	}

	// Strict policy: any failing OR errored case blocks the upload.
	return gateVerdict{
		blocked: !report.AllPassed(),
		report:  report,
	}, nil
}

// needsSQL reports whether any declared query uses the sql language (the only
// language that requires a database transaction to evaluate a fixture).
func needsSQL(queries []bundletest.ManifestQuery) bool {
	for _, q := range queries {
		if q.Language == "sql" {
			return true
		}
	}
	return false
}

// manifestQueries adapts the parsed manifest's evidence queries to the minimal
// (language, expression) shape bundletest.QueriesFromManifest reads. Keeping
// the adapter here (rather than bundletest importing internal/control) avoids
// an import cycle: control would import bundletest for the gate.
func manifestQueries(bundle *control.Bundle) []bundletest.ManifestQuery {
	out := make([]bundletest.ManifestQuery, 0, len(bundle.Manifest.EvidenceQueries))
	for _, q := range bundle.Manifest.EvidenceQueries {
		out = append(out, bundletest.ManifestQuery{Language: q.Language, Expression: q.Expression})
	}
	return out
}
