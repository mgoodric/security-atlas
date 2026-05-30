# Slice 396 — decisions log (web/lib/api.ts barrel retire, slice 370 Phase 3)

## Decisions made

- **D1: Delete the barrel; move the `apiBaseURL` test to its real home.**
  `web/lib/api.ts` was a pure `export *` shim over the 18 per-domain modules
  (slice 370 Phase 1). Slice 395 migrated all 177 import sites to the
  per-domain paths, so the barrel had zero importers. Deleted it; `git mv`'d
  `web/lib/api.test.ts` → `web/lib/api/base.test.ts` and repointed its import
  from `./api` (barrel) to `./base` (where `apiBaseURL` actually lives).
  Confidence: high. Verified by a full `next build` (106/106 pages) + tsc +
  1204 vitest tests + eslint.

- **D2: Removed FOUR phantom coverage floors, not just the barrel's — and
  this is NOT a slice-369-style floor-lowering.** This is the load-bearing
  judgment of the slice. Deleting the barrel tripped the slice-347 ratchet on
  `lib/api/{board,evidence,exceptions,risk-hierarchy}.ts` (floors 17/1/2/8%,
  all `functions:0`). Root cause: the barrel was imported by exactly one test
  (the `apiBaseURL` URL-resolution test); its `export *` transitively
  module-LOADED all 18 domain modules, crediting them with module-load
  coverage. slice-347 seeded floors from that measured number, capturing
  artifact coverage as floors. Those 4 modules have **zero direct test
  importers** and `functions:0` — no test ever called their code. With the
  barrel gone they measure their true 0%, so the floors were measurement
  noise, not real coverage. Per P0-347-3 ("the ratchet starts at truth, not
  aspiration" — 0%-measuring files are omitted from the map, exactly like the
  74 already-omitted files including the sibling attest/framework-scopes/me/
  vendors modules), the correct reconciliation is to OMIT these 4, not to
  invent cargo-cult tests to defend artifact numbers. This is the opposite of
  slice 369's authzmw case (there, real covered lines were removed from a
  sub-100% package and the fix was to ADD a test; here, no real coverage ever
  existed). Documented the distinction inline in
  coverage-thresholds.json's `$omitted_zero_pct_rationale`. Confidence: high.

- **D3: Kept the eslint `max-lines:600` rule.** The 396 doc loosely said
  "remove the eslint max-lines exception", but the rule is NOT a
  barrel-specific exception — it is the slice-370 AC-5 guard on
  `lib/api/**/*.ts` that prevents the god-file from re-accreting. It has
  nothing to do with the barrel file and removing it would un-guard the whole
  refactor. Left intact. Confidence: high.

- **D4: base.ts floor (98) unchanged.** `apiBaseURL`'s test now targets
  `base.ts` directly instead of via the barrel; base.ts was already at floor
  98 (the barrel's coverage was really base.ts's `apiBaseURL` resolution), so
  no floor moved for it.

## Revisit once in use

- Real per-function coverage for the 4 omitted fetch-wrapper modules
  (board/evidence/exceptions/risk-hierarchy) is the job of the slice-349/392
  golden-file contract-tier rollout, not this slice. When a contract test or
  unit test first calls into one of them, it re-enters the threshold map at
  floor = floor(measured − 2pp) per the standard ratchet.

## Confidence

High. The 370 web-refactor lineage (Phase 1 → 2 → 3: 370 → 395 → 396) is now
complete; the barrel is gone and the per-domain modules stand on their own.
