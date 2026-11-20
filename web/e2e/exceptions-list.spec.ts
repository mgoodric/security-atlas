// Slice 177 — Playwright E2E for the /exceptions list view.
//
// Seeded by fixtures/e2e/exceptions-list.sql, which gives the spec three
// tenants of its own: tenant A with four exceptions (requested, approved,
// active, expired — none denied), an empty tenant, and tenant B with two
// exceptions tenant A must never see. The spec signs in through
// /v1/test/issue-jwt (web/e2e/tenant-session.ts). #2755 restored the
// assertions slice 177 left commented out behind the slice-082
// quarantine.
//
// Run locally:
//   cd web
//   npx playwright install chromium     # once per machine
//   npx playwright test e2e/exceptions-list.spec.ts
//
// AC coverage targets: page chrome + table render, horizontal pill row,
// 3 Export buttons (CSV / JSON / XLSX) with stable test-ids, Export
// click triggers download, both empty-states surface correct copy +
// CTA, cross-tenant isolation.

import type { Page } from "@playwright/test";

import { expect, test } from "./fixtures";
import { seedFromFixture } from "./seed";
import { signInToTenant } from "./tenant-session";

// Mirrors the ids in fixtures/e2e/exceptions-list.sql.
const TENANT_A = "00000000-0000-0000-0000-2755c0000003";
const TENANT_EMPTY = "00000000-0000-0000-0000-2755c0000004";
const TENANT_B = "00000000-0000-0000-0000-2755c0000005";
const CONTROL_A = "00000000-0000-0000-0000-2755c000c001";
const TENANT_A_ROWS = 4;
const TENANT_B_ROWS = 2;

async function signIn(page: Page, tenantId: string): Promise<void> {
  await signInToTenant(page, { tenantId, roles: ["admin"], superAdmin: true });
}

function rows(page: Page) {
  return page.getByTestId("list-table-row");
}

