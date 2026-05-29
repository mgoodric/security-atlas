# Slice 381 — Decisions log

**Slice:** `docs/issues/381-perf-cleanup-round-1.md`
**Type:** JUDGMENT (bundle of mechanically-verifiable AFK sub-findings)
**Closes:** the slice 332 performance-audit LOW-finding bundle (6 sub-findings)
**Author:** Claude (Engineer agent), batch 163

This slice bundles six bounded-blast-radius findings from the slice 332
audit. Each sub-finding lands as its own commit (P0-1). The work is
intentionally low-risk; the judgment calls below are where the slice doc
or the audit left a decision to the implementer.

---

## Decisions made

### D1 — F-UCF-2: split the handler file NOW (per AC-3), not "when the 4th endpoint lands"

**Tension.** The slice 332 audit's F-UCF-2 remediation says "Split
per-endpoint into separate files **when the fourth endpoint lands**." The
slice 381 doc's AC-3 instead directs a concrete, do-now split into three
named files (`requirement_coverage.go`, `anchor_requirements.go`,
`control_coverage.go`).

**Decision.** Honor the slice doc — split now. AC-3 and AC-4 are concrete
acceptance criteria with named target files; the slice author resolved the
audit's softer "when 4th lands" framing into a do-now disposition. The
split is pure move-and-rename (AC-4) with zero semantic change, so the
risk of doing it now (vs. deferring) is essentially nil, and it removes
the review-cycle cost the audit flagged immediately rather than carrying
the 932-LOC file until some future endpoint.

**What stayed in `handlers.go`.** Only genuinely cross-endpoint
scaffolding: the `Handler` struct, `New`/`AttachCoverage`,
`RegisterRoutes`, the shared `inTenantTx` / `pgUUIDFromTenantCtx` /
`lookupRequirement` / `resolveFrameworkVersion` helpers, the four wire
types, and the `uuidStr` / `writeJSON` / `writeError` / `writeServerErr`
helpers. Each endpoint's own helpers moved with the endpoint.

### D2 — F-OAUTH-2: AC-7's "< 50% of uncached" bar is unreachable on the full Sign; benchmark the acquisition step instead

**Problem.** AC-7 asks for `BenchmarkSignerCachedVsUncached` to show
"cached path's ns/op is < 50% of uncached path." Measured against the full
`Sign` call, cached and uncached are nearly identical (~21µs both),
because the dominant cost is the ES256 P-256 scalar multiplication inside
`jws.Sign` — which both paths pay identically. The cache only removes the
`jose.NewSigner` construction, measured in isolation at ~235 ns/op (10
allocs) — roughly **1%** of the full Sign. There is no way to make the
full-Sign total drop by 50% by caching a 1%-of-total construction step.

**Decision.** Reframe the AC-7 benchmark to isolate the operation the
cache actually optimizes: the `jose.Signer` **acquisition** step. The
in-package `BenchmarkSignerCachedVsUncached` (in
`cache_internal_test.go`, which can reach the unexported `cachedSigner`)
compares a `sync.Map` cache hit (8.9 ns/op, 0 allocs) against
`jose.NewSigner` per call (235 ns/op, 10 allocs). Cached is ~3.8% of
uncached — comfortably under the < 50% intent of AC-7, and a _meaningful_
regression gate: dropping the cache (reverting to per-call construction)
shows up immediately as a ~10× acquisition-cost blowup. This honors the
spirit of AC-7 (prove the cache pays for itself) while being honest about
the physics. The correctness side (cache reuse, per-KeyID isolation,
`Reset()` invalidation) is covered by external-package tests in
`cache_test.go`.

This matches the audit's own baseline ("ES256 sign is sub-millisecond"
and the finding flags only the `NewSigner` allocation as the marginal
cost) — the audit never claimed the construction was a large fraction of
the sign; it's a steady-state allocation win, not a latency win.

### D3 — F-OTEL-2: the audit's default-sampler premise was stale; the runbook documents the actual default

**Finding premise.** F-OTEL-2 states the SDK default sampler is
`parentbased_always_on` (citing slice 121 D-Sampling-1) and recommends
operators tune down to `parentbased_traceidratio` at 0.1.

**Verified reality.** `internal/observability/otel/otel.go` defaults
`OTEL_TRACES_SAMPLER` to `parentbased_traceidratio` and
`OTEL_TRACES_SAMPLER_ARG` to `0.1` when unset; `docs/observability.md` and
the `otel` coverage test both confirm the 10%-ratio default. The shipped
default is the ratio sampler, **not** always-on.

**Decision.** Write the runbook around the true default — atlas is sampled
at 10% out of the box, and the recipe is for pushing the ratio _lower_
under exceptional DB load (or pinning it explicitly). The runbook
includes a short note recording that the audit's "always_on" premise was a
stale reading, so a future reader cross-referencing the audit isn't
confused. Documentation-only; the sampler default is untouched (P0-4).

