package control_test

import (
	"context"
	"testing"
	"time"

	"github.com/mgoodric/security-atlas/internal/control"
	"github.com/mgoodric/security-atlas/internal/eval"
)

// Every stock control's evidence queries must evaluate on a freshly seeded
// install. The self-host bootstrap (deploy/docker/bootstrap/bootstrap.sh)
// uploads every controls/soc2/ bundle, and the eval scheduler runs them
// against an empty ledger. A query that compiles but never assigns the
// sandbox's `data.evidence.query.result` errors there ("policy did not assign
// `result`"), and EvaluateAll stops at the first such control (#1728).
//
// The cases run through eval.EvaluateFixture, the same dispatch the live
// engine uses, so a bundle that passes here evaluates identically once
// uploaded. The empty case is the fresh install; the single-record cases
// prove each query actually decides rather than erroring on real input.
func TestStockControlPoliciesEvaluateOnFreshInstall(t *testing.T) {
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name    string
		records []eval.FixtureRecord
		want    string
	}{
		{name: "empty ledger", records: nil, want: eval.ResultInconclusive},
		{name: "one pass", records: []eval.FixtureRecord{{Result: eval.ResultPass, ObservedAt: now}}, want: eval.ResultPass},
		{name: "one fail", records: []eval.FixtureRecord{{Result: eval.ResultFail, ObservedAt: now}}, want: eval.ResultFail},
		{name: "pass and fail", records: []eval.FixtureRecord{
			{Result: eval.ResultPass, ObservedAt: now},
			{Result: eval.ResultFail, ObservedAt: now},
		}, want: eval.ResultFail},
		{name: "one na", records: []eval.FixtureRecord{{Result: eval.ResultNA, ObservedAt: now}}, want: eval.ResultNA},
		{name: "one inconclusive", records: []eval.FixtureRecord{{Result: eval.ResultInconclusive, ObservedAt: now}}, want: eval.ResultInconclusive},
		{name: "pass and na", records: []eval.FixtureRecord{
			{Result: eval.ResultPass, ObservedAt: now},
			{Result: eval.ResultNA, ObservedAt: now},
		}, want: eval.ResultPass},
	}

	withQueries := 0
	forEachSOC2Bundle(t, func(name string, b *control.Bundle) {
		if len(b.Manifest.EvidenceQueries) == 0 {
			return
		}
		withQueries++
		queries := make([]eval.FixtureQuery, len(b.Manifest.EvidenceQueries))
		for i, q := range b.Manifest.EvidenceQueries {
			queries[i] = eval.FixtureQuery{Language: q.Language, Expression: q.Expression}
		}
		for _, tc := range cases {
			got, err := eval.EvaluateFixture(context.Background(), b.Manifest.FreshnessClass, queries, tc.records, now, nil)
			if err != nil {
				t.Errorf("%s / %s: evaluate: %v", name, tc.name, err)
				continue
			}
			if got.Result != tc.want {
				t.Errorf("%s / %s: result = %q, want %q", name, tc.name, got.Result, tc.want)
			}
		}
	})
	if withQueries == 0 {
		t.Fatal("no stock control declares an evidence query; the test would pass vacuously")
	}
}
