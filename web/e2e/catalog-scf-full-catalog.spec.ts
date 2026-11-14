// Issue #2066 — /catalog/scf shows the whole SCF catalog, not the first
// page of 100 labelled as "a starter subset".
//
// Before the fix the page fetched `/api/anchors` once, got the platform's
// default page of 100 rows, and titled it "100 anchors" over hard-coded
// copy calling it a starter subset, while the catalog held 1,591.
//
// HERMETIC: the `/api/controls` anchors BFF is route-mocked with a full
// 1,591-anchor catalog that honours `limit`/`offset` the way the platform
// does (slice 594 convention), so the spec pins the page's walk, search,
// paging and marker without depending on the shared seed.

import type { Page, Route } from "@playwright/test";

import { expect, test } from "./fixtures";

const CATALOG_SIZE = 1591;
const PLATFORM_DEFAULT_LIMIT = 100;

function anchorAt(i: number) {
  const n = String(i + 1).padStart(4, "0");
  const controlled = i === 3;
  return {
    id: `a2066000-0000-4000-8000-00000000${n}`,
    scf_id: i === 3 ? "IAC-06" : `TST-${n}`,
    family: i === 3 ? "IAC" : "TST",
    name: i === 3 ? "Multi-Factor Authentication" : `Catalog anchor ${n}`,
    description: "",
    state: null,
    frameworks: [],
    control_count: controlled ? 1 : 0,
    ...(controlled
      ? { control_id: "c2066000-0000-4000-8000-000000000001" }
      : {}),
  };
}

const catalog = Array.from({ length: CATALOG_SIZE }, (_, i) => anchorAt(i));

async function mockCatalog(page: Page, seen: string[]) {
  await page.route("**/api/controls**", async (route: Route) => {
    const url = new URL(route.request().url());
    if (url.pathname !== "/api/controls") {
      await route.fallback();
      return;
    }
    seen.push(url.search);
    const limit = Number(
      url.searchParams.get("limit") ?? PLATFORM_DEFAULT_LIMIT,
    );
    const offset = Number(url.searchParams.get("offset") ?? 0);
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        anchors: catalog.slice(offset, offset + limit),
        total: CATALOG_SIZE,
        catalog_total: CATALOG_SIZE,
        with_control_total: 1,
      }),
    });
  });
}

test.describe("/catalog/scf shows the full catalog (issue #2066)", () => {
  test("shows the real total and pages through all of it", async ({
    authedPage: page,
  }) => {
    const seen: string[] = [];
    await mockCatalog(page, seen);
    await page.goto("/catalog/scf");

    await expect(page.getByTestId("catalog-scf-count")).toHaveText(
      "1,591 anchors",
    );
    await expect(page.getByText(/starter subset/i)).toHaveCount(0);
    // The walk went past the platform's first page.
    expect(seen.some((s) => /offset=[1-9]/.test(s))).toBe(true);

    const summary = page.getByTestId("catalog-scf-pagination-summary");
    await expect(summary).toHaveText(`Showing 1–100 of ${CATALOG_SIZE}`);
    await expect(page.getByTestId("catalog-scf-row")).toHaveCount(100);

    const next = page.getByTestId("catalog-scf-pagination-next");
    await next.click();
    await expect(summary).toHaveText(`Showing 101–200 of ${CATALOG_SIZE}`);
    await expect(page.getByText("TST-0101", { exact: true })).toBeVisible();

    // Page to the end: 16 pages of 100 hold 1,591 anchors.
    for (let p = 2; p < 16; p++) await next.click();
    await expect(summary).toHaveText(`Showing 1501–1591 of ${CATALOG_SIZE}`);
    await expect(page.getByTestId("catalog-scf-row")).toHaveCount(91);
    await expect(page.getByText("TST-1591", { exact: true })).toBeVisible();
    await expect(next).toBeDisabled();
  });

  test("search finds an anchor by code and by title word", async ({
    authedPage: page,
  }) => {
    await mockCatalog(page, []);
    await page.goto("/catalog/scf");
    const search = page.getByTestId("catalog-scf-search");
    const rows = page.getByTestId("catalog-scf-row");

    await search.fill("iac-06");
    await expect(rows).toHaveCount(1);
    await expect(rows.first()).toContainText("Multi-Factor Authentication");

    await search.fill("multi-factor");
    await expect(rows).toHaveCount(1);
    await expect(rows.first()).toContainText("IAC-06");

    await search.fill("TST-1590");
    await expect(rows).toHaveCount(1);
    await expect(rows.first()).toContainText("Catalog anchor 1590");

    await search.fill("no such anchor");
    await expect(rows).toHaveCount(0);
    await expect(page.getByTestId("catalog-scf-empty")).toBeVisible();
  });

  test("anchors with a tenant control carry a marker", async ({
    authedPage: page,
  }) => {
    await mockCatalog(page, []);
    await page.goto("/catalog/scf");

    const markers = page.getByTestId("catalog-scf-has-control");
    await expect(markers).toHaveCount(1);
    const row = page.getByTestId("catalog-scf-row").filter({ has: markers });
    await expect(row).toContainText("IAC-06");
  });
});
