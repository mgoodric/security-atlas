// Package regocache caches compiled `rego.PreparedEvalQuery` values keyed by
// (policy text, capability set, query, module name). It exists to close
// slice 332 audit finding F-OPA-1 — `internal/eval/rego.go evalRegoQuery`
// (and `internal/risk/aggrule/severity.go evalCustomRego`) re-prepare the
// same policy text on every call, paying ~200+ OPA compiles per
// EvaluateAll tick on a tenant-with-200-active-controls workload.
//
// The correct pattern already exists at `internal/authz/decision.go:60`
// (prepare-once-at-NewEngine + store the `rego.PreparedEvalQuery`). This
// package generalises that pattern for hot-path call sites where the
// engine is constructed per-evaluation but the policy population is
// process-stable.
//
// Cache key: SHA-256 of policy text concatenated with SHA-256 of the
// sorted capability builtin name list (plus the fixed query string and
// module name to disambiguate accidental collisions across call sites).
// The capability fingerprint is what guards against a stripped-capability
// caller getting a full-capability caller's compiled query (slice doc
// P0-3 of #377).
//
// The cache key is policy-text-derived, NOT tenant-derived (slice doc
// P0-4 of #377). Two tenants authoring the same policy text get a cache
// hit on the compiled AST; the AST holds no evaluation result so there
// is structurally no cross-tenant data leak.
//
// Storage: `sync.Map`. Per slice doc note 1 the access pattern is
// read-mostly-after-write-once; `sync.Map.LoadOrStore` is the atomic
// primitive that matches. Concurrent first-call races may compile twice
// — the second caller stores into LoadOrStore, sees the existing value,
// and discards its own work. This is acceptable for a once-per-policy
// lifetime.
package regocache

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"

	"github.com/open-policy-agent/opa/v1/ast"
	"github.com/open-policy-agent/opa/v1/rego"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
)

// ErrEmptyPolicy is returned when GetOrPrepare receives an empty policy
// string. Mirrors the empty-policy error the un-cached callers used to
// emit; lifting it to a typed error lets callers wrap it without losing
// the sentinel.
var ErrEmptyPolicy = errors.New("regocache: policy is empty")

// meterName is the OTel meter scope for this package. Follows the
// convention used elsewhere in atlas (the Go import path).
const meterName = "github.com/mgoodric/security-atlas/internal/eval/regocache"

// Metric names follow the slice 121 OTel convention (atlas_<subsystem>_<event>_total).
const (
	metricHits   = "atlas_eval_regocache_hits_total"
	metricMisses = "atlas_eval_regocache_misses_total"
)

// Cache is a prepared-query cache. Construct with New. Safe for
// concurrent use. The zero value is NOT usable — New initialises the
// OTel counters and the underlying sync.Map.
type Cache struct {
	entries sync.Map // map[string]*rego.PreparedEvalQuery — key is keyFor(...)

	// in-process counters mirror the OTel counters so tests can assert
	// hit/miss behaviour without standing up an OTel collector.
	hits   atomic.Uint64
	misses atomic.Uint64

	// OTel handles — initialised lazily on first GetOrPrepare so the
	// global MeterProvider has been wired by slice 121's Init before the
	// counters are created.
	metricsOnce sync.Once
	otelHits    metric.Int64Counter
	otelMisses  metric.Int64Counter
	otelErr     error
}

// Snapshot captures the in-process hit/miss counts at a moment in time.
// Tests use this to assert the cache observed the expected number of
// hits and misses across a batch of calls. Not part of the OTel surface.
type Snapshot struct {
	Hits   uint64
	Misses uint64
}

// New returns a fresh Cache. OTel counters are NOT created at New time —
// they are created lazily on first GetOrPrepare so the global
// MeterProvider has had a chance to be wired by main(). This matches the
// pattern used elsewhere in atlas where packages avoid touching otel.GetMeterProvider()
// at package init time.
func New() *Cache {
	return &Cache{}
}

// Snapshot returns the current in-process hit/miss counts. Safe for
// concurrent use.
func (c *Cache) Snapshot() Snapshot {
	return Snapshot{Hits: c.hits.Load(), Misses: c.misses.Load()}
}

