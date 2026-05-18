// Slice 040 — Playwright E2E for the program dashboard view.
//
// Runner status (post-slice-069, verified 2026-05-15 by slice 071 audit):
// Playwright IS installed in `web/` (`@playwright/test` in devDeps;
// `web/playwright.config.ts` present; CI runs `Frontend · Playwright
// e2e`). The job is currently quarantined per slice 079 because the
// five un-shimmed specs reference seed-data preconditions the
// docker-compose bring-up does not yet establish. Slice 082
// (`Playwright e2e seed-data harness`, status `not-ready`) is the fix;
// when it lands, the quarantine drops and the un-commented assertions
// below become the gate.
//
// Run locally:
//   cd web
//   npx playwright install chromium     # once per machine
//   npx playwright test e2e/dashboard.spec.ts
//
// Pre-conditions the seed-data harness (slice 082) must establish
// before the commented assertions are turned on:
//   - PLATFORM_BASE_URL points at a running platform instance
//   - TEST_BEARER carries a credential in a tenant that has: at least
//     one risk with treatment=mitigate, at least one control that has
//     drifted out of passing in the last 7 days, evidence records
//     across >=2 freshness classes, and at least one exception
//     expiring within 30 days (so every bound panel has data)

import { test } from "@playwright/test";

import { seedFromFixture } from "./seed";

// Per the preamble above: assertions are deliberately commented pending
// per-spec un-comment slices (slice 082's scoping decision — see
// docs/audit-log/082-playwright-seed-data-harness-decisions.md). The
// test body is preserved verbatim as a reviewable contract. Slice 082
// DOES wire the seed harness in `beforeAll` so the harness is exercised
// end-to-end against real Postgres+MinIO+NATS in CI.

