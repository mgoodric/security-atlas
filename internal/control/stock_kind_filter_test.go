package control_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mgoodric/security-atlas/internal/control"
	"github.com/mgoodric/security-atlas/internal/eval"
)

// #2818: every stock query declares the evidence_kind it reads, so none
// keeps the see-every-record behaviour the validator warns on.
func TestStockQueriesDeclareEvidenceKind(t *testing.T) {
	forEachSOC2Bundle(t, func(name string, b *control.Bundle) {
		for _, w := range b.Manifest.UnkindedQueryWarnings() {
			t.Errorf("%s: %s", name, w)
		}
	})
}

// #2818 acceptance: stock evaluation results don't change, except where a
// query was reading another kind's records. Each stock query is evaluated
// as before #2818 (no kind, so it sees every record) and as after (its
// declared kind), over every combination of one record per declared kind,
// each absent or carrying one of the four results, plus optionally a record
// of a kind no query declares. The results may differ only when a query was
// handed records not of its kind before: a declared kind with no record, or
// a record of an undeclared kind. With every declared kind present and no
// stray kind, they must be equal. docs/audit-log/2818-per-query-evidence-kind-decisions.md
// lists the cases that do change.
func TestStockControlsKindFilterChangesOnlyCrossKindReads(t *testing.T) {
	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	const strayKind = "test.stray_kind.v1"
	results := []string{"", eval.ResultPass, eval.ResultFail, eval.ResultNA, eval.ResultInconclusive}

	compared := 0
	forEachSOC2Bundle(t, func(name string, b *control.Bundle) {
		if len(b.Manifest.EvidenceQueries) == 0 || hasSQLQuery(b) {
			return
		}
		var kinds []string
		before := make([]eval.FixtureQuery, len(b.Manifest.EvidenceQueries))
		after := make([]eval.FixtureQuery, len(b.Manifest.EvidenceQueries))
		for i, q := range b.Manifest.EvidenceQueries {
			if q.EvidenceKind == strayKind {
				t.Fatalf("%s declares %s; pick another stray kind for this test", name, strayKind)
			}
			kinds = append(kinds, q.EvidenceKind)
			before[i] = eval.FixtureQuery{Language: q.Language, Expression: q.Expression}
			after[i] = eval.FixtureQuery{Language: q.Language, Expression: q.Expression, EvidenceKind: q.EvidenceKind}
		}

		for _, stray := range results {
			forEachAssignment(len(kinds), len(results), func(pick []int) {
				var recs []eval.FixtureRecord
				allPresent := true
				for i, r := range pick {
					if results[r] == "" {
						allPresent = false
						continue
					}
					recs = append(recs, eval.FixtureRecord{Result: results[r], ObservedAt: now, EvidenceKind: kinds[i]})
				}
				if stray != "" {
					recs = append(recs, eval.FixtureRecord{Result: stray, ObservedAt: now, EvidenceKind: strayKind})
				}
				old, err := eval.EvaluateFixture(context.Background(), b.Manifest.FreshnessClass, before, recs, now, nil)
				if err != nil {
					t.Fatalf("%s: before: %v", name, err)
				}
				got, err := eval.EvaluateFixture(context.Background(), b.Manifest.FreshnessClass, after, recs, now, nil)
				if err != nil {
					t.Fatalf("%s: after: %v", name, err)
				}
				compared++
				if allPresent && stray == "" && got.Result != old.Result {
					t.Errorf("%s %s: result changed from %q to %q with every declared kind present and no stray kind",
						name, describe(kinds, results, pick, stray), old.Result, got.Result)
				}
			})
		}
	})
	if compared == 0 {
		t.Fatal("no stock control declares an evidence query; the test would pass vacuously")
	}
}

// The two stock controls with two queries over two kinds: with only one
// kind present, a declared kind with no record makes its query inconclusive,
// which turns an all-na control inconclusive. It is the only result that
// changes when no stray kind is present, and the decisions log records it.
// A two-query control with a SQL query (cc1_4 since #2824) is still counted
// but not evaluated here: its SQL query needs Postgres, and its bundle
// tests/ cases cover the cross-kind combination instead.
func TestStockTwoKindControls_MissingKindOnlyTurnsNAInconclusive(t *testing.T) {
	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	two := map[string]bool{}
	forEachSOC2Bundle(t, func(name string, b *control.Bundle) {
		if len(b.Manifest.EvidenceQueries) != 2 {
			return
		}
		two[name] = true
		if hasSQLQuery(b) {
			return
		}
		var before, after []eval.FixtureQuery
		for _, q := range b.Manifest.EvidenceQueries {
			before = append(before, eval.FixtureQuery{Language: q.Language, Expression: q.Expression})
			after = append(after, eval.FixtureQuery{Language: q.Language, Expression: q.Expression, EvidenceKind: q.EvidenceKind})
		}
		for _, q := range b.Manifest.EvidenceQueries {
			for _, r := range []string{eval.ResultPass, eval.ResultFail, eval.ResultNA, eval.ResultInconclusive} {
				recs := []eval.FixtureRecord{{Result: r, ObservedAt: now, EvidenceKind: q.EvidenceKind}}
				old, err := eval.EvaluateFixture(context.Background(), b.Manifest.FreshnessClass, before, recs, now, nil)
				if err != nil {
					t.Fatal(err)
				}
				got, err := eval.EvaluateFixture(context.Background(), b.Manifest.FreshnessClass, after, recs, now, nil)
				if err != nil {
					t.Fatal(err)
				}
				want := old.Result
				if r == eval.ResultNA {
					want = eval.ResultInconclusive
				}
				if got.Result != want {
					t.Errorf("%s: only %s=%s: result = %q, want %q (before #2818: %q)", name, q.EvidenceKind, r, got.Result, want, old.Result)
				}
			}
		}
	})
	// a1_1 joined in #2935 (osquery.host_posture.v1 and
	// monitoring.host_capacity.v1, both SQL).
	want := []string{"soc2_a1_1_capacity_monitoring", "soc2_cc1_4_competence_training", "soc2_p4_2_retention_schedule"}
	for _, w := range want {
		if !two[w] {
			t.Errorf("%s no longer has two queries; update this test and the #2818 decisions log", w)
		}
	}
	if len(two) != len(want) {
		t.Errorf("two-query stock controls = %v; update this test and the #2818 decisions log", two)
	}
}

// forEachAssignment calls fn with every vector of n indices in [0, k).
func forEachAssignment(n, k int, fn func([]int)) {
	pick := make([]int, n)
	for {
		fn(pick)
		i := 0
		for ; i < n; i++ {
			pick[i]++
			if pick[i] < k {
				break
			}
			pick[i] = 0
		}
		if i == n {
			return
		}
	}
}

func describe(kinds, results []string, pick []int, stray string) string {
	parts := make([]string, 0, len(kinds)+1)
	for i, r := range pick {
		v := results[r]
		if v == "" {
			v = "absent"
		}
		parts = append(parts, kinds[i]+"="+v)
	}
	if stray != "" {
		parts = append(parts, "stray="+stray)
	}
	return "[" + strings.Join(parts, " ") + "]"
}
