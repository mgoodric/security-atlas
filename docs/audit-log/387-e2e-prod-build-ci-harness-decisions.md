# Slice 387 — decisions log: production-build standalone CI harness

Type: JUDGMENT (CI job shape; how to boot the standalone server in CI; how
the two prod-build specs are gated on it).

Context: slice 351's audit (AC-4, disposition (b)) re-quarantined
`web/e2e/bff-cookie-production-build.spec.ts` (slice 146) and
`web/e2e/logo-render-production-build.spec.ts` (slice 153) behind
`test.skip(!process.env.ATLAS_PROD_BUILD, …)` because no CI job builds +
boots the Next.js `output: "standalone"` server. Both specs assert
regressions that ONLY manifest under `node .next/standalone/web/server.js`
(not under the `npm start` dev server the existing Playwright job uses), so
forcing them green against the dev server would assert nothing — green-
washing. This slice adds the missing harness so the guards become satisfied
in CI and the regressions gate every code-touching PR.

---

## D1 — Separate job, NOT a matrix leg of `Frontend · Playwright e2e`

The slice doc allowed either a new job OR a matrix leg. Chose a **separate
job** (`frontend-playwright-prod-build`, check name
`Frontend · Playwright e2e (prod-build standalone)`).

Rationale:

- A matrix leg would have to branch THREE things on the matrix value: the
  build command (`npm run build` vs `npm run build:standalone`), the
  web-server start command (`npm start` vs
  `node .next/standalone/web/server.js`), and the Playwright invocation
  (full suite vs the two named prod-build specs only). That is more
  conditional wiring inside one job than a clean second job costs in
  duplicated bring-up.
- The backend bring-up (Postgres service container + NATS + MinIO via
  `docker run` + atlas build/boot + JWT mint via global-setup) is byte-for-
  byte the same as the dev-server leg, so the duplication is mechanical and
  low-risk; it is mirrored verbatim. The slice-061 self-contained-block
  convention (each job appended as a delimited block so parallel ci.yml
  edits rebase cleanly) already accepts this kind of duplication.
- A distinct check name makes the standalone-leg pass/fail legible in the
  PR checks list, separate from the dev-server suite.

## D2 — The dev-server leg is left completely untouched

`frontend-playwright` still runs `npm run build` → `npm start` → the full
suite. It does NOT set `ATLAS_PROD_BUILD`, so the two prod-build specs stay
`test.skip`-guarded there (they no-op, as before). AC-3 satisfied: the
dev-server leg is unaffected.

## D3 — The `test.skip(!ATLAS_PROD_BUILD)` guard is RETAINED, not removed

The guard is the mechanism that scopes each spec to the standalone leg.
Removing it would make the specs attempt to run in the dev-server leg too,
where they would either fail (no public/ copy, no standalone cookie path)
or — worse — green-wash. Keeping the guard and setting `ATLAS_PROD_BUILD=1`
only in the new job is the correct gating. The spec bodies are unchanged
(slice doc scope discipline); only the quarantine-comment + skip-reason
string were updated to reflect that the harness now exists (slice 387) vs
"no CI harness yet, quarantined behind slice 387".

## D4 — Boot via `node .next/standalone/web/server.js`, build via `npm run build:standalone`

`web/next.config.ts` sets `output: "standalone"`. `npm run build:standalone`
= `next build && cp -r public .next/standalone/web/public`. The `cp` is
load-bearing: the standalone tracer does NOT copy `web/public/` into the
traced tree (exactly the slice 153 regression), so without it the logo +
OG/Twitter assets 404. This reproduces the web.Dockerfile runtime-stage
copy. The server entrypoint is `.next/standalone/web/server.js` (the `web/`
path segment mirrors the monorepo workspace path Next.js bakes into the
output). The standalone server reads `PORT` + `ATLAS_HTTP_URL` from the
job-level env, so its BFF proxies to atlas (:8080) identically to the dev
server. `reuseExistingServer: isCI` (playwright.config.ts) attaches to the
workflow-started server instead of spawning one.

## D5 — Specs named explicitly in the Playwright invocation

`npx playwright test bff-cookie-production-build.spec.ts
logo-render-production-build.spec.ts` runs ONLY the two standalone-only
guards in this leg. The dev-server leg owns the rest of the suite; running
the full suite here would duplicate ~all of it against the standalone
server for no added signal and roughly double the leg's wall-clock.