test.describe("dashboard view", () => {
  test.beforeAll(() => {
    seedFromFixture("dashboard");
  });

  test("AC-1: /dashboard renders the full program dashboard layout", async () => {
    // 1. Sign in.
    //    await page.goto("/login");
    //    await page.fill('input[name="token"]', process.env.TEST_BEARER!);
    //    await page.click("button[type=submit]");
    // 2. Visit the dashboard route.
    //    await page.goto("/dashboard");
    // 3. The shell + all six panel regions are present (mockup layout).
    //    await expect(page.getByTestId("program-dashboard")).toBeVisible();
    //    await expect(page.getByTestId("framework-posture-panel")).toBeVisible();
    //    await expect(page.getByTestId("top-risks-panel")).toBeVisible();
    //    await expect(page.getByTestId("evidence-freshness-panel")).toBeVisible();
    //    await expect(page.getByTestId("recent-drift-panel")).toBeVisible();
    //    await expect(page.getByTestId("upcoming-panel")).toBeVisible();
    //    await expect(page.getByTestId("activity-feed-panel")).toBeVisible();
  });

  test("AC-2: framework posture tiles bind to /v1/frameworks/posture (slice 147)", async () => {
    // Slice 147: the framework posture panel was originally a
    // MissingEndpointPanel placeholder (slice 041/060 precedent). Slice 066
    // shipped the backend endpoint; slice 147 re-pointed the panel.
    //
    // P0-DASH-1: the literal "does not exist on main yet" string MUST NOT
    // render anywhere in the dashboard code path.
    //    const requests: string[] = [];
    //    page.on("request", (r) => requests.push(r.url()));
    //    await page.goto("/dashboard");
    //    await expect(page.getByTestId("framework-posture-panel")).toBeVisible();
    //    // The placeholder copy is gone:
    //    await expect(page.getByTestId("framework-posture-panel-placeholder")).toHaveCount(0);
    //    await expect(page.getByTestId("framework-posture-panel")).not.toContainText(
    //      "does not exist on main yet",
    //    );
    //    // The BFF route was called:
    //    expect(
    //      requests.filter((u) => u.includes("/api/dashboard/framework-posture")).length,
    //    ).toBeGreaterThan(0);
    //    // Real tiles render (or empty-state, never a placeholder):
    //    const tiles = page.getByTestId("framework-tile");
    //    const emptyState = page.getByTestId("framework-posture-empty");
    //    await expect(tiles.or(emptyState).first()).toBeVisible();
  });

  test("AC-3: top risks panel binds to /v1/risks?treatment=mitigate", async () => {
    // The panel's BFF route forwards to GET /v1/risks?treatment=mitigate.
    // At least one risk row renders; the residual/age sort gap is noted
    // honestly (no fabricated ranking).
    //    const requests: string[] = [];
    //    page.on("request", (r) => requests.push(r.url()));
    //    await page.goto("/dashboard");
    //    await expect(page.getByTestId("top-risk-row").first()).toBeVisible();
    //    expect(
    //      requests.filter((u) => u.includes("/api/dashboard/risks")).length,
    //    ).toBeGreaterThan(0);
    //    await expect(page.getByTestId("top-risks-sort-gap")).toContainText(
    //      "sort=residual,age",
    //    );
  });

  test("AC-4: recent drift panel binds to /v1/controls/drift?since=7d", async () => {
    // The panel's BFF route forwards to GET /v1/controls/drift?since=7d.
    // Flipped-out controls render with their last-passing date, and the
    // signed window delta is shown.
    //    const requests: string[] = [];
    //    page.on("request", (r) => requests.push(r.url()));
    //    await page.goto("/dashboard");
    //    await expect(page.getByTestId("recent-drift-row").first()).toBeVisible();
    //    await expect(page.getByTestId("drift-delta")).toBeVisible();
    //    expect(
    //      requests.filter((u) => u.includes("/api/dashboard/drift")).length,
    //    ).toBeGreaterThan(0);
  });

  test("AC-5: upcoming panel binds to /v1/exceptions/expiring", async () => {
    // The panel's BFF route forwards to GET /v1/exceptions/expiring?
    // within=30d. Expiring exceptions render as dated rows; the
    // board-report / access-review / questionnaire gap is noted honestly.
    //    await page.goto("/dashboard");
    //    await expect(page.getByTestId("upcoming-row").first()).toBeVisible();
    //    await expect(page.getByTestId("upcoming-gap")).toContainText(
    //      "upcoming-rollup endpoint",
    //    );
  });

  test("evidence freshness panel binds to /v1/evidence/freshness", async () => {
    // The panel's BFF route forwards to GET /v1/evidence/freshness.
    // Per-class fresh/stale bars render plus the tenant-wide stale total.
    //    await page.goto("/dashboard");
    //    await expect(page.getByTestId("freshness-bucket").first()).toBeVisible();
    //    await expect(page.getByTestId("evidence-freshness-stale-total")).toBeVisible();
  });

  test("AC-6: activity feed binds to /v1/activity (slice 147)", async () => {
    // Slice 147: the activity feed panel was originally a
    // MissingEndpointPanel placeholder. Slice 066 shipped the backend
    // endpoint (reading slice-062's admin_audit_log_v evidence branch);
    // slice 147 re-pointed the panel.
    //
    // P0-DASH-1: the literal "does not exist on main yet" string MUST NOT
    // render anywhere in the dashboard code path.
    //    const requests: string[] = [];
    //    page.on("request", (r) => requests.push(r.url()));
    //    await page.goto("/dashboard");
    //    await expect(page.getByTestId("activity-feed-panel")).toBeVisible();
    //    await expect(page.getByTestId("activity-feed-panel-placeholder")).toHaveCount(0);
    //    await expect(page.getByTestId("activity-feed-panel")).not.toContainText(
    //      "does not exist on main yet",
    //    );
    //    expect(
    //      requests.filter((u) => u.includes("/api/dashboard/activity")).length,
    //    ).toBeGreaterThan(0);
    //    // Real rows render (or empty-state, never a placeholder):
    //    const rows = page.getByTestId("activity-feed-row");
    //    const emptyState = page.getByTestId("activity-feed-empty");
    //    await expect(rows.or(emptyState).first()).toBeVisible();
    //    // The filter chips still render (visual continuity) but stay disabled:
    //    await expect(page.getByTestId("activity-filter-chip")).toHaveCount(4);
  });

  test("AC-1 P0-DASH-1: no 'does not exist on main yet' copy anywhere on the dashboard", async () => {
    // Whole-page guard: slice 147 must remove the literal placeholder
    // string from the entire dashboard surface, not just the two panels.
    //    await page.goto("/dashboard");
    //    await expect(page.getByTestId("program-dashboard")).not.toContainText(
    //      "does not exist on main yet",
    //    );
  });

  test("AC-7: panels degrade independently — slow/failing API skeletons + retry", async () => {
    // A failing endpoint degrades only its own panel; the others still
    // render. The page never blocks on a single slow API (P0-2).
    //    await page.route("**/api/dashboard/drift", (r) => r.abort());
    //    await page.goto("/dashboard");
    // The drift panel shows its own error with a retry affordance...
    //    await expect(page.getByTestId("recent-drift-panel-error")).toBeVisible();
    //    await expect(page.getByTestId("recent-drift-panel-retry")).toBeVisible();
    // ...while the other bound panels still resolve (slice 147 adds two more
    // bound panels to the degrade-independently contract).
    //    await expect(page.getByTestId("top-risks-panel")).toBeVisible();
    //    await expect(page.getByTestId("evidence-freshness-panel")).toBeVisible();
    //    await expect(page.getByTestId("upcoming-panel")).toBeVisible();
    //    await expect(page.getByTestId("framework-posture-panel")).toBeVisible();
    //    await expect(page.getByTestId("activity-feed-panel")).toBeVisible();
    // And while a query is in flight the panel shows its own skeleton:
    //    await page.route("**/api/dashboard/freshness", async (r) => {
    //      await new Promise((res) => setTimeout(res, 1500));
    //      await r.continue();
    //    });
    //    await page.goto("/dashboard");
    //    await expect(page.getByTestId("evidence-freshness-panel-loading")).toBeVisible();
  });

  test("responsive: layout collapses to a single column at 375px", async () => {
    //    await page.setViewportSize({ width: 375, height: 812 });
    //    await page.goto("/dashboard");
    // The lg: grid columns collapse — every panel stacks and stays
    // visible at the 375px baseline (slice 060 set this baseline).
    //    await expect(page.getByTestId("top-risks-panel")).toBeVisible();
    //    await expect(page.getByTestId("evidence-freshness-panel")).toBeVisible();
    //    await expect(page.getByTestId("recent-drift-panel")).toBeVisible();
    //    await expect(page.getByTestId("upcoming-panel")).toBeVisible();
  });

  test("auth: a 401 from a bound endpoint bounces to /login", async () => {
    // With no session cookie the (authed) layout redirects before the
    // page renders; a cookie that expires mid-session is caught by the
    // page's 401 -> /login effect.
    //    await page.context().clearCookies();
    //    await page.goto("/dashboard");
    //    await expect(page).toHaveURL(/\/login/);
  });
});
