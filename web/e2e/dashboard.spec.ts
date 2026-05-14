// Slice 040 — Playwright E2E for the program dashboard view.
//
// Like slice 041's control-detail.spec.ts and slice 060's
// admin-bootstrap.spec.ts, this spec lives AHEAD of the Playwright
// runner — `web/` has no @playwright/test installed yet (adding it
// touches web/package.json, a spine file, and is a shared follow-up
// across the frontend slices). The spec is written under the same
// `ifPlaywright` shim so:
//   * `npm run typecheck` stays green (e2e/ is in tsconfig "exclude")
//   * `npm run lint` stays green (eslint ignores e2e/)
//   * the file is a precise, reviewable contract of the intended
//     end-to-end assertions, one test per acceptance criterion
//
// To run once Playwright lands (manual smoke today):
//   cd web
//   npm install --save-dev @playwright/test
//   npx playwright install chromium
//   npx playwright test e2e/dashboard.spec.ts
//
// Pre-conditions to wire when Playwright is installed:
//   - PLATFORM_BASE_URL points at a running platform instance
//   - TEST_BEARER carries a credential in a tenant that has: at least
//     one risk with treatment=mitigate, at least one control that has
//     drifted out of passing in the last 7 days, evidence records
//     across >=2 freshness classes, and at least one exception
//     expiring within 30 days (so every bound panel has data)

/* eslint-disable @typescript-eslint/no-unused-vars */

// import { test, expect } from "@playwright/test";

type Test = (name: string, fn: () => Promise<void> | void) => void;
type Expect = <T>(actual: T) => {
  toBeVisible(): Promise<void>;
  toBeHidden(): Promise<void>;
  toContainText(substr: string): Promise<void>;
  toHaveText(s: string | RegExp): Promise<void>;
  toHaveAttribute(name: string, value: string): Promise<void>;
  toHaveCount(n: number): Promise<void>;
  toHaveURL(re: RegExp): Promise<void>;
  toBeGreaterThan(n: number): void;
  not: {
    toBeVisible(): Promise<void>;
    toContainText(substr: string): Promise<void>;
  };
};

declare const test: Test;
declare const expect: Expect;

function ifPlaywright(_fn: () => void) {
  // No-op shim until Playwright lands. Keeps this file a static contract.
}

ifPlaywright(() => {
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

  test("AC-2: framework posture tiles render as an endpoint-naming placeholder", async () => {
    // There is no per-framework posture endpoint on main; the tiles ship
    // as an endpoint-naming placeholder (slice 041/060 precedent) with a
    // six-slot data-free scaffold. No fabricated percentages or arrows.
    //    await page.goto("/dashboard");
    //    await expect(page.getByTestId("framework-posture-placeholder")).toBeVisible();
    //    await expect(page.getByTestId("framework-posture-placeholder")).toContainText(
    //      "/v1/frameworks/posture",
    //    );
    //    await expect(page.getByTestId("framework-tile")).toHaveCount(6);
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

  test("AC-6: activity feed renders as an endpoint-naming placeholder", async () => {
    // There is no NATS event-stream archive read endpoint on main; the
    // feed ships as an endpoint-naming placeholder (slice 041/060
    // precedent) with disabled filter chips. No fabricated activity rows.
    //    await page.goto("/dashboard");
    //    await expect(page.getByTestId("activity-feed-placeholder")).toBeVisible();
    //    await expect(page.getByTestId("activity-feed-placeholder")).toContainText(
    //      "/v1/activity",
    //    );
    //    await expect(page.getByTestId("activity-filter-chip")).toHaveCount(4);
  });

  test("AC-7: panels degrade independently — slow/failing API skeletons + retry", async () => {
    // A failing endpoint degrades only its own panel; the others still
    // render. The page never blocks on a single slow API (P0-2).
    //    await page.route("**/api/dashboard/drift", (r) => r.abort());
    //    await page.goto("/dashboard");
    // The drift panel shows its own error with a retry affordance...
    //    await expect(page.getByTestId("recent-drift-panel-error")).toBeVisible();
    //    await expect(page.getByTestId("recent-drift-panel-retry")).toBeVisible();
    // ...while the other bound panels still resolve.
    //    await expect(page.getByTestId("top-risks-panel")).toBeVisible();
    //    await expect(page.getByTestId("evidence-freshness-panel")).toBeVisible();
    //    await expect(page.getByTestId("upcoming-panel")).toBeVisible();
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

// Module marker — keeps this file a module under TS strict mode.
export {};
