// Slice 098 — Playwright E2E for the /controls list view.
//
// Runner status (post-slice-069 / 071 audit):
// Playwright IS installed in `web/`. This spec is quarantined behind
// slice 082 (the seed-data harness) per slice 079's decision; when that
// harness lands, the un-commented assertions below become the gate. The
// test bodies are preserved verbatim as a reviewable contract per the
// slice 040 / 042 / 056 / 060 / 064 / 071 / 094 precedent.
//
// Run locally:
//   cd web
//   npx playwright install chromium     # once per machine
//   npx playwright test e2e/controls-list.spec.ts
//
// Pre-conditions the seed-data harness (slice 082) must establish
// before the commented assertions are turned on:
//   - PLATFORM_BASE_URL points at a running platform instance
//   - TEST_BEARER carries a credential in a tenant that has at least
//     the seeded SCF anchor catalog loaded (slice 006 SCF import is
//     enough — no per-tenant control instantiation required for v1).
//   - At least three families represented (the design assumes the user
//     can filter by family).
//
// AC-8 coverage targets: list renders, filter narrows results, empty
// state appears on no-match, row click navigates.

import { test } from "@playwright/test";

test.describe("/controls list view", () => {
  test("AC-1: /controls renders the anchor table for any signed-in user", async () => {
    //    await page.goto("/login");
    //    await page.fill('input[name="token"]', process.env.TEST_BEARER!);
    //    await page.click("button[type=submit]");
    //    await page.goto("/controls");
    //    await expect(page.getByRole("heading", { name: /Controls/ })).toBeVisible();
    //    await expect(page.getByTestId("list-page")).toBeVisible();
    //    await expect(page.getByTestId("list-table-wrap")).toBeVisible();
  });

  test("AC-3: horizontal pill filter row narrows the result set", async () => {
    //    await page.goto("/controls");
    //    const initial = await page.getByTestId("list-table-row").count();
    //    const familyPill = page.getByLabel("Family");
    //    await familyPill.selectOption({ index: 1 });   // first non-ALL option
    //    await page.waitForLoadState("networkidle");
    //    const filtered = await page.getByTestId("list-table-row").count();
    //    expect(filtered).toBeLessThan(initial);
    // The filter row is horizontal (P0-A2) — verify NO left sidebar.
    //    await expect(page.getByTestId("list-filter-pills")).toBeVisible();
  });

  test("AC-4: empty state surfaces when filters return zero rows", async () => {
    //    await page.goto("/controls?family=DOES-NOT-EXIST");
    //    await expect(page.getByTestId("list-empty-state")).toBeVisible();
    //    await expect(
    //      page.getByText("No controls match these filters"),
    //    ).toBeVisible();
    // The CTA reads "Clear filters" and clearing returns the user to a populated table.
    //    await page.getByTestId("list-empty-state-cta").click();
    //    await expect(page.getByTestId("list-table-wrap")).toBeVisible();
  });

  test("AC-6: row click navigates to /controls/[id]", async () => {
    //    await page.goto("/controls");
    //    const firstRow = page.getByTestId("list-table-row").first();
    //    const scfIdLink = firstRow.getByTestId("controls-row-scf-id");
    //    const href = await scfIdLink.getAttribute("href");
    //    expect(href).toMatch(/^\/controls\//);
    //    await scfIdLink.click();
    //    await expect(page).toHaveURL(/\/controls\/[^/]+$/);
  });
});
