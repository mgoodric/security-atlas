# 397 — Rename `SESSION_COOKIE` → `ATLAS_JWT_COOKIE` symbol (slice 328 M-3)

**Cluster:** Web
**Estimate:** 1h
**Type:** JUDGMENT
**Status:** `merged` (status reconciled 2026-06-03 — backlog drained per \_STATUS.md SoR; loop terminated batch 184)

## Narrative

Surfaced during slice 395, captured per continuous-batch policy.

Slice 328 audit finding **M-3** flagged that the BFF cookie constant is
named `SESSION_COOKIE` but resolves to the value `"atlas_jwt"` (since
slice 206); the self-acknowledging comment at `web/lib/auth.ts:10-14`
already documents the intended rename to `ATLAS_JWT_COOKIE`. The 328
decisions log (§D4) recommended bundling the rename into the slice-370
api-client work; slice 370 D2 deferred it to slice 395 (Phase 2 import
migration) as the Engineer's call.

**Why it is NOT bundled into slice 395:** the rename is a single-symbol
mechanical replacement (~370 bare `SESSION_COOKIE` occurrences across
~130 files), but two of those occurrences live in golden-tier contract
tests — `web/lib/contracts/me.contract.test.ts` and
`web/lib/contracts/demo-status.contract.test.ts` — both `import {
SESSION_COOKIE } from "@/lib/auth"`. An atomic symbol rename (so nothing
dangles / the build stays green) MUST touch those two files. Slice 395's
brief carries a hard rule: do NOT disturb `web/lib/contracts/`
(slice 349/392 golden tier). The rename therefore cannot be done
atomically inside 395 without violating that constraint, so it is split
out here where the golden-tier touch can be reviewed on its own merits.

This is purely a naming-consistency change — slice 328 M-3 itself notes
"the underlying behavior is correct, only the symbol name is
misleading." No wire/value change.

### What ships

1. Rename the exported constant `SESSION_COOKIE` → `ATLAS_JWT_COOKIE` in
   `web/lib/auth.ts` (value stays `"atlas_jwt"`; update the doc comment).
2. Update ALL references atomically (web app, BFF routes, components,
   e2e, scripts, AND the two golden-tier contract tests) so nothing
   dangles. Word-boundary-aware: do NOT touch `OIDC_SESSION_COOKIE`
   (38 occurrences; a distinct cookie — `"atlas_session"`).
3. Verify the old bare name is fully gone:
   `grep -rn '\bSESSION_COOKIE\b' web/ | grep -v OIDC_SESSION_COOKIE`
   returns nothing.

### JUDGMENT note (golden-tier touch)

The golden-tier no-touch convention is about the recorded contract
shapes (`*.golden.json`) and the assertion semantics, not the local
identifier used to set a cookie in the test harness. Renaming the
imported symbol in those two contract tests is a mechanical local edit
that does not change any asserted contract. Confirm with the maintainer
that this narrow touch is acceptable, or have the maintainer apply the
two contract-file edits.

## Acceptance criteria

- [ ] **AC-1.** `web/lib/auth.ts` exports `ATLAS_JWT_COOKIE`; no
      `SESSION_COOKIE` export remains. `OIDC_SESSION_COOKIE` unchanged.
- [ ] **AC-2.** Zero bare `SESSION_COOKIE` references remain (grep clean,
      excluding `OIDC_SESSION_COOKIE`).
- [ ] **AC-3.** `tsc --noEmit`, `npm run lint`, `npm run test`, and the
      Playwright e2e suite all pass. No wire/value change.

## Dependencies

- **#395** (import-site migration) — should merge first to keep the two
  diffs from colliding on the same files.

## Anti-criteria (P0 — block merge)

- **P0-397-1.** Does NOT change the cookie VALUE (`"atlas_jwt"`) or any
  wire behavior — symbol rename only.
- **P0-397-2.** Does NOT rename or otherwise alter `OIDC_SESSION_COOKIE`.
- **P0-397-3.** Does NOT auto-merge (golden-tier touch needs review).
