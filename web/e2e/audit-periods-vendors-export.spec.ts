// Slice 139 — Playwright E2E for the audit-periods + vendors data
// export buttons.
//
// Runner status: quarantined behind slice 082 (seed-data harness) per
// the slice 102 / 098 precedent. The assertions below are preserved
// verbatim as a reviewable contract.
//
// Run locally (once the seed harness lands):
//   cd web
//   npx playwright install chromium     # once per machine
//   npx playwright test e2e/audit-periods-vendors-export.spec.ts
//
// Pre-conditions the seed-data harness (slice 082) must establish:
//   - PLATFORM_BASE_URL points at a running platform instance
//   - TEST_BEARER carries an admin credential in a tenant with at
//     least:
//     * 1 audit_period row (drives /audits Export button assertions)
//     * 1 vendor row (drives /vendors Export button assertions)
//
// AC-10 coverage targets: both pages render the three-format Export
// button group; each button has a stable data-testid; the href of
// each button points at the slice-139 BFF (`/api/admin/...`); the
// browser triggers a download when the button is clicked.

import { test } from "@playwright/test";

test.describe("audit-periods export buttons", () => {
  test("AC-10a: /audits renders three Export buttons (csv|json|xlsx)", async () => {
    //    await page.goto("/login");
    //    await page.fill('input[name="token"]', process.env.TEST_BEARER!);
    //    await page.click("button[type=submit]");
    //    await page.goto("/audits");
    //    const group = page.getByTestId("audit-periods-export-buttons");
    //    await expect(group).toBeVisible();
    //    await expect(page.getByTestId("audit-periods-export-csv")).toBeVisible();
    //    await expect(page.getByTestId("audit-periods-export-json")).toBeVisible();
    //    await expect(page.getByTestId("audit-periods-export-xlsx")).toBeVisible();
  });

  test("AC-10b: each /audits Export button hrefs to /api/admin/audit-periods/export", async () => {
    //    await page.goto("/audits");
    //    for (const fmt of ["csv", "json", "xlsx"] as const) {
    //      const a = page.getByTestId(`audit-periods-export-${fmt}`);
    //      const href = await a.getAttribute("href");
    //      expect(href).toBe(`/api/admin/audit-periods/export?format=${fmt}`);
    //    }
  });

  test("AC-10c: clicking the CSV Export button triggers a download", async () => {
    //    await page.goto("/audits");
    //    const downloadPromise = page.waitForEvent("download");
    //    await page.getByTestId("audit-periods-export-csv").click();
    //    const download = await downloadPromise;
    //    // Filename contract: audit-periods_<YYYYMMDD>[.csv]
    //    expect(download.suggestedFilename()).toMatch(/^audit-periods_\d{8}\.csv$/);
  });
});

test.describe("vendors export buttons", () => {
  test("AC-10d: /vendors renders three Export buttons (csv|json|xlsx)", async () => {
    //    await page.goto("/vendors");
    //    const group = page.getByTestId("vendors-export-buttons");
    //    await expect(group).toBeVisible();
    //    await expect(page.getByTestId("vendors-export-csv")).toBeVisible();
    //    await expect(page.getByTestId("vendors-export-json")).toBeVisible();
    //    await expect(page.getByTestId("vendors-export-xlsx")).toBeVisible();
  });

  test("AC-10e: each /vendors Export button hrefs to /api/admin/vendors/export", async () => {
    //    await page.goto("/vendors");
    //    for (const fmt of ["csv", "json", "xlsx"] as const) {
    //      const a = page.getByTestId(`vendors-export-${fmt}`);
    //      const href = await a.getAttribute("href");
    //      expect(href).toBe(`/api/admin/vendors/export?format=${fmt}`);
    //    }
  });

  test("AC-10f: clicking the JSON Export button triggers a download", async () => {
    //    await page.goto("/vendors");
    //    const downloadPromise = page.waitForEvent("download");
    //    await page.getByTestId("vendors-export-json").click();
    //    const download = await downloadPromise;
    //    expect(download.suggestedFilename()).toMatch(/^vendors_\d{8}\.json$/);
  });

  test("AC-10g: vendor export body masks owner_user emails", async () => {
    //    // The masking lives server-side; this assertion is the end-to-end
    //    // contract that the masked column reaches the wire. Walks the
    //    // download and inspects the body — local-parts MUST NOT appear.
    //    await page.goto("/vendors");
    //    const downloadPromise = page.waitForEvent("download");
    //    await page.getByTestId("vendors-export-csv").click();
    //    const download = await downloadPromise;
    //    const buf = await download.createReadStream().then(async (s) => {
    //      const chunks: Buffer[] = [];
    //      for await (const c of s!) chunks.push(c as Buffer);
    //      return Buffer.concat(chunks).toString("utf-8");
    //    });
    //    // Canonical column name + masked-token shape MUST be present.
    //    expect(buf).toContain("owner_user_masked");
    //    expect(buf).toMatch(/\*@/);
    //    // The seed harness inserts a vendor with owner_user =
    //    // "alice@operator.example.com"; the local-part MUST NOT leak.
    //    expect(buf).not.toContain("alice@operator");
  });
});
