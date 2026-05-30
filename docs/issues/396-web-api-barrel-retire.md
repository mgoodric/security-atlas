# 396 — Retire the `web/lib/api.ts` barrel shim (slice 370 Phase 3)

**Cluster:** Web
**Estimate:** 0.5d
**Type:** AFK
**Status:** `ready`

> **Unblocked 2026-05-30.** Dep #395 (Phase 2) is merged and the
> precondition is verified: `grep -rnE "['\"]@/lib/api['\"]" web/` (excluding
> the per-domain `@/lib/api/<domain>` paths) returns ZERO hits — no module
> imports the bare barrel anymore. The sole remaining match was a stale
> illustrative comment in `web/vitest.config.ts:63`, corrected in the same
> change that flipped this status. Slice 396 may now proceed to delete the
> barrel.

## Narrative

Surfaced during slice 370, captured per continuous-batch policy. Slice
370 (Phase 1) left a backward-compat barrel at `web/lib/api.ts`
(`export *` from each per-domain module). Slice 395 (Phase 2) repoints
every import site off the barrel onto the per-domain paths.

This is **Phase 3** (slice 370 AC-2): once Phase 2 has removed the last
`@/lib/api` importer, delete the barrel. The barrel is a transition aid,
not a permanent layer (slice 370 P0-370-3).

### What ships

1. Delete `web/lib/api.ts`.
2. Move `web/lib/api.test.ts` (the `apiBaseURL` URL-resolution test) to
   import from `@/lib/api/base` directly, and rename to
   `web/lib/api/base.test.ts` if appropriate.
3. Update the slice-347 `coverage-thresholds.json`: remove the
   `lib/api.ts` floor (the barrel no longer exists) and re-seed
   `lib/api/base.ts` from the test's measured coverage. No floor lowered.
4. Sanity: `grep -r 'from "@/lib/api"'` returns zero hits before delete.

## Acceptance criteria

- [ ] **AC-1.** `web/lib/api.ts` deleted; no broken imports.
- [ ] **AC-2.** `tsc --noEmit`, `npm run lint`, `npm run test`, Playwright
      e2e all pass.
- [ ] **AC-3.** Coverage thresholds reconciled (barrel floor removed; no
      aggregate regression).

## Dependencies

- **#370** (Phase 1) — merged.
- **#395** (Phase 2 import-site migration) — merged (`5f1e2169`). The last
  bare-`@/lib/api` importer is gone; this slice is no longer blocked.

## Anti-criteria (P0 — block merge)

- **P0-396-1.** Does NOT delete the barrel while any importer remains.
- **P0-396-2.** Does NOT auto-merge.
