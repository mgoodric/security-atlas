// Slice 056 — Playwright E2E for the hierarchical risk dashboard view.
//
// Seeded by fixtures/e2e/risk-hierarchy.sql: two tenants of the spec's own
// (#2755). Tenant H holds a parent/child org_unit pair, a tenant-private
// theme beside the 10 default themes, one active aggregation rule on the
// `ownership` theme, and three decisions (active with a future revisit,
// active and overdue, superseded). Tenant E holds nothing, for the empty
// states. The spec signs in with an admin JWT (web/e2e/tenant-session.ts).
// #2755 restored the assertions slice 082 left commented out, re-pointed
// at the page as it ships: the org tree shows real 5x5 risk counts, and
// the heatmap's cell counts and the side panel's risk list are ComingSoon
// lines (#2082).
//
// Run locally:
//   cd web
//   npx playwright install chromium     # once per machine
//   npx playwright test e2e/risk-hierarchy.spec.ts

import type { Page } from "@playwright/test";

import { expect, test } from "./fixtures";
import { seedFromFixture } from "./seed";
import { signInToTenant } from "./tenant-session";

// Mirror the tenants in fixtures/e2e/risk-hierarchy.sql.
const HIERARCHY_TENANT = "00000000-0000-0000-0000-2755b0300001";
const EMPTY_TENANT = "00000000-0000-0000-0000-2755b0300002";

async function signInTo(page: Page, tenantId: string): Promise<void> {
  await signInToTenant(page, { tenantId, roles: ["admin"], superAdmin: true });
}

