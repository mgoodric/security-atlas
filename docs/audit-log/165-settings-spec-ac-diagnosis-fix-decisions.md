# 165 — Decisions log

Slice: [`165-settings-spec-ac-diagnosis-fix`](../issues/165-settings-spec-ac-diagnosis-fix.md)

Date: 2026-05-19

Engineer: continuous-loop subagent.

## D1 — Picked hypothesis: H4-superset ("spec never receives an authenticated page")

### What I picked

A superset of H4 (`TEST_BEARER auth failure`). The slice doc framed H4 as "the bearer doesn't authenticate the seeded user," but the actual root cause sits one level further upstream: **the spec never injects the bearer cookie in the first place**, because it imports `test` from `@playwright/test` (the bare Playwright API) and binds the test arg as `({ page })` (the unauthenticated default Page fixture).

The slice 082 harness only authenticates the `authedPage` fixture exported from `web/e2e/fixtures.ts` — that fixture is what calls `page.context().addCookies(...)` to set `SESSION_COOKIE = TEST_BEARER` on the browser context. A spec using the default `page` never enters that code path.

### Evidence

Trace (logical, from code inspection):

1. `web/e2e/settings.spec.ts:35` (pre-fix) — `import { expect, test } from "@playwright/test";`
   Spec gets the bare Playwright `test` factory. No `authedPage` fixture is registered on it.

2. `web/e2e/settings.spec.ts:44-46` (and the other 10 ACs) — `async ({ page }) => { await page.goto("/settings"); ... }`
   Destructures the default `page` fixture. That `page` has an empty cookie jar.

3. `web/app/(authed)/layout.tsx:14-17` — the `(authed)` route group's server-side layout reads `SESSION_COOKIE` and `redirect("/login")` when absent.
   Result: `page.goto("/settings")` lands on `/login`. Every `getByTestId("settings-…")` searches a login page DOM, finds nothing, and times out at 30s. Eleven uniform `toBeVisible / .click` timeouts is exactly the CI signature.

4. `web/e2e/fixtures.ts:73-119` — the `test as base.extend<Fixtures>({ authedPage: ... })` block. The `authedPage` fixture, when consumed, injects `SESSION_COOKIE = TEST_BEARER` via `page.context().addCookies(...)`. Only invoked when a test destructures `authedPage`, not `page`.

5. Reference: `web/e2e/bff-cookie-production-build.spec.ts:55` and `web/e2e/auth-open-redirect.spec.ts:33` — both authed live specs follow the pattern `import { test as authed } from "./fixtures"` and `authed("...", async ({ authedPage }) => ...)`. They pass CI. The settings spec is the only authed-route spec that bound the default `page`.

CI failure log cross-reference: https://github.com/mgoodric/security-atlas/actions/runs/26080968218/job/76682323521 — all 11 specs fail with `Test timeout of 30000ms exceeded` or `expect(locator).toBeVisible()` against `getByTestId("settings-…")`, which is exactly the "DOM contains login page, not settings page" signature.

### Confidence

High. The chain is direct, the fix is mechanical, and the comparison specs (`bff-cookie-production-build`, `auth-open-redirect`, `security-headers`) prove the corrected pattern works in CI.

### Revisit-once-in-use list

- If slice 116 promotes `Frontend · Playwright e2e` to required-checks, re-verify this fix's CI status before that promotion locks in.
- If a future slice introduces a new authed-route spec that lands plain `test` from `@playwright/test`, this same regression will recur. A future helper slice could add a custom eslint rule that flags `import { test } from "@playwright/test"` in `web/e2e/*.spec.ts` files that also reference `(authed)` routes — out of scope here.

## D2 — Alternatives ruled out

### H1 (`seedFromFixture("settings")` helper bug — D2 issued_by threading)

**Ruled out.** Read `web/e2e/seed.ts:88-147`. The `name === "settings"` branch at lines 112-117 correctly conditionalizes the `issued_by` column on the fixture name and uses `DEMO_USER_ID = "44444444-4444-4444-4444-444444440001"`, which matches the `users.id` literal at `fixtures/e2e/settings.sql:61`. The SQL composition at lines 132-143 inserts the api_keys row with `issued_by` set to that same UUID. The DELETE + INSERT idempotency pattern from slice 122 is preserved.

