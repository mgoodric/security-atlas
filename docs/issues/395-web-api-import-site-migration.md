# 395 — Migrate `@/lib/api` import sites to per-domain paths (slice 370 Phase 2)

**Cluster:** Web
**Estimate:** 1d
**Type:** JUDGMENT
**Status:** `merged` (status reconciled 2026-06-03 — backlog drained per \_STATUS.md SoR; loop terminated batch 184)

## Narrative

Surfaced during slice 370, captured per continuous-batch policy. Slice
370 (Phase 1) split the `web/lib/api.ts` god-file into per-domain modules
under `web/lib/api/` and left a backward-compat barrel at
`web/lib/api.ts` (`export *` from each domain file) so the 176 existing
`@/lib/api` import sites kept resolving unchanged — zero import-site
churn in the structural-split PR.

This is **Phase 2** (slice 370 AC-3): mechanically migrate each
`import { foo } from "@/lib/api"` to the specific submodule path
(`import { foo } from "@/lib/api/anchors"`, etc.) across `web/app/`,
`web/components/`, and `web/lib/`. The barrel stays in place after this
PR (its deletion is Phase 3, slice 396).

### What ships

1. All 176 import sites repointed from `@/lib/api` to the per-domain
   path that exports each symbol. Use TypeScript's "Update Imports On
   File Move" or a `tsmod`/`jscodeshift` codemod; the diff is large but
   mechanical.
2. The split-mapping table in
   `docs/audit-log/370-web-api-client-split-decisions.md` is the
   authoritative symbol → file map.
3. **JUDGMENT (slice 370 D2):** bundle the M-3 `SESSION_COOKIE →
ATLAS_JWT_COOKIE` rename here — both are TS-side mechanical renames
   and the import sites are open anyway. Engineer's call; record in this
   slice's decisions log.

## Acceptance criteria

- [ ] **AC-1.** Zero remaining `from "@/lib/api"` imports (the barrel is
      no longer imported by any app/component/lib file). `grep` clean.
- [ ] **AC-2.** `tsc --noEmit`, `npm run lint`, `npm run test`, and the
      Playwright e2e suite all pass.
- [ ] **AC-3.** No wire change — pure import-path relocation.

## Dependencies

- **#370** (Phase 1 split + barrel) — must merge first.

## Anti-criteria (P0 — block merge)

- **P0-395-1.** Does NOT change any function signature or wire behavior.
- **P0-395-2.** Does NOT delete the barrel (that is Phase 3 / slice 396).
- **P0-395-3.** Does NOT auto-merge.
