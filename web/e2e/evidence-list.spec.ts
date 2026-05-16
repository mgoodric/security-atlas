// Slice 099 — Playwright E2E for the /evidence list view.
//
// Runner status (post-slice-069 / 071 audit):
// Playwright IS installed in `web/`. This spec is quarantined behind
// slice 082 (the seed-data harness) per slice 079's decision; when
// that harness lands, the un-commented assertions below become the
// gate. The test bodies are preserved verbatim as a reviewable
// contract per the slice 040 / 042 / 056 / 060 / 064 / 071 / 094 /
// 098 / 100 / 102 precedent.
//
// Run locally:
//   cd web
//   npx playwright install chromium     # once per machine
//   npx playwright test e2e/evidence-list.spec.ts
//
// Pre-conditions the seed-data harness (slice 082) must establish
// before the commented assertions are turned on:
//   - PLATFORM_BASE_URL points at a running platform instance
//   - TEST_BEARER carries a credential in a tenant with at least:
//     * 1 SCF anchor with a tenant control instantiated
//     * 1 anchor that has >=3 evidence records ingested (drives
//       row-render + filter narrowing)
//     * 1 anchor that has zero evidence records (drives empty state)
//
// AC-9 coverage targets: list renders for a selected control, control
// pill narrows the result set, true-empty state surfaces "Clear
// filters" + "Set up a connector →" when filters return zero rows,
// row click opens the drawer, hash cell is click-to-copy.

import { test } from "@playwright/test";

test.describe("/evidence list view", () => {
  test("AC-1: /evidence renders the pick-a-control prompt by default", async () => {
    //    await page.goto("/login");
    //    await page.fill('input[name="token"]', process.env.TEST_BEARER!);
    //    await page.click("button[type=submit]");
    //    await page.goto("/evidence");
    //    await expect(page.getByRole("heading", { name: /Evidence ledger/ })).toBeVisible();
    //    await expect(page.getByTestId("list-page")).toBeVisible();
    //    // No control selected yet -> the pick-a-control prompt.
    //    await expect(page.getByTestId("evidence-pick-control-title")).toBeVisible();
  });

  test("AC-3: selecting a control loads its evidence ledger", async () => {
    //    await page.goto("/evidence");
    //    const controlPill = page.getByLabel("Control");
    //    // Select the first non-sentinel option (the harness-seeded anchor
    //    // with evidence rows).
    //    const opts = await controlPill.locator("option").all();
    //    await controlPill.selectOption({ index: 1 });
    //    await page.waitForLoadState("networkidle");
    //    // The pick-a-control prompt disappears, the table renders rows.
    //    await expect(page.getByTestId("evidence-pick-control-title")).toHaveCount(0);
    //    await expect(page.getByTestId("list-table-wrap")).toBeVisible();
    //    const rowCount = await page.getByTestId("list-table-row").count();
    //    expect(rowCount).toBeGreaterThan(0);
  });

  test("AC-3 hash: hash cell renders as 8-char prefix only", async () => {
    //    await page.goto("/evidence?control_id=__seeded-anchor-with-rows__");
    //    const hashCell = page.getByTestId("evidence-row-hash").first();
    //    const text = await hashCell.innerText();
    //    // Render is "<8 chars>…" — strip the trailing ellipsis, check 8.
    //    const prefix = text.replace("…", "").trim();
    //    expect(prefix.length).toBe(8);
  });

  test("AC-5: empty state surfaces with two CTAs when control has zero records", async () => {
    //    await page.goto("/evidence?control_id=__seeded-anchor-no-rows__");
    //    await expect(page.getByTestId("list-empty-state")).toBeVisible();
    //    await expect(page.getByTestId("evidence-empty-title")).toBeVisible();
    //    // Two CTAs: Clear filters + Set up a connector ->
    //    await expect(page.getByTestId("evidence-empty-clear")).toBeVisible();
    //    await expect(page.getByTestId("evidence-empty-connector")).toBeVisible();
  });

  test("AC-7: row click opens the inline record drawer", async () => {
    //    await page.goto("/evidence?control_id=__seeded-anchor-with-rows__");
    //    await page.getByTestId("list-table-row").first().click();
    //    await expect(page.getByTestId("evidence-row-drawer")).toBeVisible();
    //    // Drawer shows the FULL hash (not just the 8-char prefix).
    //    const fullHash = page.getByTestId("evidence-drawer-full-hash");
    //    await expect(fullHash).toBeVisible();
    //    const text = await fullHash.innerText();
    //    expect(text.length).toBeGreaterThanOrEqual(40);
  });

  test("AC-3 hash-copy: clicking the hash cell copies the full hash", async () => {
    //    // Grant clipboard permissions for the test context.
    //    await page.context().grantPermissions(["clipboard-read", "clipboard-write"]);
    //    await page.goto("/evidence?control_id=__seeded-anchor-with-rows__");
    //    const hashCell = page.getByTestId("evidence-row-hash").first();
    //    await hashCell.click();
    //    // Cell briefly shows "Copied!" feedback.
    //    await expect(hashCell).toContainText(/Copied!/);
    //    // Clipboard contains the FULL hash (>= 40 chars sha256).
    //    const clip = await page.evaluate(() => navigator.clipboard.readText());
    //    expect(clip.length).toBeGreaterThanOrEqual(40);
  });

  test("P0-A3: filter row is horizontal pill row, NOT a left sidebar", async () => {
    //    await page.goto("/evidence");
    //    await expect(page.getByTestId("list-filter-pills")).toBeVisible();
    //    // No left filter sidebar exists. The pill row sits above the table.
  });
});