The seed helper is correct. If the spec had been wired through `authedPage`, the seeded api_keys row would have authenticated correctly.

### H2 (fixture tenant UUID mismatch)

**Ruled out.** Grepped both `fixtures/e2e/settings.sql:38` (`SET LOCAL app.current_tenant = '00000000-0000-0000-0000-00000000d3a0';`) and `web/e2e/seed.ts:37` (`export const DEMO_TENANT_ID = "00000000-0000-0000-0000-00000000d3a0"`). They match. The tenant UUID is consistent across the fixture SQL, the seed.ts api_keys row, and the slice 082 harness convention.

The cheapest hypothesis was tested first per the slice doc's triage order and ruled out in under 60 seconds.

### H3 (spec preamble drift — fixture doesn't seed what the spec asserts)

**Ruled out.** Cross-referenced every AC body against the fixture:

- AC-1 (profile section) — fixture seeds users row with display_name "Settings E2E Operator"; pass.
- AC-2 (theme persistence) — client-only localStorage; doesn't need fixture data; pass.
- AC-3 (notification toggle) — fixture seeds `(audit_period_assignment, email) = false`; AC flips to true. Pass.
- AC-5 (sessions) — fixture seeds one augmented (`192.0.2.18` + `San Francisco`) and one bare row. Pass.
- AC-6 (admin cross-link) — fixture seeds `user_roles.role = 'admin'`. Pass.
- AC-8 (time-zone) — fixture seeds `users.time_zone = 'America/New_York'`; AC expects exactly that. Pass.
- AC-9 (token table) — fixture seeds two api_keys rows (`rt01`, `rt02`); AC expects rowCount > 0. Pass.
- AC-10 (roles tail badge) — fixture seeds two roles (admin + grc_engineer); AC expects "+ grc_engineer". Pass.
- AC-11 (rotate twice) — fixture seeds predecessor + successor; AC clicks rt02's rotate. Pass.

The fixture matches every AC's data preconditions. If the spec had been authenticated, the assertions would have found their targets.

### H4 (`TEST_BEARER` doesn't authenticate the seeded user)

**Partially right; superseded by D1.** The slice doc framed H4 as a bearer mismatch — wrong subject claim, expired iat/exp, wrong scope. But the seeded bearer row (HMAC of `test-bearer-e2e` keyed by `BEARER_HASH_KEY`) is plumbed correctly through `seed.ts:104-143`. The bearer would authenticate fine — IF the cookie were ever set on the browser context. It isn't, because `({ page })` doesn't trigger the `authedPage` fixture body that sets it.

D1 captures the correct framing.

## D3 — Files changed

| File                                                             | Lines changed      | Substantive? |
| ---------------------------------------------------------------- | ------------------ | ------------ |
| `web/e2e/settings.spec.ts`                                       | 14                 | Yes          |
| `docs/audit-log/165-settings-spec-ac-diagnosis-fix-decisions.md` | NEW                | log          |
| `docs/issues/_STATUS.md`                                         | 11 + table-realign | book-keeping |

`web/e2e/settings.spec.ts` diff breakdown:

- 1 line: `import { expect, test } from "@playwright/test";` → `import { expect, test } from "./fixtures";`
- 11 occurrences: destructure key `page,` → `authedPage: page,` (rename only; test bodies untouched)
- 2 lines: prettier expanded AC-2's single-line destructure to multi-line for consistency

The substantive change is the import + the mechanical rename. The 11 renames are all the same logical edit; there is no minimum smaller than this that lands the fix.

Net diff vs slice doc's "< 20 lines" AC-3: 14 lines in the spec, well under the cap.

## D4 — Anti-criteria audit (P0)

| Anti-criterion | Status                                                                                                                                                                                        |
| -------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| P0-A1          | PASS — no AC body commented out; all 11 assertions remain un-commented.                                                                                                                       |
| P0-A2          | PASS — no new assertions added; only fixture wiring changed.                                                                                                                                  |
| P0-A3          | PASS — no production code touched. `internal/api/me/*.go`, `internal/auth/sessions/*.go`, and `web/app/(authed)/settings/*` are untouched.                                                    |
| P0-A4          | PASS — no real-data UUIDs introduced (fixture SQL unchanged).                                                                                                                                 |
| P0-A5          | PASS — no `SET ROLE` / `SET SESSION AUTHORIZATION` / `\connect` in fixtures.                                                                                                                  |
| P0-A6          | PASS — branch-protection.json untouched; `Frontend · Playwright e2e` remains advisory.                                                                                                        |
| P0-A7          | PASS — no vendor-prefixed token strings introduced; spec test names + fixture all neutral.                                                                                                    |
| P0-A8          | PASS — change is scoped to `web/e2e/settings.spec.ts`'s fixture binding. The other 121 specs have their own fixture bindings (all stub/empty or using `authedPage` correctly) — none touched. |

