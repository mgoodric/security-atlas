# 396 — Retire the `web/lib/api.ts` barrel shim (slice 370 Phase 3)

**Cluster:** Web
**Estimate:** 0.5d
**Type:** AFK
**Status:** `in-review`

> **Implemented 2026-05-30.** Barrel `web/lib/api.ts` deleted; the
> `apiBaseURL` test moved to `web/lib/api/base.test.ts` (imports `./base`);
> coverage thresholds reconciled (removed the `lib/api.ts` floor + four
> phantom artifact floors — see decisions log D2). All gates green: tsc, lint,
> 1204 vitest, `next build` (106/106). Completes the slice-370 lineage
> (370 → 395 → 396). Decisions log:
> `docs/audit-log/396-web-api-barrel-retire-decisions.md`.

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

- [x] **AC-1.** `web/lib/api.ts` deleted; no broken imports (`next build` 106/106).
- [x] **AC-2.** `tsc --noEmit`, `npm run lint`, `npm run test` (1204/1204),
      `next build` all pass locally; Playwright e2e runs in CI.
- [x] **AC-3.** Coverage thresholds reconciled (barrel floor removed; 4 phantom
      artifact floors removed per P0-347-3 — see decisions log D2; no
      real-coverage regression).

## Dependencies

- **#370** (Phase 1) — merged.
- **#395** (Phase 2 import-site migration) — merged (`5f1e2169`). The last
  bare-`@/lib/api` importer is gone; this slice is no longer blocked.

## Anti-criteria (P0 — block merge)

- **P0-396-1.** Does NOT delete the barrel while any importer remains.
- **P0-396-2.** Does NOT auto-merge.
