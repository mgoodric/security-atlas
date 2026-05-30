# 381 — Perf cleanup round 1 (bundle of slice 332 Low findings)

**Cluster:** Performance
**Estimate:** 2d
**Type:** AFK (each sub-finding is mechanically verifiable)
**Status:** `ready`

## Narrative

Closes the bundle of LOW-severity findings from slice 332. Six
sub-findings spread across four surfaces; each individually is
< 0.5d but the per-slice overhead (issue + branch + PR + status
update) is roughly fixed. Bundling preserves per-finding granularity
in the slice doc while collapsing the ceremony cost.

### Sub-findings

| Code      | Surface       | Description                                                                            |
| --------- | ------------- | -------------------------------------------------------------------------------------- |
| F-ING-2   | Ingest        | Per-reject `writeAudit` opens an independent DB tx; bound the connect-attempt timeout  |
| F-UCF-2   | UCF           | `handlers.go` 932 LOC — split per-endpoint into separate files when 4th endpoint lands |
| F-OAUTH-2 | OAuth         | Cache `jose.Signer` per active KeyID on the `tokensign.Signer` struct                  |
| F-OAUTH-3 | OAuth         | Return immutable handle from `keystore.Get` (no per-call `make+copy`)                  |
| F-OTEL-2  | Observability | Document operator runbook section for `OTEL_TRACES_SAMPLER` tuning at high DB rate     |
| F-ING-3   | Ingest        | Add `canonjson.HashRecord` CPU-bounded benchmark to lock the 1 MiB ceiling             |

Each sub-finding has its own AC + P0 + commit in this slice. The
slice's overall purpose is the perf-cleanup cadence pattern: surface
the small wins from the audit in one bundle so they don't get lost.

### Why now

Slice 332 surfaced these as bounded-blast-radius wins. The bundle is
the right shape for "small, independent, low-risk" remediations.

### Trigger

Slice 332 performance audit Low findings + the one Informational
finding with a documentation ask (F-OTEL-2 operator runbook).

### Disposition

Multi-file refactor + one operator runbook addition.

## Threat model

Bundle of small refactors. Per-sub-finding STRIDE:

- **F-ING-2**: bounded timeout on best-effort audit tx — strictly an
  improvement to availability (no DoS amplification).
- **F-UCF-2**: file-organization only — no semantic change.
- **F-OAUTH-2**: cached jose.Signer per KeyID — same key material,
  same signing path; cache invalidation tied to keystore.Rotate
  (currently `ErrRotateUnsupported`, so cache is effectively
  process-lifetime).
- **F-OAUTH-3**: immutable verification-key handle — defensive copy
  is gone, but the verification keys themselves are pointers to
  immutable `*ecdsa.PublicKey` values that never change after
  load. Correctness preserved.
- **F-OTEL-2**: documentation only.
- **F-ING-3**: benchmark addition only.

No new threat surface. Constitutional invariants unchanged.

## Acceptance criteria

### F-ING-2

- [ ] **AC-1.** `writeAudit` tx opens with a 3-second context
      timeout (matching the slice-188 best-effort audit pattern).
- [ ] **AC-2.** Existing reject-path integration tests still
      observe the audit row.

### F-UCF-2

- [ ] **AC-3.** `internal/api/ucfcoverage/handlers.go` split into
      three files: `requirement_coverage.go`, `anchor_requirements.go`,
      `control_coverage.go`. Imports + tests still resolve.
- [ ] **AC-4.** No semantic change — diff is move-and-rename only.

### F-OAUTH-2

- [ ] **AC-5.** `*tokensign.Signer` caches the constructed
      `jose.Signer` per active KeyID in a `sync.Map`. Cache key is
      the KeyID string.
- [ ] **AC-6.** Cache invalidation on `keystore.Rotate` — when
      Rotate ships (currently `ErrRotateUnsupported`), the cache
      must be invalidated. For now: a `Reset()` method on
      `*Signer` that empties the cache, called by future Rotate.
- [ ] **AC-7.** Benchmark `BenchmarkSignerCachedVsUncached` shows
      cached path's ns/op is < 50% of uncached path.

### F-OAUTH-3

- [ ] **AC-8.** `keystore.fsstore.Store.Get` returns an
      immutable handle (no per-call `make+copy`). Implemented via
      `sync/atomic.Pointer[[]VerificationKey]` swapped on internal
      reload (currently zero reloads after `Open`, so the slice
      is essentially load-once).
- [ ] **AC-9.** Existing `internal/auth/keystore` integration
      tests pass.

### F-OTEL-2

- [ ] **AC-10.** `docs/operator/observability-tuning.md` (new file)
      documents the recipe for `OTEL_TRACES_SAMPLER=
parentbased_traceidratio` + `OTEL_TRACES_SAMPLER_ARG=0.1`
      under "High DB query rate" header.
- [ ] **AC-11.** `docs/SELF_HOSTING.md` cross-links the new
      operator guide.

### F-ING-3

- [ ] **AC-12.** `internal/canonjson` benchmark
      `BenchmarkHashRecordAtMaxPayload` asserts a 1 MiB payload
      hashes in < 5 ms wall-clock on the CI runner. Locks the
      1 MiB ceiling's CPU cost as a regression gate.

### Cross-cutting

- [ ] **AC-13.** `pre-commit run --files` passes.

## Anti-criteria (P0)

- **P0-1.** Each sub-finding lands as its OWN commit on the branch
  (so a reviewer can revert one without losing the others). Single
  squash-merge to main is fine.
- **P0-2.** Does NOT widen scope to v2 atlas-edge tenancy or any
  other Audit findings — Critical/High findings have their own
  slices (377/378), Mediums have theirs (379/380).
- **P0-3.** Does NOT introduce a new caching framework — `sync.Map`
  is the primitive for F-OAUTH-2.
- **P0-4.** Does NOT modify the OTel sampler default — F-OTEL-2 is
  documentation-only.
- **P0-5.** Does NOT auto-merge.

## Dependencies

- **#332** (performance audit) — `merged`. Source finding.
- **#013** (evidence push API) — `merged`. F-ING-2.
- **#008** (UCF API) — `merged`. F-UCF-2.
- **#187** (auth substrate ES256) — `merged`. F-OAUTH-2, F-OAUTH-3.
- **#121** (OTel SDK) — `merged`. F-OTEL-2.

## Notes for the implementing agent

1. **Order of operations**: do F-UCF-2 (file split) FIRST — it's
   mechanically the simplest. Then the keystore/Signer caches, then
   the docs.
2. The bundle is intentionally low-risk. Reviewer should be able to
   approve in one pass.
3. The F-OTEL-2 operator runbook is the most valuable per-line
   deliverable — operators tuning OTel sampler ratios is a real
   operational concern and the platform should have a documented
   recipe.
4. F-ING-3's benchmark is a regression gate, NOT a one-shot
   measurement — wire it into the existing `internal/canonjson`
   test directory so a future PR that accidentally adds an
   O(payload²) operation surfaces the regression.
