// #2059 — the risk register shows each risk's review date and marks the
// overdue ones.
//
// Every risk now gets a review date at creation (created + the
// organisation review cadence), so the Review due column reads dates, not
// "Pending evaluation". A date whose UTC day has passed carries an
// "Overdue" marker (the policy-review rule, #2075).
//
// HERMETIC: route-mocks the `/api/risks` BFF GET (slice 594 convention).
// The overdue date is far in the past and the upcoming one far in the
// future, so the result does not depend on the day the spec runs.

import { expect, test } from "./fixtures";

// Neutral test strings only (P0-A4 / GitGuardian). Shape mirrors
// `web/lib/api/risks.ts` `Risk`.
function mockRisk(id: string, title: string, reviewDueAt: string) {
  return {
    id,
    title,
    description: "",
    category: "operational",
    methodology: "nist_800_30",
    inherent_score: { likelihood: 3, impact: 3 },
    treatment: "mitigate",
    treatment_owner: "owner-a",
    residual_score: { likelihood: 2, impact: 2 },
    review_due_at: reviewDueAt,
    accepter: "",
    instrument_reference: "",
    linked_control_ids: [],
    created_at: "2020-01-01T00:00:00Z",
    updated_at: "2020-01-01T00:00:00Z",
    themes: [],
    severity: 9,
  };
}

test.describe("/risks review-due column (#2059)", () => {
  test("shows review dates and marks overdue ones", async ({ authedPage }) => {
    await authedPage.route(
      "**/api/risks-hierarchy/org-units**",
      async (route) => {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ org_units: [] }),
        });
      },
    );
    await authedPage.route(/\/api\/risks(\?.*)?$/, async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          risks: [
            mockRisk(
              "00000000-0000-4000-8000-000000002059",
              "Test risk overdue",
              "2021-01-01T00:00:00Z",
            ),
            mockRisk(
              "00000000-0000-4000-8000-000000002060",
              "Test risk upcoming",
              "2099-01-01T00:00:00Z",
            ),
          ],
        }),
      });
    });

    await authedPage.goto("/risks");

    // Desktop table only: the mobile card stack repeats every cell.
    const desktop = authedPage.locator('[data-testid="list-table-wrap"]');

    const dates = desktop.getByTestId("risks-row-review-due");
    await expect(dates).toHaveCount(2);
    await expect(dates).toContainText(["2021-01-01", "2099-01-01"], {
      ignoreCase: false,
    });

    const overdueRow = desktop.locator("tr", { hasText: "Test risk overdue" });
    await expect(overdueRow.getByTestId("risks-row-review-overdue")).toHaveText(
      "Overdue",
    );
    const upcomingRow = desktop.locator("tr", {
      hasText: "Test risk upcoming",
    });
    await expect(
      upcomingRow.getByTestId("risks-row-review-overdue"),
    ).toHaveCount(0);
    await expect(desktop.getByTestId("risks-row-review-overdue")).toHaveCount(
      1,
    );

    // No row reads "Pending evaluation" for its review date any more.
    await expect(
      desktop.getByTestId("risks-row-review-due-missing"),
    ).toHaveCount(0);
  });
});
