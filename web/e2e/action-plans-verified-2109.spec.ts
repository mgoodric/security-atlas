// #2109 — action plans verified from evidence, as the control page shows
// them. The platform verifies a completed plan once every linked control
// passes its latest evaluation, and returns it to completed when one
// fails; the control page's "Linked Action Plans" section then reads
// Verified only while the control passes, and "Completed · linked
// controls still failing" otherwise.
//
// MOCK STRATEGY: the control page is the seeded demo control, signed in
// through the authedPage fixture. Only its linked-plans BFF
// (/api/controls/{id}/action-plans) is mocked, with what the API returns
// for the control passing or failing. The Go tier proves the evaluation
// makes those moves against Postgres
// (internal/actionplan/reconcile_integration_test.go).
//
// Hard rule (P0-A9 / GitGuardian): every id is a neutral test string.

import type { Page, Route } from "@playwright/test";

import { expect, seeded, test } from "./fixtures";

const PLAN_ID = "d4d4d4d4-0000-4000-8000-000000002109";
const PLAN_TITLE = "Rotate the deploy keys (test-2109)";

// What GET /v1/controls/{id}/action-plans returns for the linked plan,
// by whether the control passes its latest evaluation.
function linkedPlan(controlPasses: boolean) {
  return controlPasses
    ? { status: "verified", controls_failing: false }
    : { status: "completed", controls_failing: true };
}

async function mockLinkedPlans(page: Page, controlPasses: boolean) {
  await page.route(
    `**/api/controls/${seeded.controlId}/action-plans`,
    (route: Route) =>
      route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          action_plans: [
            {
              id: PLAN_ID,
              title: PLAN_TITLE,
              due_date: "2027-01-15",
              ...linkedPlan(controlPasses),
            },
          ],
          count: 1,
        }),
      }),
  );
}

test.describe("control page: linked plan verified from evidence (#2109)", () => {
  test("the linked plan reads Verified when the control passes", async ({
    authedPage: page,
  }) => {
    await mockLinkedPlans(page, true);
    await page.goto(`/controls/${seeded.controlId}`);
    const row = page
      .getByTestId("linked-action-plan")
      .filter({ hasText: PLAN_TITLE });
    await expect(row.getByTestId("linked-action-plan-status")).toHaveText(
      "Verified",
      { timeout: 30_000 },
    );
  });

  test("the linked plan is not Verified while the control fails", async ({
    authedPage: page,
  }) => {
    await mockLinkedPlans(page, false);
    await page.goto(`/controls/${seeded.controlId}`);
    const status = page
      .getByTestId("linked-action-plan")
      .filter({ hasText: PLAN_TITLE })
      .getByTestId("linked-action-plan-status");
    await expect(status).toHaveText(
      "Completed · linked controls still failing",
      { timeout: 30_000 },
    );
    await expect(status).not.toHaveText("Verified");
  });
});
