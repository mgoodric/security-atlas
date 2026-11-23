package control_test

import (
	"path/filepath"
	"testing"

	"github.com/mgoodric/security-atlas/internal/control"
	"github.com/mgoodric/security-atlas/internal/control/bundletest"
	"github.com/mgoodric/security-atlas/internal/eval"
)

// hasSQLQuery reports whether a bundle declares a SQL query. A SQL query is
// a payload check (ADR 0028 decision 1): it decides on fields inside each
// record's payload, so the result-only roll-up properties in
// stock_policy_eval_test.go and stock_kind_filter_test.go don't describe it,
// and it needs a Postgres transaction to run. Its bundle's tests/ cases
// cover it instead, run against Postgres in stock_sql_checks_integration_test.go.
func hasSQLQuery(b *control.Bundle) bool {
	for _, q := range b.Manifest.EvidenceQueries {
		if q.Language == "sql" {
			return true
		}
	}
	return false
}

// stockSQLChecksWithoutFail lists the stock SQL checks that cannot fail on
// the records they receive, each with the reason. Such a check states it
// never fails in its description, and its "every X" half is pending the
// inventory design (#2834, ADR 0028 decision 4).
var stockSQLChecksWithoutFail = map[string]string{
	// Received acknowledgements show that someone acknowledged the IR plan,
	// never that an on-call person did not (#2832).
	"soc2_cc7_4_incident_response": "#2832: no fail state until the on-call inventory (#2834)",
}

// stockSQLChecksWithoutPass lists the stock SQL checks that cannot pass on
// the records they receive, each with the reason. Such a check states it
// never passes in its description.
var stockSQLChecksWithoutPass = map[string]string{
	// The audit-event stream being off fails cc6_3; nothing documented
	// shows the account's 2FA or password rules, so it never passes (#2968).
	"soc2_cc6_3_privileged_account_management": "#2968: no documented 1Password source for 2FA or password rules",
}

// #2821 pattern: a stock payload check ships with bundle tests that state
// what passes, what fails, and that an empty ledger is inconclusive, so the
// upload gate and the integration tier both exercise the query.
func TestStockSQLChecksShipBundleTests(t *testing.T) {
	soc2 := filepath.Join(repoRoot(t), "controls", "soc2")
	sqlBundles := 0
	forEachSOC2Bundle(t, func(name string, b *control.Bundle) {
		if !hasSQLQuery(b) {
			return
		}
		sqlBundles++
		cases, err := bundletest.LoadTestCases(filepath.Join(soc2, name))
		if err != nil {
			t.Fatalf("%s: load tests: %v", name, err)
		}
		seen := map[string]bool{}
		for _, c := range cases {
			seen[c.ExpectedState] = true
			if c.ExpectedState == eval.ResultInconclusive && len(c.Records) == 0 {
				seen["empty"] = true
			}
		}
		wants := []string{eval.ResultPass, eval.ResultFail, "empty"}
		if reason, ok := stockSQLChecksWithoutFail[name]; ok {
			if seen[eval.ResultFail] {
				t.Errorf("%s: has a \"fail\" case but is listed as never failing (%s); drop it from stockSQLChecksWithoutFail", name, reason)
			}
			wants = []string{eval.ResultPass, "empty"}
		}
		if reason, ok := stockSQLChecksWithoutPass[name]; ok {
			if seen[eval.ResultPass] {
				t.Errorf("%s: has a \"pass\" case but is listed as never passing (%s); drop it from stockSQLChecksWithoutPass", name, reason)
			}
			wants = []string{eval.ResultFail, "empty"}
		}
		for _, want := range wants {
			if !seen[want] {
				t.Errorf("%s: tests/ has no %q case; a SQL payload check needs a pass, a fail and an empty-ledger inconclusive case", name, want)
			}
		}
	})
	if sqlBundles == 0 {
		t.Fatal("no stock bundle declares a SQL query; soc2_cc6_1_logical_access_baseline should (#2821)")
	}
}

// #2911: a stock SQL check compares against the `evidence.evaluated_at`
// column, never the wall clock, so a replay, an audit-period re-evaluation
// and a bundle test with pinned dates all give the verdict they gave first.
func TestStockSQLChecksNeverReadTheWallClock(t *testing.T) {
	forEachSOC2Bundle(t, func(name string, b *control.Bundle) {
		for _, q := range b.Manifest.EvidenceQueries {
			if q.Language == "sql" && eval.SQLUsesWallClock(q.Expression) {
				t.Errorf("%s / %s: SQL query reads the wall clock (now(), current_timestamp, clock_timestamp() or similar); compare against evaluated_at instead", name, q.ID)
			}
		}
	})
}

// #2939: a stock SQL check casts a payload timestamp only behind the full
// guard (RFC3339Pattern and pg_input_is_valid), so one malformed record is
// read as missing instead of erroring the query and making the control
// inconclusive for every host. The start-anchored prefix guard the first
// osquery checks used admits 2026-13-45T00:00Z, which errors the cast.
func TestStockSQLChecksGuardTimestampCasts(t *testing.T) {
	forEachSOC2Bundle(t, func(name string, b *control.Bundle) {
		for _, q := range b.Manifest.EvidenceQueries {
			if q.Language != "sql" {
				continue
			}
			unguarded, err := eval.SQLUnguardedTimestampCasts(q.Expression)
			if err != nil {
				t.Errorf("%s / %s: %v", name, q.ID, err)
				continue
			}
			for _, v := range unguarded {
				t.Errorf("%s / %s: converts %s to a time outside the full guard; put the conversion in the THEN branch of a CASE WHEN that matches %s against eval.RFC3339Pattern AND pg_input_is_valid(%s, 'timestamptz') (docs/spec/control-bundle.md, \"Reading a payload timestamp\")", name, q.ID, v, v, v)
			}
		}
	})
}
