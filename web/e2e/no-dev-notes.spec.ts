// #2082 — the pages the UI audit flagged show no developer notes.
//
// The atlas/no-dev-notes-in-ui lint rule covers strings written in JSX.
// This spec covers what the user actually sees, including text that comes
// from data (the metric catalog's descriptions) and from constants the
// lint rule cannot follow. Each page is loaded for real, and its rendered
// text is checked for slice numbers, canvas sections, API paths, SQL and
// "WIP".
//
// Framework scope: the scope list is mocked empty so the empty state, and
// its link to the scope wizard (#2115), render without depending on
// another spec's seed.

import { expect, test } from "./fixtures";
import type { Page } from "@playwright/test";

const DEV_NOTES: Array<[string, RegExp]> = [
  ["a slice number", /slice\s*\d/i],
  ["a canvas section", /canvas\s*§/i],
  ["an API path", /\/v1\//],
  ["SQL", /\b(SELECT|WHERE|LIKE|IS DISTINCT FROM)\b/],
  ["WIP", /\bWIP\b/],
];

async function expectNoDevNotes(page: Page) {
  const text = await page.locator("body").innerText();
  for (const [what, re] of DEV_NOTES) {
    const hit = text.match(re);
    expect(hit?.[0], `page text mentions ${what}`).toBeUndefined();
  }
}

test.describe("no developer notes in UI text (#2082)", () => {
  test("Metrics dashboard", async ({ authedPage: page }) => {
    await page.goto("/dashboards/metrics");
    await expect(
      page.locator('[data-testid^="board-metric-"]').first(),
    ).toBeVisible();
    await expectNoDevNotes(page);
  });

  test("Metric detail", async ({ authedPage: page }) => {
    await page.goto("/dashboards/metrics/evidence_freshness_pct");
    await expect(page.getByTestId("metric-detail-audit-trail")).toBeVisible();
    await expectNoDevNotes(page);
  });

  test("Admin overview", async ({ authedPage: page }) => {
    await page.goto("/admin");
    await expect(
      page.getByRole("link", { name: /API keys/ }).first(),
    ).toBeVisible();
    await expectNoDevNotes(page);
  });

  test("Admin users", async ({ authedPage: page }) => {
    await page.goto("/admin/users");
    await expect(page.getByText("Role-permission matrix")).toBeVisible();
    await expectNoDevNotes(page);
  });

  test("Framework scope, empty", async ({ authedPage: page }) => {
    await page.route("**/api/framework-scopes?**", (route) =>
      route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ framework_scopes: [] }),
      }),
    );
    await page.goto("/framework-scopes/e2e20820-0000-0000-0000-000000000001");
    await expect(
      page.getByTestId("framework-scope-empty-create"),
    ).toBeVisible();
    await expectNoDevNotes(page);
  });
});
