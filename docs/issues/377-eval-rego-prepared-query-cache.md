# 377 — Cache `rego.PreparedEvalQuery` in eval engine (close slice 332 F-OPA-1)

**Cluster:** Performance
**Estimate:** 1d
**Type:** AFK (mechanically verifiable — a benchmark + a unit test)
**Status:** `ready`

## Narrative

Closes slice 332 finding **F-OPA-1 (CRITICAL)**. The eval engine's
`internal/eval/rego.go evalRegoQuery` calls `rego.New(...).
PrepareForEval(ctx)` on every invocation. `evalRegoQuery` is called
from `internal/eval/engine.go:159` inside `computeRow`, which runs
once per `(control × cell)` per `EvaluateAll`. A tenant with 200
active controls and modest cell fan-out pays 200+ OPA compiles per
scheduled-eval tick.

A second site of the same anti-pattern exists in
`internal/risk/aggrule/severity.go:134` (custom-rego severity policy).

The correct pattern already exists in the codebase:
`internal/authz/decision.go:60-65 NewEngine` calls `PrepareForEval`
ONCE and stores the resulting `rego.PreparedEvalQuery` on the
`Engine` struct, re-using it for every `Decide`.

This slice introduces a shared cache (`internal/eval/regocache/`) that
both `internal/eval/rego.go` and `internal/risk/aggrule/severity.go`
use to compile-once-per-policy. The cache key is the SHA-256 of the
policy text + capability fingerprint; the cache is bounded by the
active control count (~200 at v1 scale) so unbounded growth is not
a concern.

### Why now

Slice 332's performance audit identified this as the single
most-impactful finding (decisions log §D8). The remediation is
mechanical: a `sync.Map` keyed on policy hash + a thin wrapper
around `rego.PrepareForEval`. The savings — ~600 compiles per
EvaluateAll tick eliminated — make every scheduled-eval cycle
faster by hundreds of milliseconds.

### Trigger

Slice 332 performance audit, surface 3 (OPA evaluation),
finding F-OPA-1.

### Disposition

Code change: net new `internal/eval/regocache/` package + wire-in
at two call sites + integration test asserting prepare-once
behavior + benchmark validating the speedup.

## Threat model

Caching surface. STRIDE pass:

- **S:** No auth surface change. CLEAN.
- **T:** The cache value is `rego.PreparedEvalQuery` (an
  in-process Go object); no on-disk persistence; no cross-tenant
  cache key (the cache key is policy-text-derived, not tenant-id-
  derived). One tenant authoring a policy that compiles to the same
  AST as another tenant's policy gets a cache hit — by construction
  there is no tenant data leak because the cache stores compiled
  AST, not evaluation results.
- **R:** Cache hits/misses surface as OTel counter metrics for
  forensic observability.
- **I:** No information disclosure — cache stores compiled AST only,
  no inputs.
- **D:** Bounded growth — cache is bounded by active control count.
  An LRU eviction policy bounds worst-case memory at N entries.
- **E:** Dev-level access.

**Constitutional invariants honored**:

- **Ingestion and evaluation are separated stages (invariant #2).**
  The cache lives in the evaluation stage; ingestion never reads it.
- **Tenant isolation at the database layer (invariant #6).** The
  cache is policy-text-keyed, not tenant-keyed; tenant isolation is
  preserved by the policy text itself being tenant-scoped or global.

## Acceptance criteria

- [ ] **AC-1.** New `internal/eval/regocache/` package exposes a
      `Get(ctx, policy string, opts ...Option) (rego.PreparedEvalQuery, error)`
      function that prepares-once-per-policy-text and re-uses on
      subsequent calls with the same text.
- [ ] **AC-2.** Cache key is the SHA-256 of the policy text
      concatenated with the capability-set fingerprint (so a
      capability-set change invalidates the entry).
- [ ] **AC-3.** `internal/eval/rego.go evalRegoQuery` wires through
      the cache.
- [ ] **AC-4.** `internal/risk/aggrule/severity.go evalCustomRego`
      wires through the cache.
- [ ] **AC-5.** Benchmark
      `BenchmarkEvalRegoQueryRepeatedCompile` asserts the
      second-and-subsequent calls run in < 10% of the first call's
      duration.
- [ ] **AC-6.** Unit test
      `TestRegoCacheDistinctPoliciesGetDistinctCacheEntries`
      asserts policy A and policy B with different text get
      different cache entries.
- [ ] **AC-7.** Unit test `TestRegoCacheCapabilitySetIsInKey`
      asserts that the same policy text with different capability
      sets gets different cache entries.
- [ ] **AC-8.** OTel counter metric
      `atlas_eval_regocache_hits_total` + `atlas_eval_regocache_misses_total`
      emitted on every cache lookup.
- [ ] **AC-9.** No regression to existing
      `internal/eval` or `internal/risk/aggrule` integration tests.
- [ ] **AC-10.** `pre-commit run --files` passes.

## Anti-criteria (P0)

- **P0-1.** Does NOT change the public `evalRegoQuery` or
  `evalCustomRego` signatures (call sites stay unchanged except for
  the internal cache lookup).
- **P0-2.** Does NOT introduce unbounded cache growth — LRU
  eviction policy with a max-entries cap.
- **P0-3.** Does NOT bypass the sandbox capability restriction —
  the cached prepared-query MUST carry the same restricted
  capability set the un-cached version did.
- **P0-4.** Does NOT widen the cache key to include tenant_id — the
  policy text IS the canonical key; cross-tenant cache hits on
  identical policy text are correct (and a feature, not a bug).
- **P0-5.** Does NOT auto-merge.

## Dependencies

- **#332** (performance audit) — `merged`. Source finding.
- **#012** (control state evaluation engine) — `merged`. Owner of
  `internal/eval/rego.go`.
- **#020** (risk severity aggregation) — `merged`. Owner of
  `internal/risk/aggrule/severity.go`.
- **#054** (rego sandbox capability strip) — `merged`. The
  capability-set fingerprint MUST include the slice-054 deny list
  hash.

## Notes for the implementing agent

1. Use `sync.Map` over RWMutex+map — the access pattern is
   read-mostly-after-write-once and `sync.Map.LoadOrStore` is
   exactly that primitive.
2. The capability-set fingerprint should be the SHA-256 of the
   sorted builtin-name list. Capability sets are stable per process
   lifetime, so the fingerprint can be computed once at package
   init.
3. OPA's `PrepareForEval` is goroutine-safe to call on a fresh
   `rego.Rego` instance, but the resulting `PreparedEvalQuery` is
   ALSO goroutine-safe to Eval concurrently. The cache stores
   `PreparedEvalQuery` values directly; no per-entry lock needed.
4. The benchmark should compile policy A once, then `b.N` Evals
   against the prepared query, and compare against a baseline that
   re-Prepares on every iteration.
5. Don't widen to include an `internal/api/oauth` cache — the OAuth
   path uses `tokensign`, not Rego, and is on a different perf
   surface (slice 332 F-OAUTH-2/3 → slice 381).
