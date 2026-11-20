// Slice 237 — Playwright E2E for the /evidence cursor-paginated footer.
//
// Seeded by fixtures/e2e/evidence-pagination.sql: a tenant of the spec's
// own with 120 evidence records, one minute apart, results alternating
// pass / fail. With the upstream page size of 50 that is three pages
// (50, 50, 20). The spec signs in to that tenant through
// /v1/test/issue-jwt (web/e2e/tenant-session.ts). #2755 restored the
// assertions slice 237 left commented out behind the slice-082
// quarantine.
//
// Run locally:
//   cd web
//   npx playwright install chromium     # once per machine
//   npx playwright test e2e/evidence-pagination.spec.ts
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

import type { Page } from "@playwright/test";

import { expect, test } from "./fixtures";
import { seedFromFixture } from "./seed";
import { signInToTenant } from "./tenant-session";

// Mirrors the tenant in fixtures/e2e/evidence-pagination.sql.
const TENANT = "00000000-0000-0000-0000-2755c0000002";
const PAGE_SIZE = 50;
const LAST_PAGE_SIZE = 20;

// The table branch only: at `mobileMode="cards"` the list also mounts a
// hidden card stack that repeats every cell testid.
function rows(page: Page) {
  return page.getByTestId("list-table-wrap").getByTestId("list-table-row");
}

// The first row's hash prefix identifies the page being shown (every
// seeded record has a distinct hash).
function firstHash(page: Page) {
  return page
    .getByTestId("list-table-wrap")
    .getByTestId("evidence-row-hash")
    .first();
}

async function openFirstPage(page: Page): Promise<string> {
  await page.goto("/evidence");
  await expect(rows(page)).toHaveCount(PAGE_SIZE, { timeout: 30_000 });
  return firstHash(page).innerText();
}

