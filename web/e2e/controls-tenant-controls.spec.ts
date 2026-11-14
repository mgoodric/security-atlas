// Issue #1741 — /controls lists the tenant's own controls by default,
// links rows by control id, and keeps the SCF catalog as a separate view
// with a server-side "Has control" filter.
//
// Before the fix the page listed the first 100 SCF anchors alphabetically
// (none of an adopter's 39 controls), linked each row by ANCHOR id (which
// the control detail page resolves as a control id, so every row opened
// the "no control in this tenant" state), and the sidebar badge read 100.
//
// HERMETIC: every list BFF is route-mocked (slice 594 convention), so the
// spec pins the page's requests and rendering, not the shared seed. The
// tenant-isolation and SQL-filter guarantees are pinned server-side by
// internal/api/controls/list_state_integration_test.go and
// internal/api/anchors/has_control_integration_test.go.

import type { Page, Route } from "@playwright/test";

import { expect, test } from "./fixtures";

// Neutral test strings only (P0-A9 / GitGuardian).
const CONTROL_A = "c1741000-0000-4000-8000-00000000000a";
const CONTROL_B = "c1741000-0000-4000-8000-00000000000b";
const ANCHOR_A = "a1741000-0000-4000-8000-00000000000a";
const ANCHOR_UNCONTROLLED = "a1741000-0000-4000-8000-0000000000ff";

const myControls = [
  {
    id: CONTROL_A,
    title: "Quarterly access review (1741)",
    control_family: "Identity & Access Management",
    scf_id: "IAC-37",
    lifecycle_state: "active",
    bundle_id: "ctrl-1741-a",
    scf_anchor_id: ANCHOR_A,
    state: {
      result: "pass",
      freshness_status: "fresh",
      last_observed_at: "2026-11-12T00:00:00Z",
      evaluated_at: "2026-11-12T00:00:00Z",
    },
    frameworks: ["SOC2"],
  },
  {
    id: CONTROL_B,
    title: "Encryption at rest (1741)",
    control_family: "Cryptographic Protections",
    scf_id: "CRY-05",
    lifecycle_state: "active",
    bundle_id: "ctrl-1741-b",
    scf_anchor_id: null,
    state: null,
    frameworks: [],
  },
];

const controlledAnchor = {
  id: ANCHOR_A,
  scf_id: "IAC-37",
  family: "Identification & Authentication",
  name: "Multi-factor authentication",
  description: "",
  state: null,
  frameworks: [],
  control_id: CONTROL_A,
  control_count: 1,
};
const uncontrolledAnchor = {
  id: ANCHOR_UNCONTROLLED,
  scf_id: "AAT-01",
  family: "Artificial Intelligence",
  name: "AI governance",
  description: "",
  state: null,
  frameworks: [],
  control_count: 0,
};

// One dispatcher for both list BFFs, so request order cannot matter.
async function mockListBFFs(page: Page, seen: string[]) {
  await page.route("**/api/controls**", async (route: Route) => {
    const url = new URL(route.request().url());
    if (url.pathname === "/api/controls-list") {
      seen.push(url.pathname + url.search);
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          controls: myControls,
          count: myControls.length,
          total: myControls.length,
        }),
      });
      return;
    }
    if (url.pathname === "/api/controls") {
      seen.push(url.pathname + url.search);
      const hasControl = url.searchParams.get("has_control");
      const anchors =
        hasControl === "true"
          ? [controlledAnchor]
          : hasControl === "false"
            ? [uncontrolledAnchor]
            : [uncontrolledAnchor, controlledAnchor];
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          anchors,
          total: anchors.length,
          catalog_total: 1451,
          with_control_total: 1,
        }),
      });
      return;
    }
    await route.fallback();
  });
  await page.route("**/api/scope-cells**", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ cells: [] }),
    });
  });
}

