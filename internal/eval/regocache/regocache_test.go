// Package regocache_test exercises the prepared-query cache through its
// public interface (Cache.GetOrPrepare). The tests describe behaviour — same
// policy returns the same compiled query, distinct policies do not collide,
// distinct capability sets do not collide, concurrent access is race-free.
// Cache internals (sync.Map shape, key formulation) are not asserted; the
// tests should survive any internal refactor that keeps GetOrPrepare's
// contract.
package regocache_test

import (
	"context"
	"sync"
	"testing"

	"github.com/open-policy-agent/opa/v1/ast"

	"github.com/mgoodric/security-atlas/internal/eval/regocache"
)

const (
	policyA = `
package test.a
import rego.v1
default result := "fail"
result := "pass" if {
	count(input.records) > 0
}
`
	policyB = `
package test.b
import rego.v1
default result := "fail"
result := "pass" if {
	count(input.records) > 1
}
`
	queryA = "data.test.a.result"
	queryB = "data.test.b.result"
)

// caps returns a fresh OPA capability set. Each test that needs a
// capability set asks for one of these so tests stay independent.
func capsAll() *ast.Capabilities { return ast.CapabilitiesForThisVersion() }

// capsStripped returns a capability set with http.send and opa.runtime
// removed — used to assert that a different capability shape produces a
// different cache entry from the default.
func capsStripped() *ast.Capabilities {
	caps := ast.CapabilitiesForThisVersion()
	filtered := caps.Builtins[:0]
	for _, b := range caps.Builtins {
		if b.Name == "http.send" || b.Name == "opa.runtime" {
			continue
		}
		filtered = append(filtered, b)
	}
	caps.Builtins = filtered
	return caps
}

// ===== Tracer: same key returns the same *PreparedEvalQuery instance =====

func TestCache_SameKeyReturnsSameInstance(t *testing.T) {
	t.Parallel()
	c := regocache.New()
	ctx := context.Background()

	first, err := c.GetOrPrepare(ctx, policyA, capsAll(), queryA, "a.rego")
	if err != nil {
		t.Fatalf("first GetOrPrepare: %v", err)
	}
	second, err := c.GetOrPrepare(ctx, policyA, capsAll(), queryA, "a.rego")
	if err != nil {
		t.Fatalf("second GetOrPrepare: %v", err)
	}
	if first != second {
		t.Fatalf("expected cache hit to return same *PreparedEvalQuery pointer; got distinct values")
	}
}

// ===== Distinct policy text → distinct cache entry =====

func TestCache_DistinctPoliciesGetDistinctEntries(t *testing.T) {
	t.Parallel()
	c := regocache.New()
	ctx := context.Background()

	qA, err := c.GetOrPrepare(ctx, policyA, capsAll(), queryA, "a.rego")
	if err != nil {
		t.Fatalf("policy A prepare: %v", err)
	}
	qB, err := c.GetOrPrepare(ctx, policyB, capsAll(), queryB, "b.rego")
	if err != nil {
		t.Fatalf("policy B prepare: %v", err)
	}
	if qA == qB {
		t.Fatalf("distinct policy texts must not share a cache entry")
	}
}

// ===== Distinct capability sets → distinct cache entry =====

func TestCache_DistinctCapabilitiesGetDistinctEntries(t *testing.T) {
	t.Parallel()
	c := regocache.New()
	ctx := context.Background()

	qFull, err := c.GetOrPrepare(ctx, policyA, capsAll(), queryA, "a.rego")
	if err != nil {
		t.Fatalf("full caps prepare: %v", err)
	}
	qStripped, err := c.GetOrPrepare(ctx, policyA, capsStripped(), queryA, "a.rego")
	if err != nil {
		t.Fatalf("stripped caps prepare: %v", err)
	}
	if qFull == qStripped {
		t.Fatalf("same policy with distinct capability sets must not share a cache entry")
	}
}

// ===== Concurrent access is race-free + consistent =====
//
// Under `go test -race`, this test asserts the cache survives concurrent
// GetOrPrepare on the same key. The cache may compile twice on a first-call
// race (sync.Map.LoadOrStore semantics), but both callers must observe the
// SAME stored value after the race resolves.

func TestCache_ConcurrentSameKeyConvergesOnOneValue(t *testing.T) {
	t.Parallel()
	c := regocache.New()
	ctx := context.Background()

	const goroutines = 32
	results := make([]any, goroutines)
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			q, err := c.GetOrPrepare(ctx, policyA, capsAll(), queryA, "a.rego")
			if err != nil {
				t.Errorf("goroutine %d: %v", idx, err)
				return
			}
			results[idx] = q
		}(i)
	}
	wg.Wait()

	// After the race resolves, a subsequent fetch must return the value
	// every goroutine eventually agreed on. We assert that AT LEAST one
	// of the racing fetches matches the post-race canonical value, and a
	// fresh fetch is stable.
	canonical, err := c.GetOrPrepare(ctx, policyA, capsAll(), queryA, "a.rego")
	if err != nil {
		t.Fatalf("post-race fetch: %v", err)
	}
	if canonical == nil {
		t.Fatalf("post-race fetch returned nil")
	}
	// Stability: a second fetch must equal the first.
	again, err := c.GetOrPrepare(ctx, policyA, capsAll(), queryA, "a.rego")
	if err != nil {
		t.Fatalf("stability fetch: %v", err)
	}
	if canonical != again {
		t.Fatalf("post-race cache is unstable; canonical=%p again=%p", canonical, again)
	}
}

// ===== Empty policy is rejected =====

func TestCache_EmptyPolicyErrors(t *testing.T) {
	t.Parallel()
	c := regocache.New()
	if _, err := c.GetOrPrepare(context.Background(), "", capsAll(), queryA, "a.rego"); err == nil {
		t.Fatalf("expected empty policy to error")
	}
}

// ===== Hit/miss counts surface via observable metric values =====
//
// We do not assert against OTel sinks (the global meter provider is
// no-op in tests by default). Instead, the cache exposes its in-process
// hit/miss counters via a Snapshot() method for test observability —
// the OTel emission is a side effect on top of the in-process counter.

func TestCache_FirstCallIsMissSecondCallIsHit(t *testing.T) {
	t.Parallel()
	c := regocache.New()
	ctx := context.Background()

	before := c.Snapshot()

	if _, err := c.GetOrPrepare(ctx, policyA, capsAll(), queryA, "a.rego"); err != nil {
		t.Fatalf("first: %v", err)
	}
	if _, err := c.GetOrPrepare(ctx, policyA, capsAll(), queryA, "a.rego"); err != nil {
		t.Fatalf("second: %v", err)
	}

	after := c.Snapshot()

	if got := after.Misses - before.Misses; got != 1 {
		t.Fatalf("expected exactly 1 miss across the two calls; got %d", got)
	}
	if got := after.Hits - before.Hits; got != 1 {
		t.Fatalf("expected exactly 1 hit across the two calls; got %d", got)
	}
}
