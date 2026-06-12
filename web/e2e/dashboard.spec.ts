// Slice 040 — Playwright E2E for the program dashboard view.
// Slice 082 — wired `seedFromFixture("dashboard")` in `beforeAll` and
//             shipped the FULL `fixtures/e2e/dashboard.sql` (risk + 2
//             drift snapshots + 2 freshness rows + 1 expiring exception);
//             assertion bodies were left commented per decision 2 (per-
//             spec un-comment slices) pending soak.
// Slice 147 — re-pointed framework-posture + activity-feed panels off
//             `MissingEndpointPanel` onto the slice-066 backend reads.
// Slice 157 — re-pointed upcoming + top-risks panels onto the slice-066
//             unified-rollup + residual,age-sorted endpoints (the gap
//             footers `upcoming-gap` + `top-risks-sort-gap` retired).
// Slice 111 — soak gate satisfied (56 consecutive clean post-082 runs of
//             `Frontend · Playwright e2e` confirmed 2026-05-21); this
//             slice un-skips every assertion body against the FULL seed
//             coverage. Auth is now via the slice-069 `authedPage`
//             fixture (TEST_BEARER mode injects the session cookie at
//             worker scope, so the in-spec `/login` form fills are
//             retired). Sibling per-spec slices follow: 112
//             (control-detail), 113 (audit-workspace), 114 (risk-
//             hierarchy), 115 (admin-bootstrap). Slice 116 promotes the
//             job to required-checks after all five spec un-skips +
//             ≥5 clean runs each.
// Slice 193 — AC-5 ("upcoming panel binds to /v1/upcoming") failed on
//             slice 111's first post-rebase CI run. Diagnosis verdict:
//             H1 fixture gap — `fixtures/e2e/dashboard.sql` seeded the
//             exception with `status='approved'`, but `/v1/upcoming`
//             (`ListUpcomingItems`) filters on `status='active'`. Fix
//             flipped the status + switched to `ON CONFLICT (id) DO
//             UPDATE` per slice 168 precedent. See
//             `docs/audit-log/193-dashboard-upcoming-fixture-decisions.md`.
//             This preamble note also serves as the `web/**` touchpoint
//             that fires the CI Playwright path filter — without it,
//             a fixture-only change leaves the binding gate
//             un-exercised (filed as follow-on observation; the path
//             filter should add `fixtures/**` so future
//             fixture-only PRs trigger the e2e job).
//
// Run locally:
//   cd web
//   npx playwright install chromium     # once per machine
//   npx playwright test e2e/dashboard.spec.ts

import { seedFromFixture } from "./seed";

import { fulfillFromGolden } from "./test-utils/fulfill-from-golden";

import { expect, test } from "./fixtures";

