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
		for _, want := range []string{eval.ResultPass, eval.ResultFail, "empty"} {
			if !seen[want] {
				t.Errorf("%s: tests/ has no %q case; a SQL payload check needs a pass, a fail and an empty-ledger inconclusive case", name, want)
			}
		}
	})
	if sqlBundles == 0 {
		t.Fatal("no stock bundle declares a SQL query; soc2_cc6_1_logical_access_baseline should (#2821)")
	}
}