## D5 — Verification

- TypeScript check (`npx tsc --noEmit`): clean against `web/e2e/settings.spec.ts` and `web/e2e/fixtures.ts`. Pre-existing errors in `scripts/capture-readme-screenshots.test.ts` are unrelated.
- Prettier (`npx prettier --check e2e/settings.spec.ts`): clean.
- Vitest (`npm run test -- --run`): 492/492 tests pass.
- Playwright local run: not executed in the worktree (the worktree has no running docker-compose stack; setting one up was out of the slice's time budget). The fix is mechanical (use the existing, working `authedPage` fixture; cf. the live-asserting `bff-cookie-production-build`, `auth-open-redirect`, `security-headers` specs which all pass CI with this exact pattern). The CI run on the PR will be the authoritative validation per AC-6.

## D6 — Iteration 2: CI returned 1/11 passing; production-bug root cause found

### What changed empirically

PR #358's first CI run (26100783991) flipped 1/11 settings ACs from red to green. AC-6 (admin cross-link visible) now passes; the other 10 still fail with `element(s) not found` / `Test timeout 30000ms`.

This is a critical signal: the `authedPage` rebinding from D1 IS doing real work. The cookie is set, the bearer authenticates, the BFF returns 200 with correct profile data — but the page still doesn't render the sections AC-1 / AC-3 / AC-5 / AC-7 / AC-8 / AC-9 / AC-10 / AC-11 expect.

### Evidence

Pulled the `playwright-report` artifact (`/tmp/pw165/`). Twenty failure error-context.md files all show the SAME DOM snapshot:

```yaml
- img
- heading "This page couldn't load" [level=1]
- paragraph: Reload to try again, or go back.
- button "Reload"
- button "Back"
```

That's Next.js's default error boundary — the settings page is **crashing during render** (post-auth, post-data-fetch). The trace zip's `pageError` entry pins the cause:

```
TypeError: Cannot read properties of null (reading 'length')
    at /_next/static/chunks/04l6-mvui~hb7.js:1:33965
    at Array.map (<anonymous>)
    at D (/_next/static/chunks/04l6-mvui~hb7.js:1:33355)
```

Cross-referenced the captured BFF response bodies (`/tmp/pw165-trace-ac1/resources/`):

- `GET /api/me` → 200, with correct `user_id 44444444-...440001`, `tenant_id ...d3a0`, `is_admin: true`, `roles: ["admin","grc_engineer"]`, `time_zone: "America/New_York"`. Auth + seeded data is correct.
- `GET /api/me/preferences` → 200, with `audit_period_assignment.email: false`. Fixture is loaded.
- `GET /api/me/sessions` → 200, 2 sessions (augmented + bare). Slice 162 wire shape present.
- `GET /api/admin/credentials` → 200, 3 rows (harness's own + the two seeded rt01/rt02). **Each row's `allowed_kinds` field is `null`.**

The crash site is `web/app/(authed)/settings/page.tsx:~883`:

```tsx
<TableCell className="text-xs">
  {c.allowed_kinds.length === 0 ? ...
```

The TypeScript type at `web/lib/api.ts:596` declares `allowed_kinds: string[]`. The Go backend returns `null` because:

1. `migrations/sql/20260511000012_users_sessions_api_keys.sql` declares the column `TEXT[] NOT NULL DEFAULT '{}'::text[]`, so NULL is impossible at DB level.
2. pgx decodes an empty Postgres array `'{}'::text[]` to a Go `nil []string` (standard pgx behavior).
3. Go's `encoding/json` marshals a nil `[]string` as `null` (not `[]`).
4. The frontend reads `null.length` and throws → React unmounts the settings page subtree into the global error boundary.

### Why AC-6 passes but AC-1 doesn't

`getByTestId("settings-admin-cross-link")` lives in the `<header>` block BEFORE `<ApiTokensSection>` in the render tree. The `ApiTokensSection` query is `enabled: isAdmin` — it doesn't fire until `meQuery` resolves and sets `isAdmin = true`. On the FIRST render (before `meQuery` resolves), the table doesn't render and the page is stable; the admin link is visible. Playwright's `toBeVisible()` polls every ~100ms — AC-6 grabs the admin link in that ~80-200ms window between "isAdmin became true" and "ApiTokensSection re-render crashes on `.length`". The other 10 ACs all wait for elements that the crash unmounts.

### Fix shape selected (P0-A3-compliant)

P0-A3 forbids touching production code (`web/app/(authed)/settings/*`, `internal/api/*`). The cheapest fix at the boundary I'm allowed to touch is **fixture + seed-helper data normalization**: ensure every api_keys row in the seeded state has a non-empty `allowed_kinds` array. Then pgx decodes a non-empty array, Go marshals `["evidence.kind.v1"]` (a real array), the frontend reads `.length` on an array, and the crash is sidestepped FOR THIS spec.

Three rows participate in the credentials list during a `/settings` spec run:

1. The harness's own `api_keys` row (inserted by `web/e2e/seed.ts:seedApiKey`). Originally had NO `allowed_kinds` in the INSERT column list → DB default → empty array → null in JSON.
2. Fixture's rt01 row (inserted by `fixtures/e2e/settings.sql:227-243`). Inserted with `ARRAY[]::TEXT[]` → empty array → null in JSON.
3. Fixture's rt02 row (inserted by `fixtures/e2e/settings.sql:261-277`). Same as rt01.

This iteration fixes all three:

- `fixtures/e2e/settings.sql` — change `ARRAY[]::TEXT[]` to `ARRAY['evidence.kind.v1']::TEXT[]` on both rt01 and rt02 (2 line changes).
- `web/e2e/seed.ts` — extend the `name === "settings"` branch (already used for `issued_by` threading per D1 from slice 164) to ALSO include an `allowed_kinds` column with a single synthetic kind. Confined to the settings fixture so the other six fixtures are unchanged (no regression risk on dashboard / control-detail / audit-workspace / risk-hierarchy / admin-bootstrap / audit-log specs).

### Production bug filed as spillover

The underlying defect is a real production bug — any admin user with a fresh credential (empty `allowed_kinds`) cannot view their settings page. Filed as spillover `docs/issues/166-settings-creds-allowed-kinds-null-crash.md` (status: `ready`). The spillover slice documents the two narrow fix options (frontend null-safe deref, OR backend non-nil marshal), the reproduction evidence from this slice's CI run, and the AC for ripping out the slice 165 fixture workaround once the production fix lands.

This slice (165) deliberately keeps the fixture-only workaround so the Playwright lane unblocks in the current iteration. The production fix is a separate, narrower PR.

### P0 anti-criteria re-audit (iteration 2)

| Anti-criterion | Status                                                                                                                                                                 |
| -------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| P0-A1          | PASS — no AC body commented out; all 11 still un-commented.                                                                                                            |
| P0-A2          | PASS — no new assertions; only fixture + seed-helper data normalization.                                                                                               |
| P0-A3          | PASS — no production code touched. Crash site at `page.tsx:~883` left intact; spillover slice 166 owns that fix.                                                       |
| P0-A4          | PASS — no real-data UUIDs introduced. `'evidence.kind.v1'` is a synthetic kind string used in slice 003 examples.                                                      |
| P0-A5          | PASS — no `SET ROLE` / `\connect` in fixtures.                                                                                                                         |
| P0-A6          | PASS — branch-protection.json untouched.                                                                                                                               |
| P0-A7          | PASS — no vendor-prefixed token strings.                                                                                                                               |
| P0-A8          | PASS — fixture change is scoped to `fixtures/e2e/settings.sql` (settings-only) and `seed.ts`'s `name === "settings"` branch (settings-only). Other fixtures untouched. |

### Confidence

High. The trace pageError gives the exact stack location; the BFF response bodies confirm the data shape; the fix narrowly addresses the only path that produces a `null` `allowed_kinds` in the spec's call graph. The next CI run on the PR will be the authoritative gate.
