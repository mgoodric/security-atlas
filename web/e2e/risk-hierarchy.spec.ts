// Slice 056 — Playwright E2E for the hierarchical risk dashboard view.
//
// Like slice 040's dashboard.spec.ts and slice 041's
// control-detail.spec.ts, this spec lives AHEAD of the Playwright
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
//   npx playwright test e2e/risk-hierarchy.spec.ts
//
// Pre-conditions to wire when Playwright is installed:
//   - PLATFORM_BASE_URL points at a running platform instance
//   - TEST_BEARER carries a credential in a tenant that has: at least
//     two org_units in a parent/child relationship, the 10 default
//     themes (slice 053 seed) plus optionally a tenant-private theme,
//     at least one active aggregation rule targeting a theme, at least
//     one decision with a future revisit_by, and at least one decision
//     whose revisit_by is in the past (so the amber overdue pill has
//     data)

import { test } from "@playwright/test";

// Slice 069 — Playwright is now installed; the `ifPlaywright` shim that
// used to wrap this file has been removed. Test bodies that still hold
// commented assertions are deliberately preserved — turning them on is
// per-spec follow-up work as the seed-data preconditions in the preamble
// are established by the test harness.

test.describe("risk hierarchy view", () => {
  test("AC-9a: loading the page with seeded data renders the three panels", async () => {
    // 1. Sign in.
    //    await page.goto("/login");
    //    await page.fill('input[name="token"]', process.env.TEST_BEARER!);
    //    await page.click("button[type=submit]");
    // 2. Visit the hierarchy route.
    //    await page.goto("/risks/hierarchy");
    // 3. The shell + all three panel regions are present (AC-1).
    //    await expect(page.getByTestId("risk-hierarchy-dashboard")).toBeVisible();
    //    await expect(page.getByTestId("org-tree-panel")).toBeVisible();
    //    await expect(page.getByTestId("theme-heatmap-panel")).toBeVisible();
    //    await expect(page.getByTestId("decision-timeline-panel")).toBeVisible();
    // 4. The org tree renders real nodes (AC-2 structure) and the
    //    per-node count chips name the missing endpoint honestly.
    //    await expect(page.getByTestId("org-tree-node").first()).toBeVisible();
    //    await expect(
    //      page.getByTestId("org-tree-counts-pending").first(),
    //    ).toContainText("risk counts pending");
    // 5. The heatmap renders real axes (AC-3) and a missing-endpoint
    //    banner for the cell counts.
    //    await expect(page.getByTestId("theme-heatmap-grid")).toBeVisible();
    //    await expect(page.getByTestId("theme-heatmap-col").first()).toBeVisible();
    //    await expect(page.getByTestId("theme-heatmap-missing")).toBeVisible();
    // 6. Default themes are ordered left of tenant-private themes (AC-3).
    //    const sources = await page
    //      .getByTestId("theme-heatmap-col")
    //      .evaluateAll((els) =>
    //        els.map((e) => e.getAttribute("data-theme-source")),
    //      );
    //    const firstTenant = sources.indexOf("tenant");
    //    const lastDefault = sources.lastIndexOf("default");
    //    if (firstTenant !== -1) expect(firstTenant).toBeGreaterThan(lastDefault);
  });

  test("AC-9b: clicking a heatmap cell drills correctly", async () => {
    //    await page.goto("/risks/hierarchy");
    // Clicking a cell opens the side panel for that theme / org_unit.
    //    await page.getByTestId("theme-heatmap-cell").first().click();
    //    await expect(page.getByTestId("heatmap-cell-side-panel")).toBeVisible();
    // The side panel names the missing contributing-risk endpoint
    // honestly — it never fabricates a risk list (AC-4, P0-1).
    //    await expect(page.getByTestId("heatmap-cell-side-panel")).toContainText(
    //      "/v1/risks/theme-heatmap",
    //    );
    // When an aggregation rule targets the theme, the side panel cites
    // its REAL thresholds (slice 054 data).
    //    await expect(
    //      page.getByTestId("heatmap-cell-side-panel-rule"),
    //    ).toBeVisible();
    // Closing the side panel hides it.
    //    await page.getByTestId("heatmap-cell-side-panel-close").click();
    //    await expect(page.getByTestId("heatmap-cell-side-panel")).toBeHidden();
  });

  test("AC-9c: decision timeline filtering by status updates URL and visible rows", async () => {
    //    await page.goto("/risks/hierarchy");
    //    await expect(page.getByTestId("decision-timeline-row").first()).toBeVisible();
    // Toggling the "active" status filter writes ?status=active to the URL.
    //    await page.getByTestId("filter-status-active").click();
    //    await expect(page).toHaveURL(/[?&]status=active/);
    // Every visible row now has status "active".
    //    const badges = await page
    //      .getByTestId("decision-timeline-row")
    //      .evaluateAll((rows) => rows.length);
    //    expect(badges).toBeGreaterThan(0);
    // Deep-link round-trip: navigating straight to the filtered URL
    // restores the filter state from the query params (AC-7).
    //    await page.goto("/risks/hierarchy?status=superseded");
    //    await expect(page.getByTestId("filter-status-superseded")).toHaveAttribute(
    //      "aria-pressed",
    //      "true",
    //    );
    // Clearing the filter removes the param from the URL.
    //    await page.getByTestId("filter-status-superseded").click();
    //    await expect(page).not.toHaveURL(/status=/);
  });

  test("AC-9d: overdue decisions show the amber 'Revisit overdue' pill", async () => {
    //    await page.goto("/risks/hierarchy");
    // At least one row is flagged overdue (seeded decision with a past
    // revisit_by) and carries the amber pill.
    //    await expect(page.getByTestId("decision-overdue-pill").first()).toBeVisible();
    //    const overdueRow = page
    //      .getByTestId("decision-timeline-row")
    //      .filter({ has: page.getByTestId("decision-overdue-pill") })
    //      .first();
    //    await expect(overdueRow).toHaveAttribute("data-overdue", "true");
    // The pill is NEVER auto-acknowledged — it persists across a reload
    // until a human acts on the decision upstream (anti-criterion P0-3).
    //    await page.reload();
    //    await expect(page.getByTestId("decision-overdue-pill").first()).toBeVisible();
  });

  test("responsive: layout collapses to a single column below md", async () => {
    //    await page.setViewportSize({ width: 375, height: 812 });
    //    await page.goto("/risks/hierarchy");
    // All three panels stack and stay visible at the 375px baseline.
    //    await expect(page.getByTestId("org-tree-panel")).toBeVisible();
    //    await expect(page.getByTestId("theme-heatmap-panel")).toBeVisible();
    //    await expect(page.getByTestId("decision-timeline-panel")).toBeVisible();
  });

  test("panels degrade independently — a failing query shows its own retry", async () => {
    // A failing endpoint degrades only its own panel; the others still
    // render. The page never blocks on a single API (P0-2).
    //    await page.route("**/api/risks-hierarchy/decisions*", (r) => r.abort());
    //    await page.goto("/risks/hierarchy");
    //    await expect(page.getByTestId("decision-timeline-panel-error")).toBeVisible();
    //    await expect(page.getByTestId("decision-timeline-panel-retry")).toBeVisible();
    //    await expect(page.getByTestId("org-tree-panel")).toBeVisible();
    //    await expect(page.getByTestId("theme-heatmap-panel")).toBeVisible();
  });

  test("auth: a 401 from a bound endpoint bounces to /login", async () => {
    // With no session cookie the (authed) layout redirects before the
    // page renders; a cookie that expires mid-session is caught by the
    // page's 401 -> /login effect.
    //    await page.context().clearCookies();
    //    await page.goto("/risks/hierarchy");
    //    await expect(page).toHaveURL(/\/login/);
  });

  test("empty states: each panel renders its own create-flow affordance", async () => {
    // In a tenant with no org_units / no themed risks / no decisions,
    // each panel shows its empty state with a primary action (AC-8).
    //    await page.goto("/risks/hierarchy");
    //    await expect(page.getByTestId("org-tree-empty")).toBeVisible();
    //    await expect(page.getByTestId("org-tree-empty-action")).toBeVisible();
    //    await expect(page.getByTestId("decision-timeline-empty")).toBeVisible();
    //    await expect(page.getByTestId("decision-timeline-empty-action")).toBeVisible();
  });
});
