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

| File                      | What it covers                                                                                                                                                                                                                                                |
| ------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `fixtures.ts`             | Shared `test.extend<{ authedPage }>` — signs in once per worker via `TEST_BEARER` or email/password env                                                                                                                                                       |
| `dashboard.spec.ts`       | `/dashboard` — six bound panels, framework-posture placeholder, freshness binding, drift, top risks                                                                                                                                                           |
| `control-detail.spec.ts`  | `/controls/:id` — coverage table, UCF mini-viz, evidence stream, freshness clock, effective-scope, OOS rows                                                                                                                                                   |
| `audit-workspace.spec.ts` | `/audit` — auditor flow: period bar, control nav, sampling, walkthrough, comments (shared/private)                                                                                                                                                            |
| `risk-hierarchy.spec.ts`  | `/risks/hierarchy` — org tree, theme heatmap, decision timeline, overdue pills, deep-linked filters                                                                                                                                                           |
| `admin-bootstrap.spec.ts` | `/admin/**` — admin tiles, SSO discovery, feature toggle, API key issuance, role matrix                                                                                                                                                                       |
| `controls-list.spec.ts`   | `/controls` — anchor table renders, Family pill narrows, empty state, row→detail nav, multi-select + select-all, bulk assign-owner round-trip, saved filter-views (save/re-apply/duplicate-name 409). Seeded by `fixtures/e2e/controls-list.sql` (slice 743). |

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

### Gating the FIRST visibility assertion on a network round-trip

Slice 274's fix covers the SECOND assertion onward — once a parent testid is visible, downstream snapshots should auto-wait. Slice 275 extends the pattern to the FIRST visibility assertion when the page has a load-bearing useQuery in its mount path.

**The shape of the bug** (slice 275 — `control-detail-tabs.spec.ts`): the page renders a `<Skeleton data-testid="control-detail-loading" />` branch while `coverageQ.isLoading`. The tablist (`data-testid="control-tabs"`) renders only AFTER `coverageQ` settles. Under CI load (2 parallel workers, shared docker stack), the mount sequence (Suspense fallback → mount → useSearchParams resolved → useQuery fires → fetch → React commit) can exceed Playwright's default 5s `toBeVisible` timeout. The default `await expect(tablist).toBeVisible()` polls for 5s and times out before the skeleton transitions.

**The fix**: gate the first visibility assertion on the network round-trip that drives the relevant query, and lift the timeout to 30s as a CI-load backstop.

**Bad** (slice 275 root cause):

```ts
test("AC-1", async ({ authedPage: page }) => {
  await page.goto(`/controls/${seeded.controlId}`);
  await expect(page.getByTestId("control-tabs")).toBeVisible();
  // ... 5s timeout; mount sequence under CI load exceeds it; fails.
});
```

**Good** (slice 275 fix):

```ts
async function gotoControlDetail(page: Page, opts: { tab?: string } = {}) {
  const url = opts.tab
    ? `/controls/${seeded.controlId}?tab=${opts.tab}`
    : `/controls/${seeded.controlId}`;
  const coverageResp = page.waitForResponse(
    (r) =>
      r.url().includes(`/api/controls/${seeded.controlId}/coverage`) &&
      r.status() === 200,
    { timeout: 30_000 },
  );
  await page.goto(url);
  await coverageResp;
}

test("AC-1", async ({ authedPage: page }) => {
  await gotoControlDetail(page);
  await expect(page.getByTestId("control-tabs")).toBeVisible({
    timeout: 30_000,
  });
  // ... waitForResponse closes the race; the 30s timeout is a backstop.
});
```

Two notes on the pattern:

1. The `waitForResponse` Promise MUST be set up BEFORE the `page.goto` (a Playwright invariant). The helper above does this; do the same when inlining the pattern in a test body.

2. Use `waitForResponse` (not `waitForRequest`) so the helper resolves only AFTER the response is delivered — which is the actual gate `useQuery` is waiting on.

See `docs/audit-log/275-slice-254-tabs-e2e-fix-decisions.md` for the full diagnosis.

Honest correction (slice 276): slice 275's `gotoControlDetail` helper is the right SHAPE, but slice 275's root-cause diagnosis for `control-detail-tabs.spec.ts` was wrong — the actual cause was a missing required field in the mocked `/coverage` payload that crashed the production `<UcfMiniViz>` component's `req.title.slice()` call. See the next subsection.

### Mock payload schema-conformance

Mocked `route.fulfill` payloads MUST satisfy the producer-side TypeScript type, not just the subset of fields the test asserts on. The page consumes more of the type than the test reads, and a missing required field becomes a runtime crash hidden inside the spec's own scaffold.

**The shape of the bug** (slice 276 — `control-detail-tabs.spec.ts`): the spec's `coverage` mock provided `requirement_text` on each requirement row but NOT `title` (the field the `CoverageRequirement` type in `web/lib/api.ts` actually declares). The page's `<UcfMiniViz>` component calls `req.title.slice(0, 34)` directly; with `req.title === undefined`, the `.slice()` throws a `TypeError`, the React render tree above crashes, the page-level error boundary catches it, and the page body is replaced with a generic `"This page couldn't load"` fallback. Every Playwright assertion on `control-tabs` / `control-tab-panel-overview` / etc. then times out — the testid never renders.

The diagnosis was hidden because:

