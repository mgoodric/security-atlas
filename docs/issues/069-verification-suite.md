# 069 — verification suite: Playwright runner wiring + frontend unit tests + Go coverage gate

**Cluster:** Quality / verification
**Estimate:** 2.5d
**Type:** AFK

## Narrative

The codebase has strong Go coverage (147 `*_test.go` files, 63 integration tests under `Postgres RLS` CI) and explicit Playwright specs already authored ahead of the runner (`web/e2e/{admin-bootstrap,audit-workspace,control-detail,dashboard,risk-hierarchy}.spec.ts`). What's missing is the connective tissue that turns those into a working verification surface:

1. **Playwright is not installed.** Every spec under `web/e2e/` uses a local type-shim instead of importing `@playwright/test`. From `web/e2e/dashboard.spec.ts:4-9`:

   > "this spec lives AHEAD of the Playwright runner — `web/` has no @playwright/test installed yet (adding it touches web/package.json, a spine file, and is a shared follow-up across the frontend slices). The spec is written under the same `ifPlaywright` shim so [typecheck/lint stays green] and the file is a precise, reviewable contract of the intended end-to-end assertions."

   This slice is that "shared follow-up". Land the package install, replace the shim, ship a `playwright.config.ts`, and run all five specs to green against a real stack.

2. **Frontend unit tests are at zero.** `find web -name '*.test.*' -not -path '*/e2e/*' | wc -l` returns 0. The web tree has client + server modules (e.g. `web/lib/api.ts`, the BFF route handlers at `web/app/api/**/route.ts`) that contain real logic — server-vs-client URL resolution, cookie-based bearer forwarding, error-code translation — that should be unit-tested in isolation, not only via the slower e2e harness. Pick `vitest` (Vite-native, fast, jest-compatible API, works inside Next.js's npm workspace without ejecting from the build) and ship a minimal but useful seed of tests for the highest-risk modules.

3. **Go coverage has no enforcement gate.** `internal/api/ucfcoverage` exists for the UCF-mapping-coverage product feature (orthogonal), and `cmd/scripts/coverage-check` exists for some bespoke check, but `go test ./...` does not run with `-cover` in CI and no per-package threshold gates merges. Several packages introduced over the last six months (`internal/authz`, `internal/tenancy`, `internal/audit/notes`, `internal/api/admincreds`) hold load-bearing tenant-isolation logic where a regression should be caught by an explicit floor, not by a downstream integration test eventually failing. Add a coverage step in the existing `Go · build + test` job with a published per-package floor and a single deny-list for legitimate exclusions (sqlc-generated `internal/db/dbx/*`, generated protos under `internal/proto/*`).

4. **No CI job actually executes the Playwright specs.** Even after wiring the runner locally, CI must build atlas + web, bring up postgres/nats/minio service containers, run bootstrap, and execute Playwright headless. This is the loop that catches regressions in the BFF + RSC paths the e2e specs are written against.

The slice delivers value because it converts existing latent-quality investment (147 Go tests, 5 e2e specs) into actively-enforced quality. Future slices land against a `make verify` target that asserts unit + integration + e2e + coverage in one command.

### What was discovered

| Surface                              | State today                                                                              | This slice                                                                                         |
| ------------------------------------ | ---------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------- |
| `web/e2e/*.spec.ts`                  | 5 specs authored, all behind a type-shim because Playwright not on dep                   | Install `@playwright/test`, replace shim with real import, add `playwright.config.ts`              |
| `web/` unit tests                    | Zero `*.test.{ts,tsx}` files outside e2e                                                 | Bootstrap `vitest` + initial seed: `lib/api.test.ts`, `lib/api/bff.test.ts`, ~2 BFF route handlers |
| Go coverage                          | `go test ./...` runs without `-cover`; no thresholds enforced                            | Add `-coverprofile`, publish per-package thresholds, fail merge under floor                        |
| CI job for e2e                       | None                                                                                     | New `Frontend · Playwright e2e` job, runs against a docker-compose-built stack with services       |
| `web/e2e` fixtures                   | Each spec references env vars (`PLATFORM_BASE_URL`, `TEST_BEARER`) with no central setup | One `e2e/fixtures.ts` that owns sign-in + tenant pre-seed, surfaced via Playwright project use     |
| Reviewable "how to add a test" entry | None                                                                                     | Section in `CLAUDE.md` or `Plans/canvas/testing.md` (TBD) plus inline READMEs                      |

### Scope discipline (matches user intent: "where it would provide value")

NOT in scope:

- Hitting 100% line coverage on every package
- Rewriting any of the 147 existing Go tests
- Adding component-level tests for every React component (`button.test.tsx` etc.) — that's a follow-up slice if it's ever worth it
- Visual-regression / snapshot tests — explicit anti-criterion below

IN scope:

- Enforceable floor on the packages whose regressions would be the most expensive
- E2E happy-path coverage of the five flows already specified (admin bootstrap, audit workspace, control detail, dashboard, risk hierarchy)
- Documentation that future agents can follow to add the (N+1)th test without re-inventing the wheel

## Acceptance criteria

### Playwright runner wiring (specs already exist)

- [ ] AC-1: `web/package.json` adds `@playwright/test` to `devDependencies` (pin to a current minor; document in CHANGELOG that this lands as a workspace dev-only dep)
- [ ] AC-2: `web/playwright.config.ts` exists with: a `projects` entry for chromium (firefox + webkit deliberately deferred — see anti-criterion P0-A1), `webServer` pointing at the platform under test, `use.baseURL` driven by `PLATFORM_BASE_URL` env, `retries: 1` (CI) / `retries: 0` (local), `reporter: ['list', ['html', { open: 'never' }]]`
- [ ] AC-3: Each of the 5 existing specs has its `ifPlaywright` shim removed and the commented `import { test, expect } from "@playwright/test"` un-commented; the local `type Test`/`type Expect` block is deleted; `npm run typecheck` AND `npm run lint` stay green
- [ ] AC-4: `web/e2e/fixtures.ts` exposes a `test.extend<{ authedPage }>` fixture that signs into the platform once per worker using `TEST_USER_EMAIL` + `TEST_USER_PASSWORD` and yields an authenticated `page` — each spec uses this fixture instead of re-implementing sign-in
- [ ] AC-5: `npx playwright test` against a locally-running `deploy/docker/docker-compose.yml` stack (or its equivalent) passes all 5 specs

### Frontend unit tests (zero → useful seed)

- [ ] AC-6: `web/package.json` adds `vitest` + `@vitest/coverage-v8` + `@testing-library/jest-dom` (matchers only — no DOM lib if we're testing modules, not components, in this slice) to `devDependencies`
- [ ] AC-7: `web/vitest.config.ts` exists with: jsdom env disabled (we test modules, not React components in this slice), coverage reporter `text` + `json-summary`, glob `src/**/*.test.ts` + `lib/**/*.test.ts` + `app/api/**/*.test.ts`
- [ ] AC-8: `web/lib/api.test.ts` covers the server-vs-client base-URL switch added in PR #95: at minimum (a) `typeof window === "undefined"` → reads `ATLAS_HTTP_URL`, (b) browser context → reads `NEXT_PUBLIC_API_BASE_URL` else empty, (c) both env vars unset → server falls back to `http://atlas:8080`
- [ ] AC-9: `web/lib/api/bff.test.ts` covers the BFF forwarding helper (`web/lib/api/bff.ts`): bearer-cookie pass-through, upstream-error translation, 401-on-missing-cookie
- [ ] AC-10: At least one BFF route handler under `web/app/api/admin/me/route.ts` has a route-level test that asserts the response shape on the happy path + the 403 / 401 paths
- [ ] AC-11: `npm run test` invokes vitest; `npm run test:coverage` writes a JSON summary that CI uploads as an artifact

### Go coverage gate

- [ ] AC-12: The `Go · build + test` CI job runs `go test -coverprofile=coverage.out ./...` and computes per-package coverage from it
- [ ] AC-13: A new file `cmd/scripts/coverage-thresholds.json` defines minimum per-package line coverage. Initial floors (the FIRST run of the new gate ratchets to these — do NOT pick numbers higher than what's currently passing; raise in follow-up slices):
  - `internal/authz/...` ≥ 80%
  - `internal/tenancy/...` ≥ 80%
  - `internal/audit/...` ≥ 75%
  - `internal/api/admincreds/...` ≥ 75%
  - `internal/api/tenancymw/...` ≥ 80%
  - All other `internal/...` ≥ 60%
  - Explicit exclusions: `internal/db/dbx/...` (sqlc generated), `internal/proto/...` (protoc generated), `internal/**/*_mock.go` (test doubles), `cmd/...` (main packages, integration-tested elsewhere)
- [ ] AC-14: `cmd/scripts/coverage-check` (or a new sibling script) reads `coverage.out` + `coverage-thresholds.json` and exits non-zero if any covered package falls under its floor; the existing UCF coverage check is unaffected (separate concern)
- [ ] AC-15: CI step "Go · coverage gate" runs after `go test`, fails the job on threshold violation, uploads `coverage.out` as an artifact

### CI integration

- [ ] AC-16: New CI job `Frontend · Playwright e2e` is added to `.github/workflows/ci.yml`. It depends on the existing `Detect changed paths` job and runs when web/ or atlas-server-affecting paths change. Uses GitHub Actions service containers for postgres 16 + nats 2.10 + minio (matching `deploy/docker/docker-compose.yml` images), builds atlas + web from source, runs `bootstrap.sh` against the services, then `npx playwright test` headless.
- [ ] AC-17: On Playwright failure, the job uploads the HTML report + screenshots + trace as workflow artifacts so a maintainer can drag-drop the trace into `npx playwright show-trace` locally for diagnosis
- [ ] AC-18: New CI job `Frontend · vitest` runs `npm run test:coverage` and uploads the coverage summary as an artifact. Fails the job on test failure (no coverage gate in this initial slice — the data is collected to inform the AC-13 follow-up of "raise the bar")
- [ ] AC-19: Both new jobs are added to `.github/branch-protection.json` as required checks for `main` (extending the existing 10 to 12)

### Documentation + ergonomics

- [ ] AC-20: `web/e2e/README.md` exists with: "how to run locally", "how to add a new spec", "how to debug a failure via the trace viewer", "what the auth fixture does and how to override it"
- [ ] AC-21: `web/README.md` (or a new `web/testing.md`) explains the vitest layout, the difference between vitest (modules) and Playwright (flows), and when to reach for each
- [ ] AC-22: A new entry in `CLAUDE.md` under the existing testing-discipline section enumerates the four enforced surfaces (Go unit, Go integration, frontend vitest, frontend Playwright) and links to their respective entry points

## Constitutional invariants honored

- **CLAUDE.md "tests are the contract" discipline:** This slice is the operationalization. The four test surfaces become CI-enforced rather than optional-by-convention.
- **Invariant 6 (RLS at DB layer):** Indirect — the new Go coverage gate (AC-13) sets the highest floors on `internal/authz`, `internal/tenancy`, `internal/api/tenancymw`. Regression in any of those is the most expensive regression class in the codebase; the floor is the enforcement.
- **Slice 037 acceptance criteria (5-min bring-up + 4-hour-to-first-evidence):** The Playwright e2e job (AC-16) is the integration test that catches regressions in the user-visible bring-up path before they reach `main`.

## Canvas references

- `Plans/canvas/09-tech-stack.md` (testing surfaces — confirm or extend)
- `Plans/canvas/01-vision.md` §1.5 (acceptance criterion #7 — installable + first evidence in 4h, which the e2e job protects)
- `CLAUDE.md` testing-discipline section
- `web/e2e/dashboard.spec.ts:4-9` (the comment that explicitly identifies this slice as "shared follow-up")

## Dependencies

- #037 (merged) — provides the self-host bundle the Playwright job builds against
- #040 (merged or in queue) — wrote `dashboard.spec.ts`
- #041 (merged or in queue) — wrote `control-detail.spec.ts`
- #060 (merged or in queue) — wrote `admin-bootstrap.spec.ts`
- All slices whose features are tested e2e (see `web/e2e/*.spec.ts` for the inventory) — the slice doesn't add coverage for features not yet built

## Anti-criteria (P0 — block merge)

- **P0-A1**: Does NOT add firefox or webkit to the Playwright `projects`. Chromium-only in this slice — multi-browser is a follow-up. Each added project doubles wall-clock and CI cost; defer until there's a real cross-browser bug to motivate it.
- **P0-A2**: Does NOT add visual-regression / snapshot diffing. Pixel diffs are flaky on CI runners and produce a maintenance tax that exceeds their bug-catching value at this stage. Use them later, behind a separate label, if a real visual regression slips through.
- **P0-A3**: Does NOT add component-level React unit tests (`button.test.tsx`, etc.). The vitest seed is scoped to module-level logic (api.ts, bff.ts, route handlers); React component testing is a separate skill mix + a separate slice if ever wanted.
- **P0-A4**: Does NOT raise the Go coverage thresholds above what currently passes. The first-pass floors in AC-13 are intentionally a ratchet, not a stretch goal. Stretch goals land as follow-up slices where the agent first writes the missing tests, then lifts the floor in the same PR.
- **P0-A5**: Does NOT rewrite any of the 147 existing Go tests. The slice is additive. Tests that are out of date because the code drifted should be filed as their own spillover slices, not folded in.
- **P0-A6**: Does NOT install `@playwright/test` at the repo root. It goes in `web/devDependencies` — the e2e is a frontend concern, not a workspace-wide one.
- **P0-A7**: Does NOT add e2e specs for flows whose underlying feature is in-flight or unmerged. Only the five existing specs get wired in this slice. New specs for new flows land with those flows (per the per-slice template's "tdd" workflow step).
- **P0-A8**: Does NOT bypass the `pre-commit run --all-files` step before push. Lessons from PR #116: prettier reformat caught the first push. Runner discipline > rework cycles.
- **P0-A9**: Does NOT use vendor-prefixed tokens in test fixtures (neutral `test-*` only — same convention as the rest of the codebase per slice 05's documented hard rules)

## Skill mix (3–5)

- Playwright config + fixture composition (TypeScript, project setup)
- Vitest setup in a Next.js workspace (avoiding jest/babel collisions with Next's SWC)
- Go coverage tooling + threshold gating (`go test -coverprofile`, `go tool cover -func`)
- GitHub Actions service containers (postgres + nats + minio orchestration)
- Documentation discipline (README + CLAUDE.md updates that future agents will actually read)

## Notes for the implementing agent

- The 5 existing Playwright specs are written assertively — they assume specific UI elements, data preconditions, and bearer-token shapes. Read each spec's preamble comment (e.g. `dashboard.spec.ts:21-26`) BEFORE writing the auth fixture; the fixture must establish the seed data those assertions depend on. If a precondition cannot be established (data missing, feature not actually built yet), file as a spillover slice per `Plans/prompts/07-continuous-batch-loop.md`'s Amendment 2 — do NOT relax the spec.
- For the vitest setup specifically, do NOT install `@testing-library/react` in this slice (P0-A3). The seed tests are module-level and don't render React. Adding React Testing Library expands the dependency surface without a corresponding test in scope.
- The Go coverage gate's first-pass thresholds (AC-13) MUST be derived from a real `go test -cover ./...` run on `main`, not picked from intuition. If the actual measured coverage for a package is currently 62% and you propose a floor of 75%, that's writing tests in this slice — which the slice's scope explicitly excludes (per scope discipline). Set the floor at `floor(actual - 2pp)` to allow for minor noise, file a follow-up slice for "raise X coverage floor to 75%", and move on.
- The CI job for Playwright (AC-16) will be the slowest job on the matrix. Time-budget it: 8 min for build + bootstrap + 5 specs. If a single spec exceeds 90s, that's a test-quality issue (probably missing `await page.waitForLoadState('networkidle')` or an over-broad selector) — flag back to the spec author rather than absorbing the slowness as the new baseline.