test.describe("/evidence cursor-paginated footer (slice 237)", () => {
  test.beforeAll(() => {
    seedFromFixture("evidence-pagination");
  });

  test.beforeEach(async ({ page }) => {
    await signInToTenant(page, {
      tenantId: TENANT,
      roles: ["admin"],
      superAdmin: true,
    });
  });

  test("AC-1: pagination footer renders below the table when records exist", async ({
    page,
  }) => {
    await openFirstPage(page);
    const footer = page.getByTestId("evidence-pagination");
    await expect(footer).toBeVisible();
    // Truth-telling summary: "Showing N records on this page".
    await expect(page.getByTestId("evidence-pagination-summary")).toHaveText(
      `Showing ${PAGE_SIZE} records on this page`,
    );
    const tbl = await page.getByTestId("list-table-wrap").boundingBox();
    const foot = await footer.boundingBox();
    expect(tbl).not.toBeNull();
    expect(foot).not.toBeNull();
    expect(foot!.y).toBeGreaterThanOrEqual(tbl!.y + tbl!.height);
  });

  test("AC-3: on the first page, Previous is disabled and Next is enabled", async ({
    page,
  }) => {
    await openFirstPage(page);
    await expect(page.getByTestId("evidence-pagination-prev")).toBeDisabled();
    // With 120 seeded records, page 1 has a non-empty next_cursor.
    await expect(page.getByTestId("evidence-pagination-next")).toBeEnabled();
  });

  test("AC-4: clicking Next advances to ?cursor=… and updates the page", async ({
    page,
  }) => {
    const before = await openFirstPage(page);
    await page.getByTestId("evidence-pagination-next").click();
    // URL now carries a `cursor` query param.
    await expect(page).toHaveURL(/[?&]cursor=/);
    // Previous is now enabled; the rows have changed.
    await expect(page.getByTestId("evidence-pagination-prev")).toBeEnabled();
    await expect(firstHash(page)).not.toHaveText(before);
    await expect(rows(page)).toHaveCount(PAGE_SIZE);
  });

  test("AC-5: clicking Previous from page 2 returns to the no-cursor first page", async ({
    page,
  }) => {
    const first = await openFirstPage(page);
    await page.getByTestId("evidence-pagination-next").click();
    await expect(page).toHaveURL(/[?&]cursor=/);
    await expect(firstHash(page)).not.toHaveText(first);
    await page.getByTestId("evidence-pagination-prev").click();
    // The canonical first-page URL DOES NOT carry `?cursor=` — when
    // the stack is empty (or the popped cursor is empty), the URL
    // mutator drops the param entirely.
    await expect(page).not.toHaveURL(/[?&]cursor=/);
    await expect(page.getByTestId("evidence-pagination-prev")).toBeDisabled();
    await expect(firstHash(page)).toHaveText(first);
  });

  test("AC-7: Next → Next → Previous → Previous round-trips correctly", async ({
    page,
  }) => {
    const page1Hash = await openFirstPage(page);
    // Click Next twice — page 1 → page 2 → page 3.
    await page.getByTestId("evidence-pagination-next").click();
    await expect(page).toHaveURL(/[?&]cursor=/);
    await expect(firstHash(page)).not.toHaveText(page1Hash);
    const page2URL = page.url();
    const page2Hash = await firstHash(page).innerText();
    await page.getByTestId("evidence-pagination-next").click();
    await expect(page).not.toHaveURL(page2URL);
    await expect(page).toHaveURL(/[?&]cursor=/);
    // Page 3 is the last: 20 rows and no Next.
    await expect(rows(page)).toHaveCount(LAST_PAGE_SIZE);
    await expect(page.getByTestId("evidence-pagination-next")).toBeDisabled();
    // Now Previous twice — page 3 → page 2 → page 1.
    await page.getByTestId("evidence-pagination-prev").click();
    // After one Previous, URL must equal the page-2 URL captured above.
    await expect(page).toHaveURL(page2URL);
    await expect(firstHash(page)).toHaveText(page2Hash);
    await page.getByTestId("evidence-pagination-prev").click();
    // Back to page 1 — cursor param dropped.
    await expect(page).not.toHaveURL(/[?&]cursor=/);
    await expect(page.getByTestId("evidence-pagination-prev")).toBeDisabled();
    await expect(firstHash(page)).toHaveText(page1Hash);
  });

  test("AC-deeplink: deep-link to ?cursor=X with empty stack enables Previous (returns to no-cursor)", async ({
    page,
  }) => {
    // Capture a real page-2 cursor, then open it as a shared link would:
    // a fresh load, so the in-memory stack is empty.
    const page1Hash = await openFirstPage(page);
    await page.getByTestId("evidence-pagination-next").click();
    await expect(page).toHaveURL(/[?&]cursor=/);
    const cursor = new URL(page.url()).searchParams.get("cursor");
    expect(cursor).toBeTruthy();
    await page.goto(`/evidence?cursor=${encodeURIComponent(cursor!)}`);
    await expect(rows(page)).toHaveCount(PAGE_SIZE, { timeout: 30_000 });
    await expect(firstHash(page)).not.toHaveText(page1Hash);
    // Previous is enabled (the URL cursor signals there IS a previous
    // page) even though the in-memory stack is empty.
    await expect(page.getByTestId("evidence-pagination-prev")).toBeEnabled();
    await page.getByTestId("evidence-pagination-prev").click();
    await expect(page).not.toHaveURL(/[?&]cursor=/);
    await expect(firstHash(page)).toHaveText(page1Hash);
  });

  test("AC-filter-reset: changing a filter while on page 2 resets to page 1", async ({
    page,
  }) => {
    await openFirstPage(page);
    await page.getByTestId("evidence-pagination-next").click();
    await expect(page).toHaveURL(/[?&]cursor=/);
    await expect(page.getByTestId("evidence-pagination-prev")).toBeEnabled();
    // Apply a filter change (Result → pass).
    await page
      .getByTestId("list-filter-pills")
      .getByLabel("Result")
      .selectOption({ value: "pass" });
    // The cursor param must be dropped on the next URL replace.
    await expect(page).toHaveURL(/[?&]result=pass/);
    await expect(page).not.toHaveURL(/[?&]cursor=/);
    await expect(page.getByTestId("evidence-pagination-prev")).toBeDisabled();
    // Page 1 of the 60 passing records: a full page, every row a pass.
    await expect(rows(page)).toHaveCount(PAGE_SIZE);
    await expect(
      page
        .getByTestId("list-table-wrap")
        .getByTestId("evidence-row-result")
        .filter({ hasNotText: "Pass" }),
    ).toHaveCount(0);
  });
});
