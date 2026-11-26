// Slice 139 — Playwright E2E for the audit-periods + vendors data
// export buttons.
//
// Seeded by fixtures/e2e/audit-periods-vendors-export.sql: a tenant of
// the spec's own with one audit period and one vendor whose free-text
// owner_user is email-shaped. The spec signs in to it with an admin JWT
// minted by /v1/test/issue-jwt. #2755 restored the assertions slice 139
// left commented out behind the slice-082 quarantine.
//
// Run locally:
//   cd web
//   npx playwright install chromium     # once per machine
//   npx playwright test e2e/audit-periods-vendors-export.spec.ts
//
// AC-10 coverage targets: both pages render the three-format Export
// button group; each button has a stable data-testid; the href of
// each button points at the slice-139 BFF (`/api/admin/...`); the
// browser triggers a download when the button is clicked.

import type { Download } from "@playwright/test";

import { expect, test } from "./fixtures";
import { seedFromFixture } from "./seed";
import { signInToTenant } from "./tenant-session";

// Mirrors the tenant in fixtures/e2e/audit-periods-vendors-export.sql.
const TENANT = "00000000-0000-0000-0000-2755a0000011";
// The seeded vendor's owner_user local-part; the export must mask it.
const OWNER_LOCAL_PART = "export-owner-e2e";

async function downloadBody(download: Download): Promise<string> {
  const stream = await download.createReadStream();
  const chunks: Buffer[] = [];
  for await (const c of stream) chunks.push(c as Buffer);
  return Buffer.concat(chunks).toString("utf-8");
}

test.beforeAll(() => {
  seedFromFixture("audit-periods-vendors-export");
});

test.beforeEach(async ({ page }) => {
  await signInToTenant(page, {
    tenantId: TENANT,
    roles: ["admin"],
    superAdmin: true,
  });
});

test.describe("audit-periods export buttons", () => {
  test("AC-10a: /audits renders three Export buttons (csv|json|xlsx)", async ({
    page,
  }) => {
    await page.goto("/audits");
    const group = page.getByTestId("audit-periods-export-buttons");
    await expect(group).toBeVisible({ timeout: 30_000 });
    for (const fmt of ["csv", "json", "xlsx"] as const) {
      const button = group.getByTestId(`audit-periods-export-${fmt}`);
      await expect(button).toBeVisible();
      await expect(button).toHaveText(fmt.toUpperCase());
    }
  });

  test("AC-10b: each /audits Export button hrefs to /api/admin/audit-periods/export", async ({
    page,
  }) => {
    await page.goto("/audits");
    await expect(page.getByTestId("audit-periods-export-buttons")).toBeVisible({
      timeout: 30_000,
    });
    for (const fmt of ["csv", "json", "xlsx"] as const) {
      await expect(
        page.getByTestId(`audit-periods-export-${fmt}`),
      ).toHaveAttribute(
        "href",
        `/api/admin/audit-periods/export?format=${fmt}`,
      );
    }
  });

  test("AC-10c: clicking the CSV Export button triggers a download", async ({
    page,
  }) => {
    await page.goto("/audits");
    const button = page.getByTestId("audit-periods-export-csv");
    await expect(button).toBeVisible({ timeout: 30_000 });
    const downloadPromise = page.waitForEvent("download");
    await button.click();
    const download = await downloadPromise;
    // Filename contract: audit-periods_<YYYYMMDD>.csv
    expect(download.suggestedFilename()).toMatch(/^audit-periods_\d{8}\.csv$/);
    // The body carries the seeded period, so the click exported real rows.
    expect(await downloadBody(download)).toContain("E2E export period");
  });
});

test.describe("vendors export buttons", () => {
  test("AC-10d: /vendors renders three Export buttons (csv|json|xlsx)", async ({
    page,
  }) => {
    await page.goto("/vendors");
    const group = page.getByTestId("vendors-export-buttons");
    await expect(group).toBeVisible({ timeout: 30_000 });
    for (const fmt of ["csv", "json", "xlsx"] as const) {
      const button = group.getByTestId(`vendors-export-${fmt}`);
      await expect(button).toBeVisible();
      await expect(button).toHaveText(fmt.toUpperCase());
    }
  });

  test("AC-10e: each /vendors Export button hrefs to /api/admin/vendors/export", async ({
    page,
  }) => {
    await page.goto("/vendors");
    await expect(page.getByTestId("vendors-export-buttons")).toBeVisible({
      timeout: 30_000,
    });
    for (const fmt of ["csv", "json", "xlsx"] as const) {
      await expect(page.getByTestId(`vendors-export-${fmt}`)).toHaveAttribute(
        "href",
        `/api/admin/vendors/export?format=${fmt}`,
      );
    }
  });

  test("AC-10f: clicking the JSON Export button triggers a download", async ({
    page,
  }) => {
    await page.goto("/vendors");
    const button = page.getByTestId("vendors-export-json");
    await expect(button).toBeVisible({ timeout: 30_000 });
    const downloadPromise = page.waitForEvent("download");
    await button.click();
    const download = await downloadPromise;
    expect(download.suggestedFilename()).toMatch(/^vendors_\d{8}\.json$/);
    expect(await downloadBody(download)).toContain("E2E export vendor");
  });

  test("AC-10g: vendor export body masks owner_user emails", async ({
    page,
  }) => {
    // The masking lives server-side; this is the end-to-end contract that
    // the masked column reaches the wire. Walks the download and inspects
    // the body — the local-part MUST NOT appear.
    await page.goto("/vendors");
    const button = page.getByTestId("vendors-export-csv");
    await expect(button).toBeVisible({ timeout: 30_000 });
    const downloadPromise = page.waitForEvent("download");
    await button.click();
    const body = await downloadBody(await downloadPromise);
    // The seeded vendor row is in the export.
    expect(body).toContain("E2E export vendor");
    // Canonical column name + masked-token shape MUST be present.
    expect(body).toContain("owner_user_masked");
    expect(body).toContain("*@operator.example.invalid");
    // The seeded owner_user's local-part MUST NOT leak.
    expect(body).not.toContain(OWNER_LOCAL_PART);
  });
});