test.describe("/controls lists the tenant's controls (issue #1741)", () => {
  test("the default view lists the tenant's controls, linked by control id", async ({
    authedPage: page,
  }) => {
    const seen: string[] = [];
    await mockListBFFs(page, seen);
    await page.goto("/controls");

    // The mobile card stack reuses the cell renderers (hidden at desktop),
    // so every row locator is scoped to the desktop table.
    const table = page.getByTestId("list-table-wrap");
    await expect(table.getByTestId("list-table-row")).toHaveCount(2);
    await expect(
      table.getByText("Quarterly access review (1741)"),
    ).toBeVisible();

    // Every row links to its CONTROL id, never an anchor id.
    const hrefs = await table
      .getByTestId("controls-row-scf-id")
      .evaluateAll((els) => els.map((e) => e.getAttribute("href")));
    expect(hrefs.sort()).toEqual(
      [`/controls/${CONTROL_A}`, `/controls/${CONTROL_B}`].sort(),
    );

    // Header counts controls, and states them against the catalog size.
    await expect(page.getByTestId("controls-count-label")).toContainText(
      "2 controls",
    );
    await expect(page.getByTestId("controls-view-summary")).toHaveText(
      "2 controls in this tenant · 1,451 SCF anchors in the catalog",
    );

    // The sidebar badge counts the same controls.
    await expect(page.getByTestId("sidebar-controls-count")).toHaveText("2");

    // The list came from the per-control read, not the anchor catalog.
    expect(
      seen.some((u) => u.includes("/api/controls-list?include=state")),
    ).toBe(true);

    await table.getByTestId("controls-row-scf-id").first().click();
    await expect(page).toHaveURL(/\/controls\/c1741000-/);
  });

  test("the SCF catalog is a separate view with a server-side Has control filter", async ({
    authedPage: page,
  }) => {
    const seen: string[] = [];
    await mockListBFFs(page, seen);
    await page.goto("/controls");
    const table = page.getByTestId("list-table-wrap");
    await expect(table.getByTestId("list-table-row")).toHaveCount(2);

    await page.getByLabel("View", { exact: true }).selectOption("catalog");
    await page.waitForURL((u) => u.searchParams.get("view") === "catalog");
    await expect(table.getByTestId("list-table-row")).toHaveCount(2);
    await expect(page.getByTestId("controls-view-summary")).toHaveText(
      "1 of 1,451 SCF anchors have a control in this tenant",
    );

    // The anchor with a tenant control links to that control; the one
    // without a control is plain text, not a link to a dead page.
    await expect(table.getByTestId("controls-row-scf-id")).toHaveAttribute(
      "href",
      `/controls/${CONTROL_A}`,
    );
    await expect(table.getByTestId("controls-row-scf-id-unlinked")).toHaveText(
      "AAT-01",
    );

    // Has control narrows on the server: the page asks the BFF for it.
    await page.getByLabel("Has control", { exact: true }).selectOption("true");
    await page.waitForURL((u) => u.searchParams.get("has_control") === "true");
    await expect(table.getByTestId("list-table-row")).toHaveCount(1);
    expect(seen.some((u) => u.includes("has_control=true"))).toBe(true);
  });
});

// Issue #1740 — the catalog view walked only the platform's first page,
// so 100 of SCF's ~1,600 anchors were reachable and the Family pill only
// knew the families on that page. The fake BFF pages a 1,200-anchor
// catalog the way the platform does (limit/offset, capped at 500); the
// last 200 anchors sit in a family only the third page carries.
const CATALOG_SIZE = 1200;
const catalogAnchors = Array.from({ length: CATALOG_SIZE }, (_, i) => ({
  id: `a1740000-0000-4000-8000-${String(i).padStart(12, "0")}`,
  scf_id: `TST-${String(i).padStart(4, "0")}`,
  family: i >= 1000 ? "Late Family (1740)" : "Early Family (1740)",
  name: `Catalog anchor ${i}`,
  description: "",
  state: null,
  frameworks: [],
  control_count: 0,
}));

test.describe("/controls SCF catalog walks every page (issue #1740)", () => {
  test("the catalog lists every anchor and the Family pill reaches the last page", async ({
    authedPage: page,
  }) => {
    const seen: string[] = [];
    await page.route("**/api/controls**", async (route: Route) => {
      const url = new URL(route.request().url());
      if (url.pathname !== "/api/controls") {
        await route.fallback();
        return;
      }
      seen.push(url.search);
      const limit = Math.min(Number(url.searchParams.get("limit") ?? 100), 500);
      const offset = Number(url.searchParams.get("offset") ?? 0);
      const anchors = catalogAnchors.slice(offset, offset + limit);
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          anchors,
          total: CATALOG_SIZE,
          catalog_total: CATALOG_SIZE,
          with_control_total: 0,
        }),
      });
    });
    await page.route("**/api/scope-cells**", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ cells: [] }),
      });
    });

    await page.goto("/controls?view=catalog");
    await expect(page.getByTestId("controls-pagination-summary")).toHaveText(
      "Showing 1–50 of 1200",
    );
    expect(seen.filter((s) => s.includes("offset=1000"))).toHaveLength(1);

    await page.getByLabel("Family").selectOption("Late Family (1740)");
    await expect(page.getByTestId("controls-pagination-summary")).toHaveText(
      "Showing 1–50 of 200",
    );
    await expect(
      page.getByTestId("list-table-wrap").getByTestId("list-table-row").first(),
    ).toContainText("TST-1000");
  });
});
