# Slice 450 — vitest 4 + @vitest/coverage-v8 4 paired migration — decisions log

**Type:** AFK / build-tooling MAJOR (two majors: vitest 2 → 4) + paired
coverage-v8 2 → 4.

- detection_tier_actual: integration
- detection_tier_target: integration

(The load-bearing finding — the coverage-v8 4 AST-remapper re-baseline need —
was caught exactly where it should be: by running the slice-347 coverage gate
locally / in the `Frontend · vitest` integration tier BEFORE merge, which is
the gate's whole purpose. Not a production escape; the gate did its job.)

## Versions

- `vitest` `^2.1.8` → `^4.1.8` (resolved 4.1.8).
- `@vitest/coverage-v8` `^2.1.8` → `^4.1.8` (resolved 4.1.8).
- vitest 4 bundles Vite **8.0.16** (NOT Vite 6 as the spec guessed — vitest's
  peerdep is `vite: ^6 || ^7 || ^8` and it carries its own Vite, so the repo
  does not depend on Vite directly and the bundled major is immaterial to us).
- coverage-v8 4 peer-pins `vitest: 4.1.8` exactly; `^4` for both resolves to a
  matched 4.1.8 / 4.1.8 pair — no "Running mixed versions is not supported"
  (the failure mode that sank dependabot #948 and #950). **P0-450-1 satisfied.**
- `@types/node` peerdep `^20 || ^22 || >=24`; repo pins `^25` → satisfies
  `>=24`. CI Node 20.x satisfies vite's `^20.19.0` engine. **No Node bump
  (slice 452 owns that).**

## D1 — Config migration: zero behavioural edits needed (AC-2)

`web/vitest.config.ts` was already `defineConfig` from `vitest/config` (TS/ESM),
and every option it sets (`environment: "node"`, `globals: false`,
`coverage.provider: "v8"`, `reporter: ["text","json-summary"]`,
`reportsDirectory`, `thresholds`, include/exclude globs) remains valid in
vitest 4. A full `vitest run` and `vitest run --coverage` emit **zero
deprecation warnings**. The `environment: "node"` pin (slice 069 P0-A3) is
retained verbatim — **P0-450-5 satisfied** (no jsdom/happy-dom, no DOM tests).
The only config edit is a documentation comment recording the v8-provider
behaviour change (below); no option was added, removed, or renamed.

## D2 — Test-API migration: none required (AC-4)

vitest 4 changed some matcher/mocking APIs, but this suite uses `globals:
false` with explicit `import { describe, expect, it } from "vitest"` and no
`vi.mock` spy-API surface that v4 broke. All **184 test files / 1760 tests**
pass unchanged (identical count to the vitest-2 baseline — no false-green
zero-collection; **P0-450-4 satisfied**). No test file was edited.

## D3 (LOAD-BEARING) — coverage-v8 4 AST-remapper changes the coverage ruler

coverage-v8 4 depends on **`ast-v8-to-istanbul`** and invokes it
unconditionally (`astV8ToIstanbul` in the provider; grep confirms no legacy
`v8-to-istanbul` toggle survives). AST-aware remapping no longer credits a
module with statement/line coverage for being transitively module-loaded by a
test that never calls its functions — it eliminates the "phantom coverage" the
`coverage-thresholds.json` `$comment` already documents for the slice-396
barrel-retire, but now globally.

Consequence: the _same 1760 tests on the same source_ re-measure differently
under the new provider. **68 metric-floors across 48 files** now read below
their slice-347-recorded value. This is NOT a coverage regression (no test
lost; suite identically green) — it is a ruler change. The largest gaps (98pp)
are exactly the phantom-coverage files (`lib/api/risks.ts`,
`lib/api/policies.ts`, `lib/api/audit.ts`, `components/control/strm.ts`,
`components/dashboards-metrics/format.ts`, `lib/api/audit-periods.ts`) which the
new mapper correctly reports at 0% functions/branches because no test calls
their bodies.

### D3 decision: re-baseline FOLDED INTO 450 (maintainer-approved 2026-06-12)

- **Initial instinct (correct to flag):** **P0-450-3** forbids a tooling bump
  from editing the slice-347 per-file floors — floor changes belong to a
  dedicated floor slice — so the re-baseline was first drafted as spillover
  **slice 740** (`docs/issues/740-vitest4-coverage-floor-rebaseline.md`) and the
  450↔740 coupling was reported to the maintainer rather than silently editing
  48 floors inside a tooling bump.
- **Resolution (maintainer ruling 2026-06-12):** the maintainer reviewed the
  coupling — because the slice-347 gate IS vitest's built-in threshold check,
  slice 450's `Frontend · vitest` job cannot go green WITHOUT the floors already
  matching the new provider, so 450 and 740 are inseparable at the CI-green
  boundary — and **explicitly approved applying the re-baseline INSIDE slice
  450's PR (#1347)**, landing the vitest-4 bump and the floor re-baseline
  atomically. With that sanction the P0-450-3 reservation is lifted for these 48
  files; the re-baseline now ships in 450 and slice 740 is `superseded` (folded
  in, no separate slice).
- **What was applied:** the 68 breaching metric-floors across 48 files were
  re-seeded DOWNWARD at the standard project convention `max(0, floor(measured −
2pp))` of the coverage-v8 4 measured numbers (matching how every untouched web
  floor was set — the `$methodology` in the thresholds file). **Only the 68
  breaching (file, metric) pairs were lowered; no passing floor was touched and
  no floor was raised** (verified: `git diff` on `web/coverage-thresholds.json`
  is 68 lines changed, every change a strict decrease). The threshold-key count
  stays 153 (no file added/removed). A re-baseline marker was appended to the
  file's `$comment` recording WHY the 48 dropped (the AST-remapper / slice-396
  phantom-coverage precedent), so a future reader does not mistake the lower
  numbers for a quality regression.
- **Phantom-coverage 0% files** (`lib/api/{risks,policies,audit,audit-periods}.ts`,
  `components/control/strm.ts`, `components/dashboards-metrics/format.ts`): kept
  an explicit `0` floor in the map (rather than moving to the `$omitted_zero_pct`
  set) so the row visibly documents the deliberate drop. Their real coverage is
  the slice-349/392 contract-tier rollout's job; recovering the phantom credit
  was rejected (it was never real coverage — vitest 4 correctly removed it).
- **Post-re-baseline gate result:** `npm run test:coverage -w web` exits **0**
  — 184 files / 1760 tests green, **0 floor breaches** against real
  coverage-v8-4 numbers.

## D4 — AC-6 gate-enforcement proof (the load-bearing deliverable)

Two independent proofs the slice-347 gate still has teeth under coverage-v8 4:

1. **Organic proof.** The post-bump `npm run test:coverage` exits **1** with 68
   real `ERROR: Coverage for … does not meet … threshold` lines — the gate is,
   right now, firing against real measured per-file numbers. A silently-no-op'd
   provider would have exited 0 with everything "passing" against zero data.
   This alone disproves the P0-450-2 silent-gate-drop threat.

2. **Deliberate-removal scratch proof (canonical AC-6 artifact, reverted).**
   Picked a file PASSING its floor (`app/(authed)/controls/count-label.ts`,
   measured 100% / floor 98). Deleted its sole test
   (`app/(authed)/controls/count-label.test.ts`) in a throwaway run — dropping
   the file to 0% — and the gate FAILED:

   ```
   $ mv app/(authed)/controls/count-label.test.ts /tmp/   # scratch, reverted
   $ npm run test:coverage    # exit 1
       Test Files  183 passed (183)     # suite itself green …
         Tests  1751 passed (1751)
     count-label.ts   |   0 |   0 |   0 |   0 | 34-87
   ERROR: Coverage for lines (0%) does not meet "app/(authed)/controls/count-label.ts" threshold (98%)
   ERROR: Coverage for functions (0%) does not meet ".../count-label.ts" threshold (98%)
   ERROR: Coverage for statements (0%) does not meet ".../count-label.ts" threshold (98%)
   ERROR: Coverage for branches (0%) does not meet ".../count-label.ts" threshold (98%)
   ```

   The suite stayed green at the test level (1751 passed) while the GATE failed
   — proving coverage-v8 4 is genuinely measuring per-file coverage and the
   slice-347 ratchet still enforces. Scratch reverted; `git status` confirmed
   clean of it before commit.

## D5 — AC-8 scope discipline held

`git diff --stat` on the committed change is confined to:
`web/package.json`, `package-lock.json`, `web/vitest.config.ts` (doc-comment
only), `CHANGELOG.md`, the spillover slice doc, and this decisions log. **No
production `web/app` or `web/lib` runtime code changed.** The 2 pre-existing
eslint warnings in `web/scripts/capture-readme-screenshots.ts` are untouched
(out of scope; warnings, not errors — CI lint stays green).

## CI-parity results (local)

- `npm install` (repo root) — clean, lockfile committed.
- `npm run test -w web` — 184 files / 1760 tests pass, exit 0.
- `npm run lint -w web` — exit 0 (2 pre-existing unrelated warnings).
- `npm run typecheck -w web` (`tsc --noEmit`) — exit 0.
- `npm run test:coverage -w web` — coverage POPULATED (real per-file numbers in
  `coverage/coverage-summary.json`); after the maintainer-approved re-baseline
  (D3) the gate exits **0 with 0 floor breaches** against real coverage-v8-4
  numbers. (Pre-re-baseline it fired 68 breaches — the AST-remapper ruler change
  — which is exactly the data D3 re-seeded.)

## D6 — second CI regression: self-host web image build (decouple test config from prod typecheck)

After the floor re-baseline (D3) went green, CI surfaced a SECOND, distinct,
non-flake failure (reproduced on rerun): the `Self-host bundle · end-to-end`
job (all 4 matrix variants) failed because the self-host **web Docker image
build** (`deploy/docker/web.Dockerfile`, which runs `npm run build` = `next
build`) failed with:

```
./vitest.config.ts:58:30
Type error: Cannot find module 'vitest/config' or its corresponding type declarations.
Next.js build worker exited with code: 1
```

**Root cause.** `next build` runs the TypeScript checker over `tsconfig.json`'s
`include` set (`**/*.ts`), which matched `vitest.config.ts` (and all 184
`**/*.test.ts`). The bare `import { defineConfig } from "vitest/config"` (and
the `import … from "vitest"` in every test) resolved in the production build
context under **vitest 2** but does **not** under **vitest 4** — vitest 4
changed the `vitest` / `vitest/config` package export + type-declaration
structure. This is a real slice-450 regression (it passed on `main` at vitest
2). It is NOT a Node-version issue: the container is already `node:22-alpine`
(satisfies vitest 4's `>=22.12.0`), so there is **no coupling to slice 452**.

**Fix (the correct separation — `next build` should not typecheck test infra).**

1. `web/tsconfig.json` `exclude` gains `"vitest.config.ts"` and `"**/*.test.ts"`
   (alongside the existing `"node_modules"`, `"e2e"`). The prod build typecheck
   graph no longer contains any test-tooling file, so the `vitest`-type
   resolution failure cannot occur in `next build`. (Excluding only
   `vitest.config.ts` would have surfaced the identical error on the first
   `*.test.ts` tsc reached, since all 184 tests `import … from "vitest"` — so
   both the config and the test glob are excluded.)
2. To NOT silently drop type-safety on the test files, a dedicated
   **`web/tsconfig.test.json`** (`extends: ./tsconfig.json`, re-includes
   `vitest.config.ts` + `**/*.test.ts`) is added, and the `typecheck` script
   becomes `tsc --noEmit && tsc --noEmit -p tsconfig.test.json`. The test files
   are STILL fully type-checked (the second invocation, where devDeps are
   present so the vitest types resolve) — proven by injecting a deliberate
   `TS2322` into a test and confirming the test-config tsc catches it.
3. vitest itself never reads either tsconfig (it transpiles via esbuild), so the
   split does not touch the runner — `Frontend · vitest` + the coverage gate are
   unaffected.

**Verification (reproduced-before / proven-after).**

- BEFORE fix: `docker build -f deploy/docker/web.Dockerfile -t sa-web-test .`
  FAILED at `next build` with the exact `Cannot find module 'vitest/config'`
  error at `./vitest.config.ts:58:30`.
- AFTER fix: the same build **succeeds** — `✓ Compiled successfully` and the
  image is produced.
- `npm run typecheck -w web` (both configs) — exit 0; the test files are
  type-checked via the test config (injected-error proof).
- `npm run test -w web` — 184 files / 1760 tests pass.
- `npm run test:coverage -w web` — exit 0, 0 floor breaches (D3 re-baseline
  intact).

This is the second distinct sequential CI regression of the bump (D3 was the
first); each fixed is expected, not a repeat.
