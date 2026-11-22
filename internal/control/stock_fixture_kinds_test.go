package control_test

import (
	"path/filepath"
	"testing"

	"github.com/mgoodric/security-atlas/internal/control"
	"github.com/mgoodric/security-atlas/internal/control/bundletest"
)

// #2882: in a stock bundle whose queries read two kinds, every fixture
// record names its evidence_kind. A record without one reaches every query
// in the test and none of them live (decisions log 2818 D3, D4), so the
// case would never exercise the per-query kind split. For a tenant bundle
// this is a warning; for a stock bundle it fails here.
func TestStockTwoKindBundles_FixtureRecordsDeclareEvidenceKind(t *testing.T) {
	soc2 := filepath.Join(repoRoot(t), "controls", "soc2")
	forEachSOC2Bundle(t, func(name string, b *control.Bundle) {
		cases, err := bundletest.LoadTestCases(filepath.Join(soc2, name))
		if err != nil {
			t.Fatalf("%s: load tests: %v", name, err)
		}
		qs := make([]bundletest.ManifestQuery, 0, len(b.Manifest.EvidenceQueries))
		for _, q := range b.Manifest.EvidenceQueries {
			qs = append(qs, bundletest.ManifestQuery{Language: q.Language, Expression: q.Expression, EvidenceKind: q.EvidenceKind})
		}
		for _, w := range bundletest.KindlessRecordWarnings(bundletest.QueriesFromManifest(qs), cases) {
			t.Errorf("%s: %s", name, w)
		}
	})
}

// The SQL bundles' tests run against Postgres in
// stock_sql_checks_integration_test.go. The rest need no database, so they
// run here, and a stock rego or jsonpath bundle's tests/ gate the unit tier.
func TestStockNonSQLBundleTests_Pass(t *testing.T) {
	soc2 := filepath.Join(repoRoot(t), "controls", "soc2")
	ran := 0
	forEachSOC2Bundle(t, func(name string, b *control.Bundle) {
		if hasSQLQuery(b) {
			return
		}
		rep, err := bundletest.Run(t.Context(), filepath.Join(soc2, name), bundletest.Options{})
		if err != nil {
			t.Fatalf("%s: run bundle tests: %v", name, err)
		}
		ran += len(rep.Cases)
		for _, c := range rep.Cases {
			if !c.Passed {
				t.Errorf("%s / %s: expected %s, got %s (err %q)", name, c.Name, c.ExpectedState, c.ActualState, c.Err)
			}
		}
	})
	if ran == 0 {
		t.Fatal("no stock rego or jsonpath bundle ships tests; soc2_p4_2_retention_schedule should (#2882)")
	}
}