test.describe("/exceptions list view", () => {
  test.beforeAll(() => {
    seedFromFixture("exceptions-list");
  });

  test.beforeEach(async ({ page }) => {
    await signIn(page, TENANT_A);
  });

  test("AC-1: /exceptions renders the tenant-wide register", async ({
    page,
  }) => {
    await page.goto("/exceptions");
    await expect(
      page.getByRole("heading", { name: "Exceptions", exact: true }),
    ).toBeVisible({ timeout: 30_000 });
    await expect(page.getByTestId("list-page")).toBeVisible();
    await expect(page.getByTestId("list-table-wrap")).toBeVisible();
    await expect(rows(page)).toHaveCount(TENANT_A_ROWS);
    // One row per seeded status (the fixture inserts them in one
    // statement, so their order is not pinned).
    for (const status of ["requested", "approved", "active", "expired"]) {
      await expect(
        page.getByTestId("exceptions-row-status").filter({ hasText: status }),
      ).toHaveCount(1);
    }
  });

  test("AC-1: table renders the canonical columns from exceptionWire", async ({
    page,
  }) => {
    await page.goto("/exceptions");
    await expect(page.getByTestId("list-table-wrap")).toBeVisible({
      timeout: 30_000,
    });
    await expect(page.getByRole("columnheader")).toHaveText([
      "ID",
      "Control",
      "Status",
      "Requested by",
      "Requested",
      "Expires",
      "Days",
      "Justification",
    ]);
  });

  test("AC-2: filter pills work URL-shareably (status + control_id)", async ({
    page,
  }) => {
    await page.goto("/exceptions");
    await expect(rows(page)).toHaveCount(TENANT_A_ROWS, { timeout: 30_000 });
    // The filter row is horizontal (P0-A2 of slice 098) — the pill row
    // mounts, NOT a left sidebar.
    await expect(page.getByTestId("list-filter-pills")).toBeVisible();
    await page
      .getByTestId("list-filter-pills")
      .getByLabel("Status")
      .selectOption("active");
    await expect(page).toHaveURL(/\?.*status=active/);
    await expect(rows(page)).toHaveCount(1);
    await expect(page.getByTestId("exceptions-row-status")).toHaveText(
      "active",
    );
    await page
      .getByTestId("list-filter-pills")
      .getByLabel("Control")
      .selectOption(CONTROL_A);
    await expect(page).toHaveURL(new RegExp(`[?&]control_id=${CONTROL_A}`));
    await expect(page).toHaveURL(/[?&]status=active/);
    await expect(rows(page)).toHaveCount(1);
  });

  test("AC-2: bookmark survives reload (status persists from URL)", async ({
    page,
  }) => {
    await page.goto("/exceptions?status=active");
    await expect(
      page.getByTestId("list-filter-pills").getByLabel("Status"),
    ).toHaveValue("active", {
      timeout: 30_000,
    });
    await expect(rows(page)).toHaveCount(1);
    await page.reload();
    await expect(
      page.getByTestId("list-filter-pills").getByLabel("Status"),
    ).toHaveValue("active", {
      timeout: 30_000,
    });
    await expect(rows(page)).toHaveCount(1);
  });

  test("AC-3: Export CSV / JSON / XLSX buttons appear in the toolbar with stable data-testid", async ({
    page,
  }) => {
    await page.goto("/exceptions");
    await expect(page.getByTestId("exceptions-export-buttons")).toBeVisible({
      timeout: 30_000,
    });
    await expect(page.getByTestId("exceptions-export-csv")).toHaveText("CSV");
    await expect(page.getByTestId("exceptions-export-json")).toHaveText("JSON");
    await expect(page.getByTestId("exceptions-export-xlsx")).toHaveText("XLSX");
  });

  test("AC-3: each Export button points at /api/admin/exceptions/export?format=...", async ({
    page,
  }) => {
    await page.goto("/exceptions");
    await expect(page.getByTestId("exceptions-export-buttons")).toBeVisible({
      timeout: 30_000,
    });
    for (const fmt of ["csv", "json", "xlsx"]) {
      await expect(
        page.getByTestId(`exceptions-export-${fmt}`),
      ).toHaveAttribute("href", `/api/admin/exceptions/export?format=${fmt}`);
    }
  });

  test("AC-4: clicking Export CSV triggers the browser file-save dialog", async ({
    page,
  }) => {
    await page.goto("/exceptions");
    await expect(rows(page)).toHaveCount(TENANT_A_ROWS, { timeout: 30_000 });
    const downloadPromise = page.waitForEvent("download");
    await page.getByTestId("exceptions-export-csv").click();
    const dl = await downloadPromise;
    // Backend authors the filename via Content-Disposition.
    expect(dl.suggestedFilename()).toMatch(/exceptions.*\.csv$/i);
    expect(await dl.failure()).toBeNull();
  });

  test("AC-5: true-zero state surfaces 'No exceptions filed yet' (no CTA)", async ({
    page,
  }) => {
    await signIn(page, TENANT_EMPTY);
    await page.goto("/exceptions");
    await expect(page.getByTestId("list-empty-state")).toBeVisible({
      timeout: 30_000,
    });
    await expect(page.getByText("No exceptions filed yet")).toBeVisible();
    // The truly-zero copy intentionally has no CTA — filing an
    // exception starts at the control detail page, not here.
    await expect(page.getByTestId("list-empty-state-cta")).toHaveCount(0);
  });

  test("AC-5: filter-induced empty surfaces 'Clear filters' CTA", async ({
    page,
  }) => {
    // Narrow status to a value no row carries.
    await page.goto("/exceptions?status=denied");
    await expect(page.getByTestId("list-empty-state")).toBeVisible({
      timeout: 30_000,
    });
    await expect(
      page.getByText("No exceptions match these filters"),
    ).toBeVisible();
    const cta = page.getByTestId("list-empty-state-cta");
    await expect(cta).toHaveText("Clear filters");
    await cta.click();
    await expect(page).not.toHaveURL(/status=/);
    await expect(page.getByTestId("list-table-wrap")).toBeVisible();
    await expect(rows(page)).toHaveCount(TENANT_A_ROWS);
  });

  test("AC-6: loading skeleton shows three shimmer rows on first paint", async ({
    page,
  }) => {
    // Hold the list response until the skeleton has been observed.
    let release!: () => void;
    const held = new Promise<void>((r) => (release = r));
    await page.route("**/api/exceptions*", async (route) => {
      await held;
      await route.continue();
    });
    await page.goto("/exceptions");
    await expect(page.getByTestId("list-loading-skeleton")).toBeVisible({
      timeout: 30_000,
    });
    await expect(page.getByTestId("list-loading-skeleton-row")).toHaveCount(3);
    release();
    await expect(rows(page)).toHaveCount(TENANT_A_ROWS);
    await expect(page.getByTestId("list-loading-skeleton")).toHaveCount(0);
  });

  test("P0-A-176-1: list view is READ-ONLY — no edit / approve / deny buttons in the table", async ({
    page,
  }) => {
    // The slice 022 lifecycle workflow lives on the control detail
    // page. The list view must not invent inline mutate affordances.
    await page.goto("/exceptions");
    await expect(rows(page)).toHaveCount(TENANT_A_ROWS, { timeout: 30_000 });
    const main = page.getByTestId("list-page");
    for (const name of [/approve/i, /deny/i, /activate/i, /edit/i, /revoke/i]) {
      await expect(main.getByRole("button", { name })).toHaveCount(0);
    }
  });

  test("P0-A3 (cross-tenant): tenant A bearer cannot see tenant B's exceptions via list view", async ({
    page,
  }) => {
    // Tenant B's rows exist (so isolation below is not vacuous).
    await signIn(page, TENANT_B);
    await page.goto("/exceptions");
    await expect(rows(page)).toHaveCount(TENANT_B_ROWS, { timeout: 30_000 });

    // Signed in as tenant A, only tenant A's rows render. RLS enforces
    // this at the DB layer; the BFF strips any caller-supplied tenant_id
    // query param (route.test.ts covers that at the unit level).
    await signIn(page, TENANT_A);
    await page.goto("/exceptions");
    await expect(rows(page)).toHaveCount(TENANT_A_ROWS, { timeout: 30_000 });
    await expect(page.getByText(/Tenant B only/)).toHaveCount(0);
    // Even attempting to inject tenant_id at the URL changes nothing.
    await page.goto(`/exceptions?tenant_id=${TENANT_B}`);
    await expect(rows(page)).toHaveCount(TENANT_A_ROWS, { timeout: 30_000 });
    await expect(page.getByText(/Tenant B only/)).toHaveCount(0);
  });
});
