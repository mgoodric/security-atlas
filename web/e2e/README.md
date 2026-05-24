# Playwright end-to-end tests

This directory holds the security-atlas web app's Playwright e2e suite. Each spec is one user-flow contract; the assertions in a spec drive the BFF + RSC paths against a real platform bring-up.

## CI status: required-check (as of slice 116)

`Frontend · Playwright e2e` is a **required status check** on `main` (`.github/branch-protection.json` → `required_status_checks.contexts`). A red Playwright run blocks merge. The slice-061 docs-only fastpath is preserved: when a PR touches no code (`changes.outputs.code != 'true'`), the `frontend-playwright-stub` job posts pass under the same check name so docs-only PRs resolve the check in seconds.

Promotion history:

| Slice | What                                                                                                              |
| ----- | ----------------------------------------------------------------------------------------------------------------- |
| 069   | Job introduced; `continue-on-error: true`; informational only                                                     |
| 079   | Quarantined (the 5 specs lacked seed-data preconditions)                                                          |
| 082   | Seed-data harness landed; `continue-on-error` removed; job fails red on spec failure (still not a required-check) |
| 116   | Promoted to required-check in `branch-protection.json` after ≥5 clean PR runs across slices 142/143/198/201/202   |

## Files

| File                      | What it covers                                                                                              |
| ------------------------- | ----------------------------------------------------------------------------------------------------------- |
| `fixtures.ts`             | Shared `test.extend<{ authedPage }>` — signs in once per worker via `TEST_BEARER` or email/password env     |
| `dashboard.spec.ts`       | `/dashboard` — six bound panels, framework-posture placeholder, freshness binding, drift, top risks         |
| `control-detail.spec.ts`  | `/controls/:id` — coverage table, UCF mini-viz, evidence stream, freshness clock, effective-scope, OOS rows |
| `audit-workspace.spec.ts` | `/audit` — auditor flow: period bar, control nav, sampling, walkthrough, comments (shared/private)          |
| `risk-hierarchy.spec.ts`  | `/risks/hierarchy` — org tree, theme heatmap, decision timeline, overdue pills, deep-linked filters         |
| `admin-bootstrap.spec.ts` | `/admin/**` — admin tiles, SSO discovery, feature toggle, API key issuance, role matrix                     |

## How to run locally

```sh
cd web

# 1. One-time: pull the chromium binary Playwright needs.
npx playwright install --with-deps chromium

# 2. Point the suite at a running platform. Two options:
#
#    (a) docker-compose self-host bundle (most realistic — the same
#        shape CI runs against):
#
#        cd ../deploy/docker
#        cp .env.example .env
#        docker compose up -d
#        # wait for atlas + web to be healthy on :8080 and :3000
#        cd ../../web
#
#    (b) `npm run dev` against an atlas server you already have running
#        elsewhere (faster spec-iteration loop).

# 3. Either inject a long-lived test bearer, or set email/password to
#    use the real /auth/login flow (the fixture picks one).
export PLATFORM_BASE_URL="http://localhost:3000"
export TEST_BEARER="test-bearer-local"
#  or:
#  export TEST_USER_EMAIL="atlas-test@example.com"
#  export TEST_USER_PASSWORD="local-only-password"

# 4. Run.
npm run test:e2e
#  or one file:  npx playwright test e2e/dashboard.spec.ts
#  or headed:    npx playwright test --headed
```

## Seed-harness contract for spec authors

A spec's preconditions (the test user, tenant rows, seeded controls/evidence/policies it asserts against) MUST be establishable by the docker-compose bring-up the CI job uses. Concretely:

1. **Where preconditions live.** SQL fixtures live under `fixtures/e2e/*.sql`; the harness applies them via `web/e2e/seed.ts` after `atlas-bootstrap` finishes phase-2 migrations and before any `page.goto(...)` runs. Add a new fixture file next to the existing ones; name it `<spec-feature>.sql`; load it from `seed.ts`.

2. **What the harness guarantees.**

   - Postgres + NATS + MinIO are healthy.
   - The atlas server is reachable on `http://localhost:8080`; the web app on `http://localhost:3000`.
   - `atlas-bootstrap` has run; `evidence_kind_schemas` (and the other phase-2 tables) exist.
   - A long-lived test JWT is minted at runtime via `POST /v1/test/issue-jwt` (gated by `ATLAS_TEST_MODE=1`) and written into `process.env.TEST_BEARER` by Playwright `globalSetup`. The auth fixture (`e2e/fixtures.ts`) injects it as a `sa_session_token` cookie.

3. **What the harness does NOT do.**

   - It does not fabricate UI state the spec wasn't seeded for. If a spec asserts "12 controls visible", the fixture must `INSERT INTO controls ...` 12 rows.
   - It does not patch around flakes. A spec that intermittently fails MUST file as a spillover slice (root-cause it; never tolerate a flaky required-check — flakiness is worse than no required-check per slice 116 P0).
   - It does not isolate tests per-spec — fixtures are additive within a CI run. Two specs asserting against the same table must use distinct row sets (different `tenant_id`, different `id` prefix, etc.).

