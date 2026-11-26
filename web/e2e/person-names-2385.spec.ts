// #2385 — Playwright: the dashboard activity feed, the activity page,
// exceptions and a board pack show a person's name, never a raw user ID,
// `user:` subject or `jwt:` ID.
//
// Against the real platform, no route mocks. The fixture
// (fixtures/e2e/person-names-2385.sql) seeds a named user who requested
// an exception (as a `user:` subject), whose request history feeds the
// dashboard and the activity page, and who published a board pack.
// <Person> resolves each through GET /v1/people.
//
// Hard rule (P0-A9 / GitGuardian): every id is a neutral test string.

import type { Page } from "@playwright/test";

import { expect, test } from "./fixtures";
import { seedFromFixture } from "./seed";

const USER_ID = "55555555-5555-4555-8555-555555552385";
const USER_NAME = "Remaining Pages E2E";
const EXCEPTION_ID = "e2385e23-85e2-485e-8385-e2385e2385e2";
const PACK_ID = "b2385b23-85b2-485b-8385-b2385b2385b2";

// No visible text on the page is the user's raw ID, a truncated one, a
// `user:` subject or a `jwt:` credential ID.
async function expectNoRawIds(page: Page): Promise<void> {
  const text = await page.locator("body").innerText();
  expect(text).not.toContain(USER_ID);
  expect(text).not.toContain(USER_ID.slice(0, 8));
  expect(text).not.toMatch(/user:[0-9a-f-]{8,}/i);
  expect(text).not.toMatch(/jwt:\S+/i);
}

test.beforeAll(() => {
  seedFromFixture("person-names-2385");
});

test("the dashboard activity feed names the actor", async ({
  authedPage: page,
}) => {
  await page.goto("/dashboard");
  const feed = page.getByTestId("activity-feed-list");
  await expect(
    feed.locator('[data-person-kind="user"]', { hasText: USER_NAME }).first(),
  ).toBeVisible({ timeout: 30_000 });
  await expectNoRawIds(page);
});

test("the activity page names the actor", async ({ authedPage: page }) => {
  await page.goto("/activity");
  await expect(
    page
      .locator('main [data-person-kind="user"]', { hasText: USER_NAME })
      .first(),
  ).toBeVisible({ timeout: 30_000 });
  await expectNoRawIds(page);
});

test("exceptions name the requester, in the list and on the detail", async ({
  authedPage: page,
}) => {
  await page.goto("/exceptions");
  const row = page
    .getByTestId("list-table-row")
    .filter({ hasText: EXCEPTION_ID.slice(0, 8) });
  await expect(row.getByTestId("exceptions-row-requested-by")).toHaveText(
    USER_NAME,
    { timeout: 30_000 },
  );
  await expectNoRawIds(page);

  await page.goto(`/exceptions/${EXCEPTION_ID}`);
  await expect(page.getByTestId("exception-requested-by")).toHaveText(
    USER_NAME,
  );
  await expect(page.getByTestId("exception-audit-actor").first()).toHaveText(
    USER_NAME,
  );
  await expectNoRawIds(page);
});

test("a board pack names who published it", async ({ authedPage: page }) => {
  await page.goto(`/board-packs/${PACK_ID}`);
  await expect(page.getByTestId("pack-header-published-by")).toHaveText(
    USER_NAME,
    { timeout: 30_000 },
  );
  await expect(page.getByTestId("publish-footer-published-by")).toHaveText(
    USER_NAME,
  );
  await expectNoRawIds(page);
});