- The screenshot at failure shows a generic chrome-error-looking page; it's easy to mis-read as a navigation failure.
- The Playwright `expect(...).toBeVisible()` timeout error message is identical whether the page is slow OR crashed.
- A test that deep-links to a tab that doesn't mount the crashing component (e.g. `?tab=policies` — Policies panel never instantiates `<UcfMiniViz>`) passes in <2s, suggesting the page CAN render fast — which made the "slow mount" hypothesis (slice 275's H1) look plausible.

**The fix**: align the mocked payload with the producer-side type contract. Every required field gets a deterministic value.

**Bad** (slice 254 / slice 275 mock, pre-fix):

```ts
requirements: [
  {
    framework_version_id: seeded.frameworkVersionId,
    framework_name: "SOC 2",
    framework_version: "2017",
    requirement_id: "CC6.6",
    requirement_text: "logical access controls", // ← NOT a CoverageRequirement field
    relationship_type: "equal",
    strength: 1.0,
    coverage: 0.94,
  },
];
```

**Good** (slice 276 fix):

```ts
requirements: [
  {
    edge_id: "00000000-0000-0000-0000-0000000000e1",
    requirement_id: "CC6.6",
    code: "CC6.6", // ← required by CoverageRequirement
    title: "Logical access controls", // ← required, was the .slice() crash site
    framework_slug: "soc2", // ← required
    framework_name: "SOC 2",
    framework_version: "2017",
    framework_version_id: seeded.frameworkVersionId,
    framework_version_status: "active", // ← required
    relationship_type: "equal",
    strength: 1.0,
    coverage: 0.94,
    source_attribution: "scf", // ← required
  },
];
```

Two debugging heuristics for the next reader hit by this class of bug:

1. **If a Playwright test fails with `expect(locator).toBeVisible()` timeout AND the page screenshot shows `"This page couldn't load"` (or any generic error fallback), the FIRST step is to read the trace's `pageError` events.** The trace viewer surfaces them; the `error-context.md` ARIA snapshot often shows the error-boundary chrome. A `TypeError: Cannot read properties of undefined (reading 'X')` inside `Array.map` is the classic signature of a missing-required-field mock.

2. **When authoring a new mock, import the producer-side type and use it.** TypeScript's structural type system happily accepts a partial mock (it sees the literal as a wider type when assigned to `body: string`); discipline lives in the author. The cheapest discipline is to annotate the payload object with the producer type:

   ```ts
   const body: ControlCoverage = {
     control: {
       /* ... */
     },
     anchor: {
       /* ... */
     },
     requirements: [
       /* ... */
     ],
   };
   await route.fulfill({ status: 200, body: JSON.stringify(body) });
   ```

   The compiler then flags missing required fields at build time, not at trace-decode time.

See `docs/audit-log/276-control-detail-tabs-deep-fix-decisions.md` for the full diagnosis.

## Golden-backed route mocks (slice 394)

For the nine BFF↔atlas endpoints that have a recorded **contract golden** under `web/lib/contracts/*.golden.json` (slices 349/392 + 409), DO NOT hand-write a `route.fulfill` body. Load the recorded body via `fulfillFromGolden` (`web/e2e/test-utils/fulfill-from-golden.ts`) so the e2e mock cannot drift from the provider's recorded wire shape — the slice-334 P-1 / slice-276 mock-vs-reality drift this whole tier (ADR-0007) exists to catch.

The nine golden-covered endpoints (`GoldenEndpoint` union in the helper): `me`, `version`, `install-state`, `demo-status`, `framework-posture`, `activity`, `upcoming`, `freshness`, `drift`.

```ts
import { fulfillFromGolden } from "./test-utils/fulfill-from-golden";

// Happy path — serve the recorded body verbatim:
await page.route("**/api/install-state", (route) =>
  fulfillFromGolden(route, "install-state", "fresh_install_without_tenant"),
);

// Empty-set is a recorded variant — use it, don't hand-write `[]`:
await page.route("**/api/dashboard/freshness", (route) =>
  fulfillFromGolden(route, "freshness", "empty"),
);
```

The caller still owns `page.route(pattern, …)`, the URL glob, and any method-guard (`route.request().method() !== "GET"` → `route.fallback()`). The helper owns only the `route.fulfill` of the recorded body.

### The escape hatch (when the golden does not carry what you need)

The goldens are happy-path 200 bodies with `populated` + `empty` variants. Three cases stay hand-written or use the override path:

1. **Error states (4xx/5xx)** — there is no recorded body for an error, so keep a hand-written `route.fulfill({ status, body })` (or `route.abort()`). `first-time-login.spec.ts`'s 503-fallback test is the canonical example.
2. **A populated body that needs one spec-specific value** — pass `options.override`. The golden stays the shape-complete base; only the named top-level keys change. The credential-bearer specs override `display_name` (the visible assertion reads the formatted credential label); the dashboard slice-229 subtitle test overrides the freshness numbers to a deterministic 87%. This keeps the spec from drifting on the **shape** while pinning the one value the assertion needs.
3. **Routes with no golden** (`/v1/risks`, `/v1/controls/*`, `/v1/board`, `/v1/policies` — goldens tracked as #410 / #411) — hand-write as before; the typed `GoldenEndpoint` union mechanically prevents passing an uncovered endpoint to the helper. When a golden for one of these lands, record it, add the endpoint to the union, and migrate the mock.

The helper's pure logic is unit-tested at `web/lib/contracts/fulfill-from-golden.test.ts` (vitest — it imports node `fs`, not a browser; the helper's only Playwright import is `import type { Route }`, erased at runtime). See `docs/audit-log/394-e2e-fulfill-from-golden-decisions.md` for the full design rationale.

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