// GetOrPrepare returns the compiled query for (policy, capabilities,
// query, moduleName). On a cache miss the policy is compiled via
// rego.PrepareForEval; on a hit the cached *rego.PreparedEvalQuery is
// returned. The caller invokes .Eval(ctx, rego.EvalInput(...)) on the
// returned value — input is supplied AT EVAL TIME, not at prepare time
// (the authz pattern: `e.query.Eval(ctx, rego.EvalInput(toRegoInput(in)))`).
//
// On policy=="" returns ErrEmptyPolicy without consulting the cache.
//
// Cache key composition: SHA-256(policy) || SHA-256(sorted-capability-builtin-names)
// || query || moduleName. The capability fingerprint is what guards
// against a stripped-capability caller receiving a different caller's
// full-capability compiled query (P0-3 of slice 377).
func (c *Cache) GetOrPrepare(
	ctx context.Context,
	policy string,
	caps *ast.Capabilities,
	query string,
	moduleName string,
) (*rego.PreparedEvalQuery, error) {
	if policy == "" {
		return nil, ErrEmptyPolicy
	}
	c.ensureOTel()

	key := keyFor(policy, caps, query, moduleName)
	if existing, ok := c.entries.Load(key); ok {
		c.hits.Add(1)
		c.recordHit(ctx)
		return existing.(*rego.PreparedEvalQuery), nil
	}

	// Miss path: compile. We may race with another goroutine on the same
	// key; LoadOrStore below resolves the race deterministically (the
	// loser's compiled query is discarded).
	q, err := rego.New(
		rego.Query(query),
		rego.Module(moduleName, policy),
		rego.Capabilities(caps),
	).PrepareForEval(ctx)
	if err != nil {
		// Misses-that-fail are still misses for observability — a
		// repeatedly-failing compile still costs CPU each time, which is
		// the signal an operator wants to see.
		c.misses.Add(1)
		c.recordMiss(ctx)
		return nil, fmt.Errorf("regocache: prepare: %w", err)
	}

	actual, loaded := c.entries.LoadOrStore(key, &q)
	if loaded {
		// Lost the race; the other goroutine's value is canonical.
		c.hits.Add(1)
		c.recordHit(ctx)
		return actual.(*rego.PreparedEvalQuery), nil
	}
	c.misses.Add(1)
	c.recordMiss(ctx)
	return actual.(*rego.PreparedEvalQuery), nil
}

// ensureOTel lazily initialises the OTel Int64Counter handles. Errors
// are stored on the cache and silently swallowed thereafter — the
// in-process counters remain authoritative for tests, and a broken
// OTel pipeline must not break the eval engine.
func (c *Cache) ensureOTel() {
	c.metricsOnce.Do(func() {
		meter := otel.GetMeterProvider().Meter(meterName)
		hits, err := meter.Int64Counter(
			metricHits,
			metric.WithDescription("Prepared-query cache hits"),
		)
		if err != nil {
			c.otelErr = err
			return
		}
		misses, err := meter.Int64Counter(
			metricMisses,
			metric.WithDescription("Prepared-query cache misses"),
		)
		if err != nil {
			c.otelErr = err
			return
		}
		c.otelHits = hits
		c.otelMisses = misses
	})
}

func (c *Cache) recordHit(ctx context.Context) {
	if c.otelHits != nil {
		c.otelHits.Add(ctx, 1)
	}
}

func (c *Cache) recordMiss(ctx context.Context) {
	if c.otelMisses != nil {
		c.otelMisses.Add(ctx, 1)
	}
}

// keyFor returns the cache key for the (policy, capabilities, query,
// moduleName) tuple. SHA-256(policy) || SHA-256(sorted builtin names)
// || query || moduleName. Returning a string is intentional — sync.Map
// keys must be comparable and a string composes cleanly.
func keyFor(policy string, caps *ast.Capabilities, query string, moduleName string) string {
	policyHash := sha256.Sum256([]byte(policy))
	capsFP := capabilitiesFingerprint(caps)
	// The query + moduleName are short fixed strings; including them in
	// the key (rather than hashing) keeps the key human-readable when
	// pretty-printed for debugging.
	return hex.EncodeToString(policyHash[:]) + ":" + capsFP + ":" + query + ":" + moduleName
}

// capabilitiesFingerprint returns SHA-256 of the sorted list of builtin
// names in caps. A capability set with the same builtin names in
// different order returns the same fingerprint. nil caps return the
// fingerprint of the empty string (so a nil-caps caller is distinct from
// an all-caps caller).
func capabilitiesFingerprint(caps *ast.Capabilities) string {
	if caps == nil {
		h := sha256.Sum256(nil)
		return hex.EncodeToString(h[:])
	}
	names := make([]string, 0, len(caps.Builtins))
	for _, b := range caps.Builtins {
		if b == nil {
			continue
		}
		names = append(names, b.Name)
	}
	sort.Strings(names)
	h := sha256.New()
	for _, n := range names {
		h.Write([]byte(n))
		h.Write([]byte{0}) // NUL separator so "ab"+"c" doesn't collide with "a"+"bc"
	}
	sum := h.Sum(nil)
	return hex.EncodeToString(sum)
}