4. **Now that the job is a required-check (slice 116).** Adding a spec that fails locally and was merged "to see if CI catches it" will block every other PR until reverted. Run `npm run test:e2e` locally before pushing. If the new spec can only run against CI (e.g., needs a service the local compose doesn't ship), file a spillover slice for the local harness gap first.

## How to add a new spec

1. Create `e2e/<feature>.spec.ts`. Start it with:

   ```ts
   import { test, expect } from "@playwright/test";

   test.describe("<feature>", () => {
     test("<one assertion per AC>", async ({ page }) => {
       await page.goto("/<route>");
       // assertions
     });
   });
   ```

2. Reference the auth fixture from `./fixtures` if the test needs an authenticated session:

   ```ts
   import { test, expect } from "./fixtures";

   test("...", async ({ authedPage }) => {
     await authedPage.goto("/<protected-route>");
     // assertions
   });
   ```

3. Use `getByTestId("...")` over CSS selectors. The dashboard / control-detail / audit components already carry `data-testid` attributes on every assertable element.

4. Add the spec to CI by doing nothing — `npx playwright test` runs the whole `e2e/` directory.

## Timing-sensitive assertions

**Use auto-waiting assertions for any data that arrives asynchronously.** Playwright divides its locator API into two camps:

- **Auto-waiting**: `await expect(locator).toBeVisible()`, `await expect(locator).toHaveCount(N)`, `await expect(locator).toHaveText(...)`. These poll until the assertion passes or the timeout fires (5s default).
- **Snapshot**: `await locator.count()`, `await locator.innerText()`, `await locator.getAttribute(...)`. These read the DOM at the instant they fire and return immediately.

A snapshot read of state that arrives via `useQuery` / `fetch` / any async path is a race against the data-fetch. It works whenever the fetch happens to win; it fails whenever the page becomes visible before the fetch completes.

The shape of the bug: a parent `useQuery` resolves and renders the section's outer testid; the test's `await expect(section).toBeVisible()` passes; the next line snapshots a child locator that depends on a SECOND `useQuery` still in `isLoading`. Snapshot returns 0. Assertion fails.

**Bad** (slice 274 root cause):

```ts
await expect(page.getByTestId("settings-section-tokens")).toBeVisible();
const rowCount = await page.getByTestId("settings-token-row").count();
expect(rowCount).toBeGreaterThan(0);
```

**Good** (slice 274 fix):

```ts
await expect(page.getByTestId("settings-section-tokens")).toBeVisible();
const rows = page.getByTestId("settings-token-row");
await expect(rows.first()).toBeVisible();
```

The auto-waiting `expect(rows.first()).toBeVisible()` polls until either a row appears (test passes) or the timeout fires (test fails for a real reason). The race is closed at the assertion shape, not by adding sleeps or retries.

This is especially load-bearing for pages whose outer shell is SSR-prefetched (e.g. `/settings` post-slice-249 via `HydrationBoundary`) because the outer testid becomes visible on the first byte of HTML, leaving the inner data-fetch with zero head-start.

See `docs/audit-log/274-settings-ac9-token-row-flake-decisions.md` for the full diagnosis.

## How to debug a failure via the trace viewer

When CI fails, the Playwright job uploads `web/playwright-report/` and `web/test-results/` as a workflow artifact named `playwright-report`. Download it, then:

```sh
npx playwright show-trace path/to/trace.zip
```

The trace viewer shows network calls, console logs, DOM snapshots at every step, and screenshots — most failures are diagnosable inside the viewer without rerunning.

Locally, traces are written to `web/test-results/` automatically on failure (`trace: "retain-on-failure"` in `playwright.config.ts`).

## What the auth fixture does and how to override it

`e2e/fixtures.ts` exposes a `test.extend<{ authedPage }>` fixture. Its job is to make `authedPage` a `Page` that's already signed in before any `await authedPage.goto(...)` call. It chooses the auth path based on env:

| Env vars set                             | Path taken                                                                                 |
| ---------------------------------------- | ------------------------------------------------------------------------------------------ |
| `TEST_BEARER`                            | Inject the bearer as a `sa_session_token` cookie directly. Fastest. Recommended for local. |
| `TEST_USER_EMAIL` + `TEST_USER_PASSWORD` | POST `/auth/login` and let the platform set the session cookie. What CI uses.              |
| Neither                                  | Fixture throws — there's no sensible default for "not signed in".                          |

To override per-test, do not modify the fixture — write your own `test.extend(...)` in a sibling file. The shared fixture stays narrow.

**Hard rule:** every token referenced in this directory or in CI env must be a neutral test string (`test-*`). No vendor-prefixed tokens (`ghp_*`, `sk_*`, `eyJ*`, `AKIA*`) — GitGuardian flags those even inside test files (slice 069 P0-A9).
