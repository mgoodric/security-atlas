// #2085 — Playwright: action plans, audit periods and personnel show a
// person's name, never a raw user ID, `user:` subject or `jwt:` ID.
//
// Against the real platform, no route mocks. The fixture
// (fixtures/e2e/person-names.sql) seeds a named user who owns an action
// plan, created an audit period (recorded as a `user:` subject) and
// completed a personnel checklist item. <Person> resolves each through
// GET /v1/people.
//
// Hard rule (P0-A9 / GitGuardian): every id is a neutral test string.

import type { Page } from "@playwright/test";

import { expect, test } from "./fixtures";
import { seedFromFixture } from "./seed";

const USER_ID = "44444444-4444-4444-4444-444444442085";
const USER_NAME = "Person Names E2E";
const PLAN_ID = "acacacac-acac-acac-acac-acacacac2085";
const PERIOD_NAME = "Person names e2e period (2085)";
const CHECKLIST_ID = "cdcdcdcd-cdcd-cdcd-cdcd-cdcdcdcd2085";

// No visible text on the page is a raw user ID, a truncated one, a
// `user:` subject or a `jwt:` credential ID.
async function expectNoRawIds(page: Page): Promise<void> {
  const text = await page.locator("main").innerText();
  expect(text).not.toContain(USER_ID);
  expect(text).not.toContain(USER_ID.slice(0, 8));
  expect(text).not.toMatch(/user:[0-9a-f-]{8,}/i);
  expect(text).not.toMatch(/jwt:\S+/i);
}

test.beforeAll(() => {
  seedFromFixture("person-names");
});

test("action plans show the owner's name", async ({ authedPage: page }) => {
  await page.goto("/action-plans");
  const row = page
    .getByTestId("list-table-row")
    .filter({ hasText: "Person names e2e plan (2085)" });
  await expect(row.getByTestId("action-plans-row-owner")).toHaveText(USER_NAME);
  await expect(row.getByTestId("action-plans-row-owner")).toHaveAttribute(
    "data-person-kind",
    "user",
  );
  await expectNoRawIds(page);

  await page.goto(`/action-plans/${PLAN_ID}`);
  await expect(page.getByTestId("action-plan-detail-owner")).toContainText(
    USER_NAME,
  );
  await expectNoRawIds(page);
});

test("audit periods show who created them, from a user: subject", async ({
  authedPage: page,
}) => {
  await page.goto("/audits");
  const row = page
    .getByTestId("list-table-row")
    .filter({ hasText: PERIOD_NAME });
  await expect(row.getByTestId("audits-row-created-by")).toHaveText(USER_NAME);
  await expectNoRawIds(page);
});

test("personnel checklist items show who completed them", async ({
  authedPage: page,
}) => {
  await page.goto(`/personnel-security/${CHECKLIST_ID}`);
  await expect(page.getByTestId("personnel-item-completed-by")).toHaveText(
    `By ${USER_NAME}`,
  );
  await expectNoRawIds(page);
});
