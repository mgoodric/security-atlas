// Slice 449 — OPA 1.17 regression gate (regocache correctness contract).
//
// The slice-377 prepared-query cache (this package) is a security-critical
// fast-path: a stale or semantically-divergent cached query would silently
// change a control verdict. The existing regocache_test.go asserts the
// cache's structural contract (same key -> same pointer, distinct keys ->
// distinct entries, hit/miss counts). It does NOT assert the property that
// matters most across an engine bump: a CACHED prepared query must yield
// the SAME evaluation result as a FRESH (uncached) prepare of the same
// policy under OPA 1.17.
//
// This file pins that correctness contract. If a 1.x evaluation-semantics
// change ever made the cached AST diverge from a fresh prepare (e.g. a
// capability-fingerprint mismatch, or a prepare-time vs eval-time coercion
// shift), this assertion fails. It composes with AC-4: the benchmark
// already measures that the fast-path is HIT; this measures that the
// fast-path is CORRECT.
package regocache_test

import (
	"context"
	"testing"

	"github.com/open-policy-agent/opa/v1/ast"
	"github.com/open-policy-agent/opa/v1/rego"

	"github.com/mgoodric/security-atlas/internal/eval/regocache"
)

// evalControlPolicy is a representative control-as-code evidence query in
// the exact shape internal/eval/rego.go issues: package evidence.query,
// rego.v1, result over input.records. Mirrors the production query string.
const evalControlPolicy = `
package evidence.query
import rego.v1
default result := "fail"
result := "pass" if {
	count(input.records) > 0
	every r in input.records { r.result == "pass" }
}
`

const evalControlQuery = "data.evidence.query.result"

// freshEval prepares the policy WITHOUT the cache and evaluates it. This
// is the ground-truth oracle: whatever a from-scratch 1.17 prepare+eval
// produces is what the cached path MUST reproduce.
func freshEval(t *testing.T, ctx context.Context, caps *ast.Capabilities, input map[string]interface{}) string {
	t.Helper()
	q, err := rego.New(
		rego.Query(evalControlQuery),
		rego.Module("evidence_query.rego", evalControlPolicy),
		rego.Capabilities(caps),
	).PrepareForEval(ctx)
	if err != nil {
		t.Fatalf("fresh prepare: %v", err)
	}
	rs, err := q.Eval(ctx, rego.EvalInput(input))
	if err != nil {
		t.Fatalf("fresh eval: %v", err)
	}
	if len(rs) == 0 || len(rs[0].Expressions) == 0 {
		t.Fatalf("fresh eval produced no result")
	}
	s, ok := rs[0].Expressions[0].Value.(string)
	if !ok {
		t.Fatalf("fresh eval result is %T, want string", rs[0].Expressions[0].Value)
	}
	return s
}

// cachedEval evaluates the same policy through the slice-377 cache.
func cachedEval(t *testing.T, ctx context.Context, c *regocache.Cache, caps *ast.Capabilities, input map[string]interface{}) string {
	t.Helper()
	q, err := c.GetOrPrepare(ctx, evalControlPolicy, caps, evalControlQuery, "evidence_query.rego")
	if err != nil {
		t.Fatalf("cached prepare: %v", err)
	}
	rs, err := q.Eval(ctx, rego.EvalInput(input))
	if err != nil {
		t.Fatalf("cached eval: %v", err)
	}
	if len(rs) == 0 || len(rs[0].Expressions) == 0 {
		t.Fatalf("cached eval produced no result")
	}
	s, ok := rs[0].Expressions[0].Value.(string)
	if !ok {
		t.Fatalf("cached eval result is %T, want string", rs[0].Expressions[0].Value)
	}
	return s
}

// TestSlice449_CachedEqualsFresh asserts that across a spread of inputs,
// the cached prepared query (GetOrPrepare) yields the IDENTICAL control
// verdict to a fresh uncached prepare under OPA 1.17. The cache is warmed
// once and then re-hit; every hit must match ground truth. This is the
// correctness half of AC-4 (the benchmark is the performance half).
func TestSlice449_CachedEqualsFresh(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	c := regocache.New()
	caps := ast.CapabilitiesForThisVersion()

	inputs := []struct {
		name  string
		input map[string]interface{}
		want  string
	}{
		{
			name:  "all-pass",
			input: map[string]interface{}{"records": []map[string]interface{}{{"result": "pass"}, {"result": "pass"}}},
			want:  "pass",
		},
		{
			name:  "one-fail",
			input: map[string]interface{}{"records": []map[string]interface{}{{"result": "pass"}, {"result": "fail"}}},
			want:  "fail",
		},
		{
			name:  "empty-records",
			input: map[string]interface{}{"records": []map[string]interface{}{}},
			want:  "fail",
		},
	}

	for _, tc := range inputs {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			// Do NOT t.Parallel() here — the assertions share the cache c
			// and we want deterministic warm/hit ordering per case.
			fresh := freshEval(t, ctx, caps, tc.input)
			if fresh != tc.want {
				t.Fatalf("ground-truth fresh eval = %q, want %q (1.17 semantics drift?)", fresh, tc.want)
			}
			// First cached call warms the entry; second is a guaranteed hit.
			firstCached := cachedEval(t, ctx, c, caps, tc.input)
			secondCached := cachedEval(t, ctx, c, caps, tc.input)
			if firstCached != fresh {
				t.Fatalf("CORRECTNESS VIOLATION: cached eval %q != fresh eval %q under 1.17", firstCached, fresh)
			}
			if secondCached != fresh {
				t.Fatalf("CORRECTNESS VIOLATION: cache-HIT eval %q != fresh eval %q under 1.17", secondCached, fresh)
			}
		})
	}

	// Confirm the cache actually served hits (the fast-path was exercised,
	// not bypassed). One miss + >=1 hit per distinct input shape.
	snap := c.Snapshot()
	if snap.Hits == 0 {
		t.Fatalf("expected the prepared-query fast-path to be hit; Snapshot shows 0 hits (cache bypassed?)")
	}
}
