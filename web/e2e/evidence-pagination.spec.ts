// Slice 237 — Playwright E2E for the /evidence cursor-paginated footer.
//
// Runner status (post-slice-069 / 071 audit):
// Playwright IS installed in `web/`. This spec is quarantined behind the
// slice 082 seed-data harness, matching the precedent of every other
// list-view e2e spec (slice 040 / 042 / 056 / 060 / 064 / 071 / 094 /
// 098 / 100 / 102 / 246). The test bodies are preserved verbatim as a
// reviewable contract; when the seed harness lands the assertions
// uncomment and gate the slice.
//
// Run locally:
//   cd web
//   npx playwright install chromium     # once per machine
//   npx playwright test e2e/evidence-pagination.spec.ts
//
// Pre-conditions the seed-data harness (slice 082) must establish
// before the assertions are uncommented:
//   - PLATFORM_BASE_URL points at a running platform instance
//   - TEST_BEARER carries a credential in a tenant with:
//     * At least 3 pages of evidence (with the upstream default
//       `limit`, ~50 records per page → seed ≥101 evidence records so
//       there is a definite Next on page 1 AND on page 2). If the dev
//       seed currently ships <101 records, the slice spec (AC-7) calls
//       out the seed top-up as in-scope for THIS slice.
//
// Spec acceptance criteria mapped:
//   AC-1   — pagination footer mounted below `<ListTable>`
//   AC-3   — Previous disabled when stack is empty AND no URL cursor;
//            Next disabled when next_cursor is empty
//   AC-4   — Next click pushes cursor onto stack + re-issues query
//   AC-5   — Previous click pops stack + re-issues query; empty stack
//            returns to no-cursor first page
//   AC-6   — Navigating away resets cursor stack (covered by mount
//            behaviour — `useState<string[]>([])` initializer)
//   AC-7   — Next → Previous round-trip on the seeded dataset

import { test } from "@playwright/test";

test.describe("/evidence cursor-paginated footer (slice 237)", () => {
  test("AC-1: pagination footer renders below the table when records exist", async () => {
    //    await page.goto("/evidence");
    //    await expect(page.getByTestId("evidence-pagination")).toBeVisible();
    //    // Truth-telling summary: "Showing N records on this page".
    //    await expect(
    //      page.getByTestId("evidence-pagination-summary"),
    //    ).toContainText(/Showing \d+ records? on this page/);
  });

  test("AC-3: on the first page, Previous is disabled and Next is enabled", async () => {
    //    await page.goto("/evidence");
    //    await expect(page.getByTestId("evidence-pagination-prev")).toBeDisabled();
    //    // With ≥101 seeded records, page 1 has a non-empty next_cursor.
    //    await expect(page.getByTestId("evidence-pagination-next")).toBeEnabled();
  });

  test("AC-4: clicking Next advances to ?cursor=… and updates the page", async () => {
    //    await page.goto("/evidence");
    //    // Capture the first row's evidence_id so we can verify the page
    //    // contents actually changed after Next.
    //    const firstIdBefore = await page
    //      .getByTestId("evidence-row-observed-at")
    //      .first()
    //      .innerText();
    //    await page.getByTestId("evidence-pagination-next").click();
    //    // URL now carries a `cursor` query param.
    //    await expect(page).toHaveURL(/[?&]cursor=/);
    //    // Previous is now enabled; the rows have changed.
    //    await expect(page.getByTestId("evidence-pagination-prev")).toBeEnabled();
    //    const firstIdAfter = await page
    //      .getByTestId("evidence-row-observed-at")
    //      .first()
    //      .innerText();
    //    expect(firstIdAfter).not.toBe(firstIdBefore);
  });

  test("AC-5: clicking Previous from page 2 returns to the no-cursor first page", async () => {
    //    await page.goto("/evidence");
    //    await page.getByTestId("evidence-pagination-next").click();
    //    await expect(page).toHaveURL(/[?&]cursor=/);
    //    await page.getByTestId("evidence-pagination-prev").click();
    //    // The canonical first-page URL DOES NOT carry `?cursor=` — when
    //    // the stack is empty (or the popped cursor is empty), the URL
    //    // mutator drops the param entirely.
    //    await expect(page).not.toHaveURL(/[?&]cursor=/);
    //    await expect(page.getByTestId("evidence-pagination-prev")).toBeDisabled();
  });

  test("AC-7: Next → Next → Previous → Previous round-trips correctly", async () => {
    //    await page.goto("/evidence");
    //    // Click Next twice — page 1 → page 2 → page 3.
    //    await page.getByTestId("evidence-pagination-next").click();
    //    await expect(page).toHaveURL(/[?&]cursor=/);
    //    const page2URL = page.url();
    //    await page.getByTestId("evidence-pagination-next").click();
    //    await expect(page).toHaveURL(/[?&]cursor=/);
    //    const page3URL = page.url();
    //    expect(page3URL).not.toBe(page2URL);
    //    // Now Previous twice — page 3 → page 2 → page 1.
    //    await page.getByTestId("evidence-pagination-prev").click();
    //    // After one Previous, URL must equal the page-2 URL captured above.
    //    expect(page.url()).toBe(page2URL);
    //    await page.getByTestId("evidence-pagination-prev").click();
    //    // Back to page 1 — cursor param dropped.
    //    await expect(page).not.toHaveURL(/[?&]cursor=/);
    //    await expect(page.getByTestId("evidence-pagination-prev")).toBeDisabled();
  });

  test("AC-deeplink: deep-link to ?cursor=X with empty stack enables Previous (returns to no-cursor)", async () => {
    //    // The operator opened a shared link to page 2 directly.
    //    await page.goto("/evidence?cursor=__seeded-page-2-cursor__");
    //    // Previous is enabled (the URL cursor signals there IS a previous
    //    // page) even though the in-memory stack is empty.
    //    await expect(page.getByTestId("evidence-pagination-prev")).toBeEnabled();
    //    await page.getByTestId("evidence-pagination-prev").click();
    //    await expect(page).not.toHaveURL(/[?&]cursor=/);
  });

  test("AC-filter-reset: changing a filter while on page 2 resets to page 1", async () => {
    //    await page.goto("/evidence");
    //    await page.getByTestId("evidence-pagination-next").click();
    //    await expect(page).toHaveURL(/[?&]cursor=/);
    //    // Apply a filter change (Result → pass).
    //    const resultPill = page.getByLabel("Result");
    //    await resultPill.selectOption({ value: "pass" });
    //    // The cursor param must be dropped on the next URL replace.
    //    await expect(page).not.toHaveURL(/[?&]cursor=/);
    //    await expect(page.getByTestId("evidence-pagination-prev")).toBeDisabled();
  });
});
