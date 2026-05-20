// Slice 177 — Playwright E2E for the /exceptions list view.
//
// Runner status: quarantined behind slice 082 (the seed-data harness)
// per the slice 101/102 policies-list precedent. The test bodies below
// are preserved verbatim as a reviewable contract; the un-commented
// assertions become the gate when the seed harness lands.
//
// Run locally (after slice 082):
//   cd web
//   npx playwright install chromium     # once per machine
//   npx playwright test e2e/exceptions-list.spec.ts
//
// Pre-conditions the seed-data harness (slice 082) must establish
// before the commented assertions are turned on:
//   - PLATFORM_BASE_URL points at a running platform instance
//   - TEST_BEARER carries a credential in a tenant with at least:
//     * 1 exception in `requested` status
//     * 1 exception in `approved` status
//     * 1 exception in `active` status
//     * 1 exception in `expired` status (drives the empty-after-filter
//       case for the empty-state CTA)
//
// AC coverage targets: page chrome + table render, horizontal pill row,
// 3 Export buttons (CSV / JSON / XLSX) with stable test-ids, Export
// click triggers download, both empty-states surface correct copy +
// CTA, cross-tenant isolation.

import { test } from "@playwright/test";

test.describe("/exceptions list view", () => {
  test("AC-1: /exceptions renders the tenant-wide register", async () => {
    //    await page.goto("/login");
    //    await page.fill('input[name="token"]', process.env.TEST_BEARER!);
    //    await page.click("button[type=submit]");
    //    await page.goto("/exceptions");
    //    await expect(page.getByRole("heading", { name: /Exceptions/ })).toBeVisible();
    //    await expect(page.getByTestId("list-page")).toBeVisible();
    //    await expect(page.getByTestId("list-table-wrap")).toBeVisible();
  });

  test("AC-1: table renders the canonical columns from exceptionWire", async () => {
    //    await page.goto("/exceptions");
    //    for (const header of [
    //      "ID",
    //      "Control",
    //      "Status",
    //      "Requested by",
    //      "Requested",
    //      "Expires",
    //      "Days",
    //      "Justification",
    //    ]) {
    //      await expect(
    //        page.getByRole("columnheader", { name: header }),
    //      ).toBeVisible();
    //    }
  });

  test("AC-2: filter pills work URL-shareably (status + control_id)", async () => {
    //    await page.goto("/exceptions");
    //    const statusPill = page.getByLabel("Status");
    //    await statusPill.selectOption("active");
    //    await page.waitForLoadState("networkidle");
    //    await expect(page).toHaveURL(/\?.*status=active/);
    //    // The filter row is horizontal (P0-A2 of slice 098) — verify the
    //    // pill row mounts, NOT a left sidebar.
    //    await expect(page.getByTestId("list-filter-pills")).toBeVisible();
  });

  test("AC-2: bookmark survives reload (status persists from URL)", async () => {
    //    await page.goto("/exceptions?status=active");
    //    await page.waitForLoadState("networkidle");
    //    const statusPill = page.getByLabel("Status");
    //    await expect(statusPill).toHaveValue("active");
  });

  test("AC-3: Export CSV / JSON / XLSX buttons appear in the toolbar with stable data-testid", async () => {
    //    await page.goto("/exceptions");
    //    await expect(page.getByTestId("exceptions-export-buttons")).toBeVisible();
    //    await expect(page.getByTestId("exceptions-export-csv")).toBeVisible();
    //    await expect(page.getByTestId("exceptions-export-json")).toBeVisible();
    //    await expect(page.getByTestId("exceptions-export-xlsx")).toBeVisible();
  });

  test("AC-3: each Export button points at /api/admin/exceptions/export?format=...", async () => {
    //    await page.goto("/exceptions");
    //    for (const fmt of ["csv", "json", "xlsx"]) {
    //      const link = page.getByTestId(`exceptions-export-${fmt}`);
    //      const href = await link.getAttribute("href");
    //      expect(href).toBe(`/api/admin/exceptions/export?format=${fmt}`);
    //    }
  });

  test("AC-4: clicking Export CSV triggers the browser file-save dialog", async () => {
    //    await page.goto("/exceptions");
    //    const downloadPromise = page.waitForEvent("download");
    //    await page.getByTestId("exceptions-export-csv").click();
    //    const dl = await downloadPromise;
    //    // Backend authors the filename via Content-Disposition; we just
    //    // verify it landed.
    //    expect(dl.suggestedFilename()).toMatch(/exceptions.*\.csv$/i);
  });

  test("AC-5: true-zero state surfaces 'No exceptions filed yet' (no CTA)", async () => {
    //    // Pre-condition: seed an empty tenant (slice 082 fresh-tenant
    //    // fixture). When that lands:
    //    await page.goto("/exceptions");
    //    await expect(page.getByTestId("list-empty-state")).toBeVisible();
    //    await expect(page.getByText("No exceptions filed yet")).toBeVisible();
    //    // The truly-zero copy intentionally has no CTA — filing an
    //    // exception starts at the control detail page, not here.
  });

  test("AC-5: filter-induced empty surfaces 'Clear filters' CTA", async () => {
    //    // Narrow status to a value no row carries.
    //    await page.goto("/exceptions?status=denied");
    //    await expect(page.getByTestId("list-empty-state")).toBeVisible();
    //    await expect(
    //      page.getByText("No exceptions match these filters"),
    //    ).toBeVisible();
    //    await page.getByTestId("list-empty-state-cta").click();
    //    await expect(page.getByTestId("list-table-wrap")).toBeVisible();
  });

  test("AC-6: loading skeleton shows three shimmer rows on first paint", async () => {
    //    await page.route("**/api/exceptions*", async (route) => {
    //      await new Promise((r) => setTimeout(r, 250));
    //      await route.continue();
    //    });
    //    await page.goto("/exceptions");
    //    await expect(page.getByTestId("list-loading-skeleton")).toBeVisible();
  });

  test("P0-A-176-1: list view is READ-ONLY — no edit / approve / deny buttons in the table", async () => {
    //    // The slice 022 lifecycle workflow lives on the control detail
    //    // page. The list view must not invent inline mutate affordances.
    //    await page.goto("/exceptions");
    //    await expect(page.getByRole("button", { name: /approve/i })).toHaveCount(0);
    //    await expect(page.getByRole("button", { name: /deny/i })).toHaveCount(0);
    //    await expect(page.getByRole("button", { name: /activate/i })).toHaveCount(0);
  });

  test("P0-A3 (cross-tenant): tenant A bearer cannot see tenant B's exceptions via list view", async () => {
    //    // Seed harness pre-condition: two tenants with disjoint
    //    // exception rows. Sign in as tenant A; the visible row count
    //    // matches the tenant-A row count exactly. RLS enforces this at
    //    // the DB layer; the BFF defensively strips any caller-supplied
    //    // tenant_id query param (verified at the unit-test level by
    //    // route.test.ts).
    //    await page.goto("/login");
    //    await page.fill('input[name="token"]', process.env.TEST_BEARER_TENANT_A!);
    //    await page.click("button[type=submit]");
    //    await page.goto("/exceptions");
    //    const rowsA = await page.getByTestId("list-table-row").count();
    //    // Even attempting to inject tenant_id at the URL changes nothing.
    //    await page.goto("/exceptions?tenant_id=tenant-b");
    //    const rowsTrick = await page.getByTestId("list-table-row").count();
    //    expect(rowsTrick).toBe(rowsA);
  });
});
