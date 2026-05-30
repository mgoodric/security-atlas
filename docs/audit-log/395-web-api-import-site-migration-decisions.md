# Slice 395 — `@/lib/api` import-site migration: build-time decisions log

JUDGMENT slice (slice 370 Phase 2). This log records the build-time
calls so the codemod is reviewable. Migrates the 176 `@/lib/api` (barrel)
import sites to the per-domain `@/lib/api/<domain>` paths the barrel
re-exports, so the barrel can be retired in Phase 3 (slice 396). The
barrel itself is untouched and still works (P0-395-2 honored).

## D1 — Symbol→module map derived from source, not hand-transcribed

The 370 decisions log carries the authoritative 219-export split table.
Rather than hand-transcribe it (error-prone over 219 symbols), the
codemod parsed the actual `export` statements out of the 18
barrel-re-exported modules (`base`, `anchors`, `controls-list`,
`vendors`, `framework-scopes`, `attest`, `admin`, `control-detail`,
`dashboard`, `risk-hierarchy`, `board`, `calendar`, `risks`,
`audit-periods`, `evidence`, `exceptions`, `policies`, `me` — the exact
18 `export *` lines in `web/lib/api.ts`) and built the map at runtime.
The map came out to exactly **219 symbols with zero collisions**, byte-
matching the 370 table's count — a cross-check that the split surface is
intact. `_shared.ts` was excluded (its `apiFetch`/`bffControlFetch` are
package-internal and never in the barrel — 370 D5).

## D2 — Codemod, not manual edit; named-import-statement scoped

A Node codemod (`/tmp/codemod_395.mjs`, not committed — a throwaway
tool) rewrote each `import { … } from "@/lib/api"` statement:

- Each imported symbol is looked up in the map; clauses are grouped by
  target module; one `import … from "@/lib/api/<module>"` is emitted per
  module, modules sorted alphabetically for deterministic diffs.
- `type` modifiers are preserved at both granularities: a top-level
  `import type { … }` stays `import type`; an inline `import { type X }`
  keeps the inline `type`. Aliases (`a as b`) are preserved.
- The codemod matched only the `{ … }` named-import form; the **one**
  namespace import (`import * as api from "@/lib/api"` in
  `web/app/(authed)/dashboard/dashboard-prefetch.test.ts`) was migrated
  by hand (D3).

176 files were rewritten by the codemod; 1 by hand (D3) → **177 import
sites migrated**, matching the 370 estimate of "176" plus the namespace
case the count rounded over.

## D3 — `dashboard-prefetch.test.ts` namespace import + `vi.mock` retarget

This test does `vi.mock("@/lib/api", …)` then `import * as api from
"@/lib/api"`. The SUT (`dashboard-prefetch.ts`) imports its six upstream
fns (`getControlDrift`, `getEvidenceFreshness`, `getMitigateRisks`,
`getUpcoming`, `getFrameworkPosture`, `getActivity`) — all six resolve to
the `dashboard` module per the 370 map. The codemod repointed the SUT to
`@/lib/api/dashboard`; the test's `vi.mock` factory MUST target the same
module the SUT now imports from, or the mock no longer intercepts the
live bindings. Both the `vi.mock` target and the namespace `import * as
api` were retargeted to `@/lib/api/dashboard`. The six mocked fns are
unchanged — they all live in that one module.

## D4 — Duplicate same-module imports consolidated (board e2e spec)

`web/e2e/board-pack-export-e2e.spec.ts` had two separate `@/lib/api`
import statements (one `import type`, one value import) — both resolved
to the `board` module after migration, leaving two `import … from
"@/lib/api/board"` lines. No `no-duplicate-imports`/`import/no-duplicates`
rule is configured, so this would not fail CI, but it is sloppy; the two
were merged into a single `import { BOARD_PACK_SECTION_KEYS, type
BoardPack, type BoardPackSection } from "@/lib/api/board"`. This was the
only file in the 177 with a same-module duplicate.

## D5 — Sibling-module imports left untouched

Some files import from BOTH the barrel and a non-barrel sibling in the
same dir (`@/lib/api/metrics`, `@/lib/api/audit`, `@/lib/api/audit-server`,
`@/lib/api/*-export`). Those siblings are NOT re-exported by the barrel,
so the barrel never aliased their symbols; only the barrel import was
rewritten, and no sibling import collided on a module with a barrel-
derived import (verified: the only same-module duplicate was D4's board
spec, both barrel-derived). `web/lib/api/audit-server.ts` imports
`apiBaseURL` from the barrel — rewritten to `@/lib/api/base` (the file
lives inside `lib/api/`, so the `@/`-rooted path is still correct).

