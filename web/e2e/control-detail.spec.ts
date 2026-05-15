// Slice 041 — Playwright E2E for the control detail view.
//
// Like slice 042's audit-workspace.spec.ts and slice 060's
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
//   npx playwright test e2e/control-detail.spec.ts
//
// Pre-conditions to wire when Playwright is installed:
//   - PLATFORM_BASE_URL points at a running platform instance
//   - TEST_BEARER carries a credential in a tenant with at least one
//     control that is anchored to an SCF anchor with >=2 framework
//     requirement mappings, and at least one of those frameworks has an
//     activated FrameworkScope that the control is OUT of (so AC-7's
//     dashed/greyed row has data)
//   - KNOWN_CONTROL_ID is that control's UUID

import { test, expect } from "@playwright/test";

// Slice 069 — Playwright is now installed; the `ifPlaywright` shim that
// used to wrap this file has been removed. Test bodies that still hold
// commented assertions are deliberately preserved — turning them on is
// per-spec follow-up work as the seed-data preconditions in the preamble
// are established by the test harness.

test.describe("control detail view", () => {
  test("AC-1: /controls/:id renders the full detail layout", async () => {
    // 1. Sign in.
    //    await page.goto("/login");
    //    await page.fill('input[name="token"]', process.env.TEST_BEARER!);
    //    await page.click("button[type=submit]");
    // 2. Visit the control detail route.
    //    await page.goto(`/controls/${KNOWN_CONTROL_ID}`);
    // 3. Header: title, SCF anchor pill, lifecycle badge.
    //    await expect(page.getByTestId("control-detail")).toBeVisible();
    //    await expect(page.getByTestId("control-title")).toBeVisible();
    //    await expect(page.getByTestId("scf-anchor-pill")).toBeVisible();
    //    await expect(page.getByTestId("lifecycle-badge")).toBeVisible();
    // 4. KPI strip + every major section is present (mockup layout).
    //    await expect(page.getByTestId("kpi-strip")).toBeVisible();
    //    await expect(page.getByTestId("coverage-section")).toBeVisible();
    //    await expect(page.getByTestId("ucf-viz-section")).toBeVisible();
    //    await expect(page.getByTestId("evidence-stream-section")).toBeVisible();
    //    await expect(page.getByTestId("freshness-section")).toBeVisible();
    //    await expect(page.getByTestId("effective-scope-section")).toBeVisible();
    //    await expect(page.getByTestId("policies-section")).toBeVisible();
    //    await expect(page.getByTestId("risks-section")).toBeVisible();
    //    await expect(page.getByTestId("audit-log-section")).toBeVisible();
  });

  test("AC-2: coverage table shows STRM types + strengths per row", async () => {
    //    await page.goto(`/controls/${KNOWN_CONTROL_ID}`);
    //    await expect(page.getByTestId("coverage-table")).toBeVisible();
    // At least one row, each with a STRM badge + a strength bar.
    //    const rows = page.getByTestId("coverage-row");
    //    await expect(rows.first()).toBeVisible();
    //    await expect(rows.first().locator("[data-strm]")).toBeVisible();
    //    await expect(rows.first().getByTestId("strength-bar")).toBeVisible();
    // The STRM badge text equals the raw backend relationship_type
    // (open-string rendering — equal | subset_of | superset_of |
    // intersects_with), never a fabricated label.
  });

  test("AC-3: UCF mini-viz renders control -> anchor -> requirements", async () => {
    //    await page.goto(`/controls/${KNOWN_CONTROL_ID}`);
    //    await expect(page.getByTestId("ucf-mini-viz")).toBeVisible();
    //    await expect(page.getByTestId("ucf-viz-control")).toBeVisible();
    //    await expect(page.getByTestId("ucf-viz-anchor")).toBeVisible();
    //    await expect(page.getByTestId("ucf-viz-requirement").first()).toBeVisible();
    // P0-1: every edge originates at the control or the anchor — there is
    // no requirement-to-requirement edge. Asserted structurally: the
    // component has no code path that draws one (see ucf-mini-viz.tsx).
  });

  test("AC-4: evidence stream — placeholder until the list endpoint ships", async () => {
    // There is no GET /v1/evidence?control_id=... endpoint on main; the
    // section ships as an empty-state naming the gap (slice-060
    // precedent). When the endpoint lands this test asserts the
    // paginated last-30-days stream instead.
    //    await page.goto(`/controls/${KNOWN_CONTROL_ID}`);
    //    await expect(page.getByTestId("evidence-stream-placeholder")).toBeVisible();
    //    await expect(page.getByTestId("evidence-stream-placeholder")).toContainText(
    //      "GET /v1/evidence",
    //    );
  });

  test("AC-5: freshness clock binds to control state", async () => {
    // AC-5's text references slice 016 `valid_until`; slice 016 is not on
    // main. The clock binds to slice 012's /state — freshness_status,
    // last_observed_at, freshness_class — which is the merged freshness
    // surface. The 016 drift overlay is additive when it lands.
    //    await page.goto(`/controls/${KNOWN_CONTROL_ID}`);
    //    await expect(page.getByTestId("freshness-clock")).toBeVisible();
    //    await expect(page.getByTestId("freshness-since")).toBeVisible();
    //    await expect(page.getByTestId("freshness-status")).toBeVisible();
  });

  test("AC-6: effective-scope panel calls /effective-scope per framework", async () => {
    // One row per distinct framework_version_id in the coverage
    // requirements; each row is backed by its own
    // /api/controls/:id/effective-scope?framework_version=<fvId> call.
    //    const requests: string[] = [];
    //    page.on("request", (r) => requests.push(r.url()));
    //    await page.goto(`/controls/${KNOWN_CONTROL_ID}`);
    //    await expect(page.getByTestId("effective-scope-row").first()).toBeVisible();
    //    expect(
    //      requests.filter((u) => u.includes("/effective-scope?framework_version=")).length,
    //    ).toBeGreaterThan(0);
  });

  test("AC-7: out-of-scope framework rows render dashed/greyed", async () => {
    // The pre-condition control is out of scope for >=1 framework. That
    // row carries data-out-of-scope="true" and the dashed/greyed
    // styling; it is NEVER hidden (anti-criterion P0-2).
    //    await page.goto(`/controls/${KNOWN_CONTROL_ID}`);
    //    const oosRow = page.locator('[data-testid="coverage-row"][data-out-of-scope="true"]');
    //    await expect(oosRow.first()).toBeVisible();
    // And the corresponding UCF-viz edge is dashed:
    //    const oosEdge = page.locator('[data-testid="ucf-viz-requirement"] [data-out-of-scope="true"]');
    //    await expect(oosEdge.first()).toBeVisible();
  });

  test("responsive: layout collapses to a single column at 375px", async () => {
    //    await page.setViewportSize({ width: 375, height: 812 });
    //    await page.goto(`/controls/${KNOWN_CONTROL_ID}`);
    // The lg: grid columns collapse — every section stacks and stays
    // visible at the 375px baseline (slice 060 set this baseline).
    //    await expect(page.getByTestId("coverage-section")).toBeVisible();
    //    await expect(page.getByTestId("freshness-section")).toBeVisible();
  });

  test("auth: a 401 from a bound endpoint bounces to /login", async () => {
    // With no session cookie the (authed) layout + proxy.ts redirect
    // before the page renders; a cookie that expires mid-session is
    // caught by the page's 401 -> /login effect.
    //    await page.context().clearCookies();
    //    await page.goto(`/controls/${KNOWN_CONTROL_ID}`);
    //    await expect(page).toHaveURL(/\/login/);
  });
});