## D6 — Wall-clock cost (AC-4)

The new job's wall-clock is dominated by the same fixed costs as the
existing dev-server Playwright leg — service-container bring-up, `npm
install`, atlas `go build`, `next build`, `npx playwright install
--with-deps chromium` — plus the standalone `cp` (sub-second) and a 2-spec
Playwright run (faster than the full suite). Empirically on a clean dev box
`npm run build:standalone` completed in well under a minute and produced
`.next/standalone/web/server.js` + the copied `public/` tree; the booted
standalone server served `/logo-light.svg`, `/logo-dark.svg`,
`/og-image.png`, `/twitter-card.png`, `/favicon.ico` all 200 with correct
content-types and `/login` referencing `/logo-light.svg` — i.e. it exercises
the slice-153 path the spec asserts. Estimated CI wall-clock is comparable
to the dev-server leg (~5-8 min), running in parallel with it (independent
job), so it adds no critical-path serialization. It ships NON-required
(like slice 065 / 038) and is promoted to a required check after a few
green runs; `.github/branch-protection.json` is NOT modified by this slice.

## D7 — `disable-sudo: false` on this job

Same slice 069 / 117 D5 exception as the dev-server Playwright leg:
`npx playwright install --with-deps chromium` needs sudo to install
chromium's system libs. Egress audit still applies; only sudo is permitted
on this one job.

## D9 — cookie spec held to spillover 399; job ships ADVISORY + logo-only

The first CI run of the new job (run 26673862768) surfaced that the
harness works end-to-end — all 16 build/boot steps green, the standalone
server up, and the slice-153 `logo-render-production-build.spec.ts`
PASSES — but the slice-146 `bff-cookie-production-build.spec.ts` FAILS on
two **spec-body** issues that had never executed before (the guard had
never been satisfied until this slice built the harness):

1. `dashboard panel BFF returns JSON` asserts the browser fires
   `/api/dashboard/` BFF calls (`bffResponses.length > 0`). Under the
   PRODUCTION build the dashboard panels are server-rendered (RSC), so
   zero client-side BFF calls fire — received 0. The failure-run page
   snapshot shows a fully-authenticated dashboard (Sign-out button, "API
   key" identity, full nav, "Program" heading), so auth carries through:
   this is NOT the slice-146 cookie regression (that would render a login
   page). The assertion is dev-server-shaped and false for the prod build.
2. `session cookie sentinel …` calls `context.addCookies` with
   `domain: new URL(authedPage.url()).hostname` BEFORE any navigation, so
   `authedPage.url()` is `about:blank` → empty hostname → Playwright
   rejects the cookie.

Both are spec-BODY fixes, which slice 387's brief explicitly forbids
("DOES NOT modify the two spec bodies"). The correct, non-green-washing
resolution: the new job ships **ADVISORY** (`continue-on-error: true`)
and runs **only the logo spec** (which passes and proves the harness +
gates the slice-153 regression); the cookie spec is held to spillover
**slice 399** (`docs/issues/399-bff-cookie-prod-build-spec-body-fix.md`),
which re-shapes its two assertions, adds it back to the job's Playwright
invocation, and drops `continue-on-error` so the leg becomes blocking.
This honors disposition (b) — we do NOT force the cookie spec green
against a shape it cannot satisfy.

The unrelated `Go · integration` failure in the same run
(`TestRun_FiresInlineSweepAndExitsOnCancel`,
`internal/metrics/scheduler`) is the known scheduler-integration flake
(slice 346/352 history — passes on rerun); slice 387 touches zero Go and
did not cause it.

## D8 — actionlint cleanliness

The repo's pre-commit actionlint hook runs with `-shellcheck ""` (slice 158
D3 — embedded shellcheck disabled because pre-existing SC2034/SC2045
warnings in `run:` blocks are noise). The new job's `for i in $(seq 1 30)`
readiness loops and `for f in migrations/sql/*.sql` reuse the exact patterns
already present in the dev-server leg, so they carry the same (suppressed)
shellcheck profile. `actionlint -shellcheck "" -no-color
.github/workflows/ci.yml` exits 0.
