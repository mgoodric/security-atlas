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
// # Gate policy (slice-574 default + slice-608 per-tenant override)
//
// The STRICT policy (GateModeStrict — the default, preserving slice 574's
// global behaviour — see docs/audit-log/574-*-decisions.md):
//
//   - HARD-BLOCK when a bundle ships tests/ and any case fails or errors. The
//     upload is rejected 400 with the per-case report (AC-2). This is the
//     spec's primary intent: "the bundle ships only if its tests are green."
//   - ALLOW (warn) a bundle with NO tests. A bundle that ships no tests/ uploads
//     successfully; the success response carries a gate_warning so the absence
//     is visible. This matches slice 496's CLI (no-tests = warning, not failure)
//     and avoids blocking the inline-JSON path (which structurally cannot carry
//     fixtures) and every existing test-less bundle.
//
// Slice 608 makes the policy PER-TENANT via tenants.bundle_gate_mode:
//
//   - GateModeAdvisory: a bundle with red tests is ACCEPTED with the per-case
//     report attached as a warning (not a 400) — for iterative authoring.
//   - GateModeMandatoryTests: a bundle with NO tests/ is REJECTED — for tenants
//     who want every control test-backed. Red tests still hard-block.
//
// The handler resolves the mode from the tenant row (default strict on an
// absent value) and passes it to runGate.
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
	// blocked is true when the upload must be rejected (strict/mandatory policy
	// + a failing/errored case, or mandatory_tests + a bundle with no tests).
	// When false the upload proceeds.
	blocked bool
	// report is the per-case test report (nil when the bundle had no tests).
	// Surfaced on rejection so the uploader sees exactly which fixture failed
	// (AC-5: the CLI renders this report on a 400). Under advisory mode the
	// report is ALSO carried on a non-blocked verdict (advisoryReport), so the
	// uploader still sees which case is red even though the upload succeeded.
	report *bundletest.Report
	// warning, when non-empty, is a non-fatal note attached to a SUCCESSFUL
	// upload (e.g. "bundle declares no tests", or under advisory mode a note
	// that the bundle's tests are red but the upload was accepted).
	warning string
	// advisoryReport, when non-nil, is the per-case report of a RED bundle that
	// was accepted under advisory mode. It is surfaced on the success response
	// (alongside the warning) so the uploader sees the failing cases without the
	// upload being blocked (slice 608 AC-3).
	advisoryReport *bundletest.Report
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

// runGate evaluates the uploaded bundle's tests and returns the gate verdict
// under the resolved per-tenant policy (mode).
//
// Behaviour by mode:
//
//	No tests/ files:
//	  - strict / advisory      → allow with a "no tests" warning.
//	  - mandatory_tests        → BLOCK (the tenant requires every control
//	                             test-backed; slice 608 AC-4).
//	Tests, all green:
//	  - any mode               → allow.
//	Tests, any red:
//	  - strict / mandatory     → BLOCK with the per-case report (slice 574 AC-2).
//	  - advisory               → allow, attaching the report as a warning
//	                             (advisoryReport); slice 608 AC-3.
//
// The runner only opens a read-only tenant tx when the bundle actually declares
// a sql-language query — Rego/JSON-path-only bundles evaluate fully in memory
// with no database (matching eval.EvaluateFixture's AC-9 contract). A nil
// runner (unit servers with no pool) degrades gracefully: SQL fixtures then
// surface as a per-case ERROR (ErrFixtureSQLNeedsDB), which strict/mandatory
// treat as a block — never a silent pass.
func runGate(ctx context.Context, runner txRunner, bundle *control.Bundle, mode GateMode) (gateVerdict, error) {
	mqs := manifestQueries(bundle)
	queries := bundletest.QueriesFromManifest(mqs)

	// No tests: under mandatory_tests this is a hard block; otherwise allow
	// with a warning. The verdict's report stays nil.
	if len(bundle.TestFiles) == 0 {
		if mode == GateModeMandatoryTests {
			return gateVerdict{
				blocked: true,
				warning: fmt.Sprintf("bundle %q declares no test cases (tests/ is absent); this tenant's gate policy (mandatory_tests) requires every control be test-backed", bundle.Manifest.BundleID),
			}, nil
		}
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

	// All green: allow under every mode.
	if report.AllPassed() {
		return gateVerdict{blocked: false, report: report}, nil
	}

	// Red bundle. Strict + mandatory_tests both hard-block with the report;
	// advisory accepts it and surfaces the report as a warning (slice 608 AC-3).
	if mode == GateModeAdvisory {
		return gateVerdict{
			blocked:        false,
			advisoryReport: report,
			warning: fmt.Sprintf("bundle %q has %d failing and %d errored test case(s); accepted under this tenant's advisory gate policy (the upload was NOT blocked)",
				bundle.Manifest.BundleID, report.Failed, report.Errored),
		}, nil
	}
	return gateVerdict{
		blocked: true,
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
