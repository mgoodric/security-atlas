# Playwright end-to-end tests

This directory holds the security-atlas web app's Playwright e2e suite. Each spec is one user-flow contract; the assertions in a spec drive the BFF + RSC paths against a real platform bring-up.

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