test.describe("risk hierarchy view", () => {
  test.beforeAll(() => {
    seedFromFixture("risk-hierarchy");
  });

  test.beforeEach(async ({ page }) => {
    await signInTo(page, HIERARCHY_TENANT);
  });

  test("AC-9a: loading the page with seeded data renders the three panels", async ({
    page,
  }) => {
    await page.goto("/risks/hierarchy");
    // The shell + all three panel regions are present (AC-1).
    await expect(page.getByTestId("risk-hierarchy-dashboard")).toBeVisible();
    await expect(page.getByTestId("org-tree-panel")).toBeVisible();
    await expect(page.getByTestId("theme-heatmap-panel")).toBeVisible();
    await expect(page.getByTestId("decision-timeline-panel")).toBeVisible();
    // The org tree renders the seeded parent/child pair (AC-2), with the
    // per-node 5x5 risk count the platform reports (no risks seeded).
    // The child's <li> nests inside the parent's.
    const nodes = page.getByTestId("org-tree-node");
    await expect(nodes).toHaveCount(2);
    const engineering = nodes.first();
    await expect(engineering).toContainText("Engineering");
    await expect(engineering.getByTestId("org-tree-child-count")).toHaveText(
      "1 child",
    );
    const platform = engineering.getByTestId("org-tree-node");
    await expect(platform).toContainText("Platform Team");
    await expect(platform.getByTestId("org-tree-child-count")).toHaveCount(0);
    await expect(page.getByTestId("org-tree-risk-counts")).toHaveText([
      "5x5 0",
      "5x5 0",
    ]);
    // The heatmap renders real axes (AC-3): one row per org_unit, one
    // column per theme, and a ComingSoon line for the cell counts.
    await expect(page.getByTestId("theme-heatmap-grid")).toBeVisible();
    await expect(page.getByTestId("theme-heatmap-row")).toHaveCount(2);
    await expect(page.getByTestId("theme-heatmap-col")).toHaveCount(11);
    await expect(
      page.getByTestId("theme-heatmap-missing").getByTestId("coming-soon"),
    ).toBeVisible();
    // Default themes are ordered left of tenant-private themes (AC-3).
    const sources = await page
      .getByTestId("theme-heatmap-col")
      .evaluateAll((els) =>
        els.map((e) => e.getAttribute("data-theme-source")),
      );
    expect(sources.filter((s) => s === "default")).toHaveLength(10);
    expect(sources.indexOf("tenant")).toBe(10);
    await expect(page.getByTestId("theme-heatmap-col").last()).toContainText(
      "acme-private-theme",
    );
  });

  test("AC-9b: clicking a heatmap cell drills correctly", async ({ page }) => {
    await page.goto("/risks/hierarchy");
    // Clicking a cell opens the side panel for that theme / org_unit.
    await page
      .locator(
        '[data-testid="theme-heatmap-cell"][data-theme="ownership"][data-org-unit="00000000-0000-0000-0000-2755b0300102"]',
      )
      .click();
    const panel = page.getByTestId("heatmap-cell-side-panel");
    await expect(panel).toBeVisible();
    await expect(panel).toContainText("ownership · Platform Team");
    // It never fabricates a risk list: the list is a ComingSoon line
    // (AC-4, P0-1; #2082).
    await expect(panel.getByTestId("coming-soon")).toBeVisible();
    // The seeded rule targets the theme, so the panel cites its REAL
    // thresholds (slice 054 data).
    await expect(page.getByTestId("heatmap-cell-side-panel-rule")).toHaveText(
      "Aggregation rule ownership-aggregation-2026 targets this theme: fires at 3 risks across 2 org_units within a 90-day window.",
    );
    // Closing the side panel hides it.
    await page.getByTestId("heatmap-cell-side-panel-close").click();
    await expect(panel).toBeHidden();
  });

  test("AC-9c: decision timeline filtering by status updates URL and visible rows", async ({
    page,
  }) => {
    await page.goto("/risks/hierarchy");
    const rows = page.getByTestId("decision-timeline-row");
    await expect(rows).toHaveCount(3);
    // Toggling the "active" status filter writes ?status=active to the URL
    // and leaves only the two active decisions.
    await page.getByTestId("filter-status-active").click();
    await expect(page).toHaveURL(/[?&]status=active/);
    await expect(page.getByTestId("filter-status-active")).toHaveAttribute(
      "aria-pressed",
      "true",
    );
    await expect(rows).toHaveCount(2);
    for (const row of await rows.all()) {
      await expect(row).not.toContainText("superseded");
    }
    // Deep-link round-trip: navigating straight to the filtered URL
    // restores the filter state from the query params (AC-7).
    await page.goto("/risks/hierarchy?status=superseded");
    await expect(page.getByTestId("filter-status-superseded")).toHaveAttribute(
      "aria-pressed",
      "true",
    );
    await expect(rows).toHaveCount(1);
    await expect(rows).toContainText("dec-superseded-2025");
    // Clearing the filter removes the param from the URL.
    await page.getByTestId("filter-status-superseded").click();
    await expect(page).not.toHaveURL(/status=/);
    await expect(rows).toHaveCount(3);
  });

  test("AC-9d: overdue decisions show the amber 'Revisit overdue' pill", async ({
    page,
  }) => {
    await page.goto("/risks/hierarchy");
    // Exactly the seeded decision with a past revisit_by is flagged.
    const pill = page.getByTestId("decision-overdue-pill");
    await expect(pill).toHaveCount(1);
    await expect(pill).toHaveText("Revisit overdue");
    const overdueRow = page
      .getByTestId("decision-timeline-row")
      .filter({ has: pill });
    await expect(overdueRow).toHaveAttribute("data-overdue", "true");
    await expect(overdueRow).toContainText("dec-overdue-2025");
    await expect(
      page
        .getByTestId("decision-timeline-row")
        .filter({ hasText: "dec-future-2026" }),
    ).toHaveAttribute("data-overdue", "false");
    // The pill is NEVER auto-acknowledged — it persists across a reload
    // until a human acts on the decision upstream (anti-criterion P0-3).
    await page.reload();
    await expect(page.getByTestId("decision-overdue-pill")).toHaveCount(1);
  });

  test("responsive: layout collapses to a single column below md", async ({
    page,
  }) => {
    // #2213 moved the breakpoint from md to lg; at the 375px baseline the
    // three panels stack in one column either way.
    await page.setViewportSize({ width: 375, height: 812 });
    await page.goto("/risks/hierarchy");
    const panels = [
      "org-tree-panel",
      "theme-heatmap-panel",
      "decision-timeline-panel",
    ];
    for (const id of panels) {
      await expect(page.getByTestId(id)).toBeVisible();
    }
    // #2793: measure only once every panel holds its seeded content. A
    // panel still in its loading state has a different height, so boxes
    // read while the tree was loading put the heatmap above the tree's
    // bottom edge. The three rects are read in one evaluate, from one
    // layout, rather than three round-trips apart.
    await expect(page.getByTestId("org-tree-node")).toHaveCount(2);
    await expect(page.getByTestId("theme-heatmap-row")).toHaveCount(2);
    await expect(page.getByTestId("decision-timeline-row")).toHaveCount(3);
    const [tree, heatmap, timeline] = await page.evaluate((ids) => {
      return ids.map((id) => {
        const el = document.querySelector(`[data-testid="${id}"]`);
        if (!el) throw new Error(`missing panel ${id}`);
        const r = el.getBoundingClientRect();
        return { x: r.x, y: r.y, width: r.width, height: r.height };
      });
    }, panels);
    expect(Math.abs(tree.x - heatmap.x)).toBeLessThan(1);
    expect(Math.abs(heatmap.x - timeline.x)).toBeLessThan(1);
    expect(heatmap.y).toBeGreaterThanOrEqual(tree.y + tree.height);
    expect(timeline.y).toBeGreaterThanOrEqual(heatmap.y + heatmap.height);
    // No panel is wider than the viewport (#2080: the heatmap scrolls in
    // its own container).
    for (const b of [tree, heatmap, timeline]) {
      expect(b.x + b.width).toBeLessThanOrEqual(375);
    }
  });

  test("panels degrade independently — a failing query shows its own retry", async ({
    page,
  }) => {
    // A failing endpoint degrades only its own panel; the others still
    // render. The page never blocks on a single API (P0-2).
    await page.route("**/api/risks-hierarchy/decisions*", (r) => r.abort());
    await page.goto("/risks/hierarchy");
    await expect(page.getByTestId("decision-timeline-panel-error")).toBeVisible(
      {
        timeout: 30_000,
      },
    );
    await expect(
      page.getByTestId("decision-timeline-panel-retry"),
    ).toBeVisible();
    await expect(page.getByTestId("org-tree-node")).toHaveCount(2);
    await expect(page.getByTestId("theme-heatmap-grid")).toBeVisible();
    await expect(page.getByTestId("org-tree-panel-error")).toHaveCount(0);
    await expect(page.getByTestId("theme-heatmap-panel-error")).toHaveCount(0);
  });

  test("auth: a 401 from a bound endpoint bounces to /login", async ({
    page,
  }) => {
    // A cookie that expires mid-session: a bound read answers 401 and the
    // page's effect redirects to /login. The mock drops the cookie as it
    // answers, as an expiry would; with the cookie still valid, /login
    // sends the browser straight back.
    await page.route("**/api/risks-hierarchy/org-units*", async (route) => {
      await page.context().clearCookies();
      await route.fulfill({
        status: 401,
        contentType: "application/json",
        body: JSON.stringify({ error: "unauthorized" }),
      });
    });
    await page.goto("/risks/hierarchy");
    await expect(page).toHaveURL(/\/login\?from=/, { timeout: 30_000 });
    expect(new URL(page.url()).searchParams.get("from")).toBe(
      "/risks/hierarchy",
    );
  });

  test("empty states: each panel renders its own create-flow affordance", async ({
    page,
  }) => {
    // In a tenant with no org_units / no themed risks / no decisions,
    // each panel shows its empty state with a primary action (AC-8).
    await signInTo(page, EMPTY_TENANT);
    await page.goto("/risks/hierarchy");
    await expect(page.getByTestId("org-tree-empty")).toBeVisible();
    await expect(page.getByTestId("org-tree-empty-action")).toHaveText(
      "Add your first org_unit",
    );
    await expect(page.getByTestId("org-tree-empty-action")).toHaveAttribute(
      "href",
      "/risks",
    );
    await expect(page.getByTestId("theme-heatmap-empty")).toBeVisible();
    await expect(page.getByTestId("decision-timeline-empty")).toBeVisible();
    await expect(page.getByTestId("decision-timeline-empty-action")).toHaveText(
      "Record your first decision",
    );
    await expect(
      page.getByTestId("decision-timeline-empty-action"),
    ).toHaveAttribute("href", "/risks");
  });
});