### D4 — F-OAUTH-3: immutable-snapshot via atomic.Pointer, not a frozen-copy-on-write of the existing fields

**Options.**

1. Keep the RWMutex + verify slice, but return the _same_ backing slice
   from `Get` without copying (relying on callers never mutating it).
2. Replace the mutable fields with an immutable `snapshot` struct
   published via `sync/atomic.Pointer`, swapped wholesale on
   load/generate/Rotate/Prune.

**Decision.** Option 2. It makes the immutability a structural property
(the published slice is never mutated after `Store`, by construction)
rather than a convention a future caller could violate, gives `Get` a
lock-free pointer load, and keeps the signing key + verify set internally
consistent (no torn read where a rotate updates one field but not the
other). Mutators serialise on a dedicated `loadMu`; readers (`Get`,
`List`) never take a lock. Confirmed no caller (`serveJWKS`, tokensign
`Verify`) mutates the returned slice, and `-race` is clean on the
existing Rotate-during-Verify integration test. The slice-366
rotation/overlap/prune contract and its tests are preserved unchanged.

### D5 — F-ING-3: the wall-clock gate is a PATHOLOGICAL-regression ceiling (100ms), measured median-of-9, not a microsecond budget

**Defect found post-push.** The first cut of
`TestHashRecord_AtMaxPayloadUnderBudget` asserted a best-of-5 single
sample against a 5ms hard ceiling. On the M3 dev box the 1 MiB hash runs
~0.8ms, but on a shared CI runner under load it runs ~5-7ms — so the 5ms
ceiling sat **on top of** the observed cost and flaked (`Go · build +
test` failed on PR #870 at 5.71ms vs 5ms). That is a flake generator: it
would intermittently block unrelated PRs without any real regression. The
hashing logic was not changed by this slice — this was purely a
calibration error.

**Root cause.** I conflated two different questions. AC-12's intent is
"did `HashRecord` become **pathologically** slow at the 1 MiB ceiling?"
(an accidental O(payload²) re-canonicalisation, or a per-byte allocation
blowup). That is an **order-of-magnitude** question. A 5ms ceiling instead
tried to assert a precise microsecond budget, which is inherently noisy on
shared hardware and answers the wrong question.

**Decision.**

1. **Ceiling = 100ms** (`maxPayloadHashBudget`). A non-pathological 1 MiB
   hash costs ~1-7ms across runners (linear: one SHA-256 pass + one
   deterministic proto marshal). A pathological superlinear regression at
   1 MiB would cost **hundreds of ms to seconds** — two-to-three orders of
   magnitude higher. 100ms sits in the dead zone: ~14-125× above any
   realistic CI sample (runner noise can never reach it) yet far below any
   superlinear pathology (a real regression always trips it). A 1ms bump
   (5→6) was explicitly rejected — it stays in the noise band and would
   still flake.
2. **Measurement = median-of-9 with a discarded warmup**
   (`medianHashDuration`). The median is robust in **both** directions: a
   single slow scheduler hiccup can't inflate it (unlike a mean), and a
   single fast outlier can't mask a genuine regression (unlike the
   original best-of-N). N is odd so the median is a real sample.
3. **The `testing.B` benchmark stays the precise-number artifact.**
   `BenchmarkHashRecordAtMaxPayload` re-publishes a steady ~441µs/op (14
   allocs) for trend-watching; its self-assert uses the SAME generous
   100ms pathological ceiling, so a `-bench`-only run still catches an
   order-of-magnitude regression but never flakes.

**Validation.** Stable across `-count=20`, `-race -count=5`, and
`-cpu=1 -count=10` contention runs; verbose median-of-9 logs ~791µs
against the 100ms ceiling (~126× headroom). No production code changed —
the diff is confined to `internal/canonjson/canonjson_test.go`, so patch
coverage is unaffected (the changed lines are themselves test code).

---

## Anti-criteria honored

- **P0-1** — six independent commits on the branch (one per sub-finding).
- **P0-2** — scope held to the six bundled Low/Informational findings; no
  v2 atlas-edge tenancy or other-severity findings touched.
- **P0-3** — F-OAUTH-2 uses `sync.Map`, no new caching framework.
- **P0-4** — F-OTEL-2 is documentation-only; the OTel sampler default is
  unchanged.
- **P0-5** — no auto-merge.

## Constitutional invariants

Unchanged. F-ING-2 preserves the slice-015 ingest invariant order
(size-check → schema-validate → redact → hash → write); the reject-audit
tx is off that path. No new threat surface (see slice doc threat model).

## STATUS row

Left to the orchestrator — `docs/issues/_STATUS.md` is not touched on this
branch (slice 382 CI guard).