## D6 — SESSION_COOKIE (M-3) rename DEFERRED to spillover slice 397

The slice doc (and 370 D2) gave the Engineer the call on whether to
bundle the `SESSION_COOKIE → ATLAS_JWT_COOKIE` rename (slice 328 M-3)
into this PR. **Decision: defer to spillover slice 397.**

**Why deferred (not bundled):**

- An atomic symbol rename (so the build never has a dangling reference)
  must update **every** reference in the same commit. Two of the ~370
  bare `SESSION_COOKIE` references live in golden-tier contract tests:
  `web/lib/contracts/me.contract.test.ts` and
  `web/lib/contracts/demo-status.contract.test.ts`, both
  `import { SESSION_COOKIE } from "@/lib/auth"`.
- Slice 395's brief carries a **hard rule**: do NOT disturb
  `web/lib/contracts/` (slice 349/392 golden tier). The rename cannot be
  done atomically inside 395 without violating that hard rule. The two
  constraints are in direct conflict; the hard rule wins over the
  discretionary ("Engineer's call") rename.
- Bundling a 370-occurrence rename into a 177-file import codemod also
  doubles the review surface and entangles two unrelated concerns
  (path relocation vs. naming consistency). Keeping 395 single-concern
  keeps the codemod auditable.
- M-3 is cosmetic — 328 M-3 itself: "the underlying behavior is correct,
  only the symbol name is misleading." Zero urgency; nothing regresses
  by splitting it out.
- The word-boundary hazard (`OIDC_SESSION_COOKIE` contains the substring
  `SESSION_COOKIE` and must NOT be renamed) is also cleaner to get right
  in a focused rename slice than as a rider on an import sweep.

Spillover filed: `docs/issues/397-session-cookie-symbol-rename.md`
(depends on #395, status `ready`, no-auto-merge — golden-tier touch
needs maintainer review). P0-395 anti-criteria are all honored: no
signature/wire change, barrel not deleted, no auto-merge.

## D7 — Pre-existing `tsc --noEmit` errors (15) are NOT slice 395's; spillover 398

`tsc --noEmit` (the `npm run typecheck` script) is **already red on
`main`** with 15 errors across three test files NOT touched by this
slice (`lib/auth/oauth-client.test.ts`, `next-config.test.ts`,
`scripts/capture-readme-screenshots.test.ts`). Verified by stashing all
slice-395 changes and re-running on the clean checkout: identical 15
errors. Diffing the baseline error set against the post-migration error
set is byte-identical — **slice 395 introduces zero new type errors;
the migration is type-clean.**

These do not gate CI: the web CI job runs `npm run build` (`next build`),
not standalone `tsc --noEmit`. `next build` type-checks the production
graph (passed clean: 106/106 static pages) but does not compile these
test files. `vitest` runs them fine (1204/1204 green). The gap is only
in the un-wired standalone `typecheck` script. AC-2's `tsc --noEmit`
clause is therefore satisfied _for this slice's changes_ (no new errors);
the pre-existing failures are captured as spillover slice 398 rather than
fixed here (out of scope — unrelated test files; continuous-batch policy
is capture-don't-fix).

Spillover filed: `docs/issues/398-web-tsc-noemit-pre-existing-test-type-errors.md`.

## Verification

- AC-1: `grep -rn '"@/lib/api"' web/{app,components,lib,e2e,e2e-audit,scripts}`
  (excluding `web/lib/api.ts` itself) returns nothing — zero barrel
  imports remain.
- AC-2: `npm run lint` 0 errors; `npm run test` (vitest) 1204/1204 green;
  `next build` 106/106 pages; slice-347 per-file coverage gate green;
  `tsc --noEmit` introduces zero new errors over the main baseline (D7).
  Playwright e2e runs as the required CI check against the real stack (the
  migrated `board-pack-export-e2e.spec.ts` type-checks; its three imported
  `board` symbols are confirmed exported by `lib/api/board.ts`).
- AC-3: pure import-path relocation — no function signature, return type,
  or wire shape changed. The codemod only rewrites `import` specifiers.
- Coverage (slice 347/370 D7): import-path-only changes do not move which
  physical source file v8 attributes execution to, so per-file floors are
  unaffected; `npm run test:coverage` stays green.