test.describe("dashboard view", () => {
  test.beforeAll(() => {
    seedFromFixture("dashboard");
  });

  // Slice 380: the dashboard now prefetches all six panels server-side
  // (`Promise.all` in dashboard-prefetch.ts) and ships them via
  // HydrationBoundary, so on a cold load the client fires no
  // `/api/dashboard/*` request. Every test in THIS file, however,
  // asserts the CLIENT-side binding contract: that the panel's BFF
  // route is hit, or that a Playwright `page.route(...)` browser-side
  // mock shapes the panel (the slice-229 subtitle empty/error states +
  // the AC-7 degrade-independently test). Those browser-side mocks
  // cannot intercept the SSR prefetch. We therefore set the test-only
  // `e2e_no_prefetch` cookie (honored only under ATLAS_TEST_MODE=1, per
  // dashboard-prefetch.ts `serverPrefetchBypassed`) so the layout skips
  // the SSR prefetch and the page uses its pure client-side `useQuery`
  // path -- exactly the surface these tests exercise. The SSR fan-out
  // itself is covered by the sibling
  // `dashboard-server-component.spec.ts`. See decisions log D6.
  test.beforeEach(async ({ authedPage: page, baseURL }) => {
    await page.context().addCookies([
      {
        name: "e2e_no_prefetch",
        value: "1",
        url: baseURL ?? "http://localhost:3000",
      },
    ]);
  });

  test("AC-1: /dashboard renders the full program dashboard layout", async ({
    authedPage: page,
  }) => {
    await page.goto("/dashboard");
    await expect(page.getByTestId("program-dashboard")).toBeVisible();
    await expect(page.getByTestId("framework-posture-panel")).toBeVisible();
    await expect(page.getByTestId("top-risks-panel")).toBeVisible();
    await expect(page.getByTestId("evidence-freshness-panel")).toBeVisible();
    await expect(page.getByTestId("recent-drift-panel")).toBeVisible();
    await expect(page.getByTestId("upcoming-panel")).toBeVisible();
    await expect(page.getByTestId("activity-feed-panel")).toBeVisible();
  });

  test("AC-2: framework posture tiles bind to /v1/frameworks/posture (slice 147)", async ({
    authedPage: page,
  }) => {
    // Slice 147: the framework posture panel was originally a
    // MissingEndpointPanel placeholder (slice 041/060 precedent). Slice 066
    // shipped the backend endpoint; slice 147 re-pointed the panel.
    //
    // P0-DASH-1: the literal "does not exist on main yet" string MUST NOT
    // render anywhere in the dashboard code path.
    const requests: string[] = [];
    page.on("request", (r) => requests.push(r.url()));
    await page.goto("/dashboard");
    await expect(page.getByTestId("framework-posture-panel")).toBeVisible();
    // The placeholder copy is gone:
    await expect(
      page.getByTestId("framework-posture-panel-placeholder"),
    ).toHaveCount(0);
    await expect(page.getByTestId("framework-posture-panel")).not.toContainText(
      "does not exist on main yet",
    );
    // The BFF route was called:
    expect(
      requests.filter((u) => u.includes("/api/dashboard/framework-posture"))
        .length,
    ).toBeGreaterThan(0);
    // Real tiles render (or empty-state, never a placeholder):
    const tiles = page.getByTestId("framework-tile");
    const emptyState = page.getByTestId("framework-posture-empty");
    await expect(tiles.or(emptyState).first()).toBeVisible();
  });

  test("AC-3: top risks panel binds to /v1/risks?treatment=mitigate&sort=residual,age (slice 157)", async ({
    authedPage: page,
  }) => {
    // Slice 157: the top-risks panel was wired by slice 040 to the
    // unsorted treatment=mitigate list with a labelled `top-risks-sort-gap`
    // footer naming the residual,age ranking as a follow-up backend gap.
    // Slice 066 shipped the server-side sort; slice 157 re-points the
    // panel onto it and drops the gap footer.
    //
    // P0-148-3: the `top-risks-sort-gap` testid MUST NOT render after
    // slice 157.
    const requests: string[] = [];
    page.on("request", (r) => requests.push(r.url()));
    await page.goto("/dashboard");
    await expect(page.getByTestId("top-risk-row").first()).toBeVisible();
    expect(
      requests.filter((u) => u.includes("/api/dashboard/risks")).length,
    ).toBeGreaterThan(0);
    // The gap footer is gone (slice 157):
    await expect(page.getByTestId("top-risks-sort-gap")).toHaveCount(0);
  });

  test("AC-4: recent drift panel binds to /v1/controls/drift?since=7d", async ({
    authedPage: page,
  }) => {
    // The panel's BFF route forwards to GET /v1/controls/drift?since=7d.
    // Flipped-out controls render with their last-passing date, and the
    // signed window delta is shown.
    const requests: string[] = [];
    page.on("request", (r) => requests.push(r.url()));
    await page.goto("/dashboard");
    await expect(page.getByTestId("recent-drift-row").first()).toBeVisible();
    await expect(page.getByTestId("drift-delta")).toBeVisible();
    expect(
      requests.filter((u) => u.includes("/api/dashboard/drift")).length,
    ).toBeGreaterThan(0);
  });

  test("AC-5: upcoming panel binds to /v1/upcoming unified rollup (slice 157)", async ({
    authedPage: page,
  }) => {
    // Slice 157: the upcoming panel was wired by slice 040 to
    // `/v1/exceptions/expiring?within=30d` (the only real source on
    // main at slice-040 time) with a labelled `upcoming-gap` footer
    // naming the unified-rollup endpoint as a follow-up gap. Slice 066
    // shipped the unified rollup; slice 157 re-points the panel onto
    // it (categories: exception / policy_ack / vendor_review /
    // audit_period).
    //
    // P0-148-3: the `upcoming-gap` testid MUST NOT render after slice
    // 157, and the panel renders the unified rollup row shape with a
    // category badge per row.
    const requests: string[] = [];
    page.on("request", (r) => requests.push(r.url()));
    await page.goto("/dashboard");
    await expect(page.getByTestId("upcoming-row").first()).toBeVisible();
    await expect(
      page.getByTestId("upcoming-row-category").first(),
    ).toBeVisible();
    expect(
      requests.filter((u) => u.includes("/api/dashboard/upcoming")).length,
    ).toBeGreaterThan(0);
    // The gap footer is gone (slice 157):
    await expect(page.getByTestId("upcoming-gap")).toHaveCount(0);
  });

  test("evidence freshness panel binds to /v1/evidence/freshness", async ({
    authedPage: page,
  }) => {
    // The panel's BFF route forwards to GET /v1/evidence/freshness.
    // Per-class fresh/stale bars render plus the tenant-wide stale total.
    await page.goto("/dashboard");
    await expect(page.getByTestId("freshness-bucket").first()).toBeVisible();
    await expect(
      page.getByTestId("evidence-freshness-stale-total"),
    ).toBeVisible();
  });

  test("AC-6: activity feed binds to /v1/activity (slice 147)", async ({
    authedPage: page,
  }) => {
    // Slice 147: the activity feed panel was originally a
    // MissingEndpointPanel placeholder. Slice 066 shipped the backend
    // endpoint (reading slice-062's admin_audit_log_v evidence branch);
    // slice 147 re-pointed the panel.
    //
    // P0-DASH-1: the literal "does not exist on main yet" string MUST NOT
    // render anywhere in the dashboard code path.
    const requests: string[] = [];
    page.on("request", (r) => requests.push(r.url()));
    await page.goto("/dashboard");
    await expect(page.getByTestId("activity-feed-panel")).toBeVisible();
    await expect(
      page.getByTestId("activity-feed-panel-placeholder"),
    ).toHaveCount(0);
    await expect(page.getByTestId("activity-feed-panel")).not.toContainText(
      "does not exist on main yet",
    );
    expect(
      requests.filter((u) => u.includes("/api/dashboard/activity")).length,
    ).toBeGreaterThan(0);
    // Real rows render (or empty-state, never a placeholder):
    const rows = page.getByTestId("activity-feed-row");
    const emptyState = page.getByTestId("activity-feed-empty");
    await expect(rows.or(emptyState).first()).toBeVisible();
  });

  test("AC-2/AC-3 slice 667: no inert filter chips or dev placeholder note", async ({
    authedPage: page,
  }) => {
    // Slice 667: the All/Evidence/Controls/Approvals chips were inert
    // (no handler) and carried a developer-facing placeholder note
    // ("Filter chips activate once...") duplicated 4x in `title`. The
    // dashboard /v1/activity endpoint surfaces only the evidence branch
    // and takes no kind/source filter, so the chips had nothing to bind
    // to. Per the JUDGMENT decision they are hidden, not wired.
    await page.goto("/dashboard");
    await expect(page.getByTestId("activity-feed-panel")).toBeVisible();
    // No empty chip row renders:
    await expect(page.getByTestId("activity-filter-chip")).toHaveCount(0);
    await expect(page.getByTestId("activity-feed-filters")).toHaveCount(0);
    // The internal "activate once" placeholder note is gone from the DOM:
    await expect(page.getByTestId("activity-feed-panel")).not.toContainText(
      "activate once",
    );
  });

  test("AC-1 P0-DASH-1: no 'does not exist on main yet' copy anywhere on the dashboard", async ({
    authedPage: page,
  }) => {
    // Whole-page guard: slice 147 must remove the literal placeholder
    // string from the entire dashboard surface, not just the two panels.
    await page.goto("/dashboard");
    await expect(page.getByTestId("program-dashboard")).not.toContainText(
      "does not exist on main yet",
    );
  });

  test("AC-7: panels degrade independently — slow/failing API skeletons + retry", async ({
    authedPage: page,
  }) => {
    // A failing endpoint degrades only its own panel; the others still
    // render. The page never blocks on a single slow API (P0-2).
    await page.route("**/api/dashboard/drift", (r) => r.abort());
    await page.goto("/dashboard");
    // The drift panel shows its own error with a retry affordance...
    await expect(page.getByTestId("recent-drift-panel-error")).toBeVisible();
    await expect(page.getByTestId("recent-drift-panel-retry")).toBeVisible();
    // ...while the other bound panels still resolve (slice 147 adds two more
    // bound panels to the degrade-independently contract).
    await expect(page.getByTestId("top-risks-panel")).toBeVisible();
    await expect(page.getByTestId("evidence-freshness-panel")).toBeVisible();
    await expect(page.getByTestId("upcoming-panel")).toBeVisible();
    await expect(page.getByTestId("framework-posture-panel")).toBeVisible();
    await expect(page.getByTestId("activity-feed-panel")).toBeVisible();
    // And while a query is in flight the panel shows its own skeleton:
    await page.route("**/api/dashboard/freshness", async (r) => {
      await new Promise((res) => setTimeout(res, 1500));
      await r.continue();
    });
    await page.goto("/dashboard");
    await expect(
      page.getByTestId("evidence-freshness-panel-loading"),
    ).toBeVisible();
  });

  test("responsive: layout collapses to a single column at 375px", async ({
    authedPage: page,
  }) => {
    await page.setViewportSize({ width: 375, height: 812 });
    await page.goto("/dashboard");
    // The lg: grid columns collapse — every panel stacks and stays
    // visible at the 375px baseline (slice 060 set this baseline).
    await expect(page.getByTestId("top-risks-panel")).toBeVisible();
    await expect(page.getByTestId("evidence-freshness-panel")).toBeVisible();
    await expect(page.getByTestId("recent-drift-panel")).toBeVisible();
    await expect(page.getByTestId("upcoming-panel")).toBeVisible();
  });

  test("auth: a 401 from a bound endpoint bounces to /login", async ({
    authedPage: page,
  }) => {
    // With no session cookie the (authed) layout redirects before the
    // page renders; a cookie that expires mid-session is caught by the
    // page's 401 -> /login effect.
    await page.context().clearCookies();
    await page.goto("/dashboard");
    await expect(page).toHaveURL(/\/login/);
  });

  // ----- Slice 229 — dashboard header tenant + snapshot subtitle -----
  //
  // The slice adds a tenant-name chip next to the H1 and a subtitle
  // bound to the freshness pct (with skeleton on loading, "Snapshot
  // unavailable" on error, "No evidence ingested yet" on empty). The
  // pure-helper branches (compute / format) are pinned by the vitest
  // sibling `dashboard-header-subtitle.test.ts`; the e2e here pins the
  // integrated render under mocked BFF payloads (the audits-header
  // spec's canonical pattern for chrome that depends on /api/me/tenants
  // — fixture seeding tenant rows is out of scope for this slice).

  test("AC-1 (slice 229): tenant-name chip renders next to the H1 from /api/me/tenants", async ({
    authedPage: page,
  }) => {
    await page.route("**/api/me/tenants", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          tenants: [
            {
              id: "11111111-1111-1111-1111-111111111111",
              name: "Sentinel Labs",
              current: true,
            },
          ],
        }),
      });
    });
    await page.goto("/dashboard");
    const chip = page.getByTestId("dashboard-header-tenant-context");
    await expect(chip).toBeVisible();
    await expect(chip).toHaveText("Sentinel Labs");
  });

  test("AC-2 (slice 229): subtitle renders 'evidence freshness {pct}% within window' from the freshness endpoint", async ({
    authedPage: page,
  }) => {
    // Slice 394: load the freshness body from the recorded golden, then
    // OVERRIDE the numbers to a deterministic 87% (100 - 13/100 stale) so
    // the visible copy is predictable regardless of seed-fixture drift.
    // The golden supplies the recorded shape (`bucket: "class"`, the array
    // contract); the override pins only the numbers the assertion reads —
    // the AC-3 populated-with-override escape hatch (decisions log D3).
    await page.route("**/api/dashboard/freshness", (route) =>
      fulfillFromGolden(route, "freshness", "populated", {
        override: {
          buckets: [
            { freshness_class: "monthly", total: 100, fresh: 87, stale: 13 },
          ],
          total: 100,
          total_stale: 13,
        },
      }),
    );
    await page.goto("/dashboard");
    const subtitle = page.getByTestId("dashboard-header-subtitle");
    await expect(subtitle).toBeVisible();
    await expect(subtitle).toHaveText("evidence freshness 87% within window");
  });

  test("AC-4 (slice 229): subtitle renders 'Snapshot unavailable' when the freshness endpoint errors", async ({
    authedPage: page,
  }) => {
    await page.route("**/api/dashboard/freshness", (r) => r.abort());
    await page.goto("/dashboard");
    const errSubtitle = page.getByTestId("dashboard-header-subtitle-error");
    await expect(errSubtitle).toBeVisible();
    await expect(errSubtitle).toHaveText("Snapshot unavailable");
  });

  test("AC-5 (slice 229): subtitle renders 'No evidence ingested yet' when total === 0", async ({
    authedPage: page,
  }) => {
    // Slice 394: the empty-set body IS a recorded golden variant
    // (`freshness:empty` — the array-vs-null contract 409 D3 pins). Load
    // it instead of hand-writing `{buckets: [], total: 0}`.
    await page.route("**/api/dashboard/freshness", (route) =>
      fulfillFromGolden(route, "freshness", "empty"),
    );
    await page.goto("/dashboard");
    const subtitle = page.getByTestId("dashboard-header-subtitle");
    await expect(subtitle).toBeVisible();
    await expect(subtitle).toHaveText("No evidence ingested yet");
  });

  test("P0-229-2 (slice 229): the subtitle does NOT show '100% fresh of 0' when total === 0", async ({
    authedPage: page,
  }) => {
    // Anti-criterion: when total === 0 we MUST NOT show "100%" or
    // "100% fresh" anywhere in the subtitle region.
    // Slice 394: same recorded empty-set golden as the AC-5 case above.
    await page.route("**/api/dashboard/freshness", (route) =>
      fulfillFromGolden(route, "freshness", "empty"),
    );
    await page.goto("/dashboard");
    const subtitle = page.getByTestId("dashboard-header-subtitle");
    await expect(subtitle).toBeVisible();
    await expect(subtitle).not.toContainText("100%");
    await expect(subtitle).not.toContainText("100% fresh");
  });

  test("P0-229-1 (slice 229): the subtitle does NOT render the prior generic marketing copy", async ({
    authedPage: page,
  }) => {
    // Anti-criterion enforcement: slice 229 removes the generic
    // "The home screen for the security program — live posture, drift,
    // risk, and what is coming up." copy.
    await page.goto("/dashboard");
    const dashboard = page.getByTestId("program-dashboard");
    await expect(dashboard).not.toContainText(
      "The home screen for the security program",
    );
  });

  // ----- Slice 359 — a11y skip-link in the authed layout -----
  //
  // Closes slice 331 audit finding A11Y-1 (Critical). The authed
  // layout renders a visually-hidden skip-link as its first focusable
  // element; tabbing once from page load focuses it; pressing Enter
  // moves focus into `<main id="main-content" tabIndex={-1}>`. WCAG
  // SC 2.4.1 Bypass Blocks (Level A) + SC 2.4.7 Focus Visible.

  test("AC-1/2/3/4 (slice 359): skip-link is the first focusable element and moves focus to <main> on activation", async ({
    authedPage: page,
  }) => {
    await page.goto("/dashboard");

    // AC-2: a single Tab from page load focuses the skip-link.
    await page.keyboard.press("Tab");
    const skipLink = page.locator('a[href="#main-content"]');
    await expect(skipLink).toBeFocused();

    // AC-3: when focused, the skip-link is no longer visually hidden
    // (the `focus:not-sr-only` utility makes it visible). Playwright's
    // `toBeVisible()` excludes `sr-only`-style off-screen elements, so
    // a focused skip-link being `visible` is the focus-visible signal.
    await expect(skipLink).toBeVisible();

    // AC-2 (continued): activating the link with Enter moves focus
    // to the `<main>` content region.
    await page.keyboard.press("Enter");
    const main = page.locator("main#main-content");
    await expect(main).toBeFocused();
  });
});
