# Slice 377 — Cache `rego.PreparedEvalQuery` in eval engine (decisions log)

Closes slice 332 audit finding **F-OPA-1 (CRITICAL)**.

Baseline:
[`internal/authz/decision.go:60`](../../internal/authz/decision.go) already
prepares-once-and-caches; this slice generalises that pattern to the two
hot-path call sites that re-prepared per call:

- `internal/eval/rego.go evalRegoQuery` (slice 332 audit headline)
- `internal/risk/aggrule/severity.go evalCustomRego` (second site,
  surfaced in the audit's F-OPA-1 narrative)

The new `internal/eval/regocache` package is the shared substrate.

## D1 — Cache capacity policy: unbounded `sync.Map`, bounded-in-practice

**Decision:** Ship as an unbounded `sync.Map`-backed cache for v1. No
LRU eviction.

**Rationale.** The policy population in v1 is bounded by the control
catalog (~200 controls per tenant) plus the small custom-severity-rule
population (single-digit per tenant in practice). The cache entries
live in-process for the binary's lifetime, but the working set is
~hundreds of entries × O(small) bytes-per-entry for a compiled OPA AST.
That fits comfortably in process memory at v1 scale.

LRU is a documented v2 add-on if and when:

- custom-control authoring lands (canvas §4.4) — tenants may then
  author arbitrarily many distinct policies; OR
- atlas-edge tenant-isolation work (slice 332 F-OPA-2 / slice 378) —
  per-tenant policy populations multiplied by tenant count could
  unbound the working set.

The shape of the cache type (`Cache.GetOrPrepare` returning a single
`*rego.PreparedEvalQuery`) is intentionally compatible with a future
LRU-shaped concrete impl: callers stay the same; only `regocache.New()`
changes to `regocache.NewLRU(maxEntries)`.

**Alternatives rejected:**

- Bounded `sync.Map` with manual TTL eviction — adds a janitor
  goroutine to a foundational substrate, complicates the
  reasoning-about-correctness for marginal v1 benefit.
- Per-call `golang.org/x/sync/singleflight` — solves a different
  problem (duplicate work suppression) without solving the steady-state
  cache-hit problem.

## D2 — `risk/aggrule` is in scope

**Decision:** Apply the same cache fix to
`internal/risk/aggrule/severity.go evalCustomRego` in this slice. Each
package gets its own package-level `defaultRegoCache` instance.

**Rationale.** The audit's F-OPA-1 narrative explicitly names this as
"a second site of the wrong pattern" (audit lines 328-330). The slice
377 brief lists it as the second deliverable. The fix is identical in
shape — drop in the cache, move input from prepare-time to eval-time.

**Alternatives rejected:**

- "Defer to a separate slice" — the audit lumped them under one
  finding; splitting them would leave the finding partially open and
  fragment the close-out trail.
- "Share one cache between eval and aggrule" — see D4.

## D3 — OTel counter shape: `atlas_eval_regocache_hits_total` / `_misses_total`

**Decision:** Two `Int64Counter` instruments on the
`github.com/mgoodric/security-atlas/internal/eval/regocache` meter:

- `atlas_eval_regocache_hits_total` — incremented on every cache hit
  (including the race-loss path where `LoadOrStore` returns the
  existing value).
- `atlas_eval_regocache_misses_total` — incremented on every cache
  miss (including failed-compile misses — a repeatedly-failing compile
  still costs CPU, which is the signal an operator wants to see).

The meter is initialised lazily via `sync.Once` on first GetOrPrepare
so the global MeterProvider has been wired by `slice 121 otel.Init`
before the counter handles are created. OTel handle creation failures
are stored on the cache and swallowed — the in-process counters remain
authoritative for tests, and a broken OTel pipeline must not break the
eval engine.

**Rationale.** Matches the slice 121 naming convention (`atlas_*_total`).
The package-level scope name follows the standard Go import path
convention so operators can pivot dashboards on
`otel_scope_name="github.com/mgoodric/security-atlas/internal/eval/regocache"`.
The dual hits/misses pair is the minimum signal needed to compute
cache hit ratio downstream (Grafana: `rate(hits) / (rate(hits) + rate(misses))`).

**Alternatives rejected:**

- Single counter with a `result="hit"|"miss"` label — OTel best
  practice (and the slice 121 baseline) is one counter per event class;
  labels are for partitioning, not for switching events.
- Histogram of compile-duration — useful for diagnosing slow OPA
  compiles, but orthogonal to the cache-hit-ratio question this slice
  is built to answer. Filed as a future-work note in the package doc.

## D4 — Per-package `defaultRegoCache` (NOT cross-package shared)

**Decision:** `internal/eval` and `internal/risk/aggrule` each
declare their own package-level `var defaultRegoCache = regocache.New()`.
The shared substrate is the `regocache.Cache` type; the instances are
distinct.

**Rationale.** The two call sites operate on disjoint policy
populations: `internal/eval` compiles evidence-query policies (`package
evidence.query`); `internal/risk/aggrule` compiles custom-severity
policies (`package aggrule.severity`). A cross-package cache key would
include the policy text itself, so logically there would be no
collisions — but separating the caches preserves package-locality of
reasoning and OTel emission (each cache reports its own hit/miss
counts under its own meter scope when wired explicitly per-package).

Note: in this implementation BOTH instances emit under the meter scope
`internal/eval/regocache` because the meter is constructed inside the
shared `Cache` type. That is acceptable for v1; if the operator dashboard
needs to distinguish the two populations, a future enhancement could
plumb an instance-name option through `New(WithName("eval"))` to label
the counter emissions. Filed as a non-blocking follow-up.

**Alternatives rejected:**

- Single package-level cache in `internal/eval/regocache` itself,
  re-used by both call sites — saves three lines of code but ties the
  cache lifetime to a single package's `init` and obscures which call
  site is responsible for which entries.

## D5 — Benchmark numbers (before vs after)

**Hardware:** Apple M3 Ultra (`darwin/arm64`), `go test -benchtime=2s`.
**Policy under test:** representative 7-line evidence-query Rego policy
(`every r in input.records { r.result == "pass" }`).
**Records per call:** 3 in-window pass records.

| Benchmark                                        | ns/op       | B/op        | allocs/op  |
| ------------------------------------------------ | ----------- | ----------- | ---------- |
| `BenchmarkEvalRegoQueryRepeatedCompile`          | **31,951**  | **21,349**  | **273**    |
| `BenchmarkEvalRegoQueryRepeatedCompile_Uncached` | **225,498** | **200,168** | **4,207**  |
| **Delta (cached/uncached)**                      | **7.06×**   | **9.38×**   | **15.41×** |

**Per-call savings:** 225.5 µs → 31.95 µs — a **193.5 µs/op CPU win**.

**Per-EvaluateAll-tick savings** (200 active controls × ~3 scope cells
per control = ~600 evalRegoQuery calls per tick):

- Pre-377: ~600 × 225.5 µs = **~135 ms of pure CPU per tick**
- Post-377: ~600 × 32 µs = **~19 ms per tick**
- **Net savings: ~116 ms per scheduled-eval tick (~86% reduction)**

This matches the audit's expected magnitude — audit line 341 estimated
"600 ms–3 s of pure CPU spent in the planner" per tick; the measured
135 ms baseline is lower (the audit was order-of-magnitude not
measured), and the 116 ms savings vindicates F-OPA-1 as the
highest-value perf bug in v1.

Memory: a hot tick allocated ~120 MB of throwaway compiler state under
the pre-377 path (600 × 200 KB); the cached path allocates ~13 MB
amortised — the GC pressure reduction is its own win.

## Out-of-scope (intentional)

- `internal/authz/decision.go` is NOT modified — it already prepares
  once at `NewEngine` and is the reference pattern this slice
  generalises (slice 377 P0-4).
- LRU eviction policy — deferred per D1.
- Per-cache OTel name labelling — deferred per D4 note.
- OPA-compile histogram — orthogonal to cache-hit-ratio; deferred per D3.
- `internal/api/oauth` token signing perf is on a different surface
  (`tokensign`, not Rego) and goes through slice 381.
