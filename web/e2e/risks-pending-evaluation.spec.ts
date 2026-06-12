// Slice 680 / ATLAS-029 — Playwright E2E pinning the new-risk
// "Pending evaluation" affordance on the /risks list.
//
// A freshly-created risk has no residual_score (the create path stores an
// empty `{}` JSONB) and no review_due_at until the evaluator backfills
// them. The old cell rendered a bare "—", which reads as broken. This
// slice surfaces an explicit "Pending evaluation" state instead.
//
// This spec is HERMETIC: it route-mocks the `/api/risks` BFF GET so it
// does not depend on the shared docker-compose seed (slice 594 shared-DB
// → hermetic-mock convention). It mocks two rows:
//
//   - a PENDING risk: residual_score = {} (the new-risk shape), no
//     review_due_at — both cells must read "Pending evaluation", not "—".
//   - a SCORED risk: a real {likelihood, impact} residual + a review_due
//     date — the residual renders its 0..1 magnitude and the review-due
//     renders the date, NOT the pending affordance.

import { expect, test } from "./fixtures";

// Neutral test strings only (P0-A4 / GitGuardian). Shape mirrors
// `web/lib/api/risks.ts` `Risk`.
function mockRisks() {
  return [
    {
      // Pending: empty residual + no review_due_at (the new-risk shape).
      id: "00000000-0000-4000-8000-000000000001",
      title: "Test risk pending",
      description: "Neutral pending risk",
      category: "operational",
      methodology: "nist_800_30",
      inherent_score: { likelihood: 3, impact: 3 },
      treatment: "mitigate",
      treatment_owner: "owner-a",
      residual_score: {},
      accepter: "",
      instrument_reference: "",
      linked_control_ids: [],
      created_at: "2026-01-01T00:00:00Z",
      updated_at: "2026-01-01T00:00:00Z",
      themes: [],
      severity: 9,
    },
    {
      // Scored: a real residual + a review-due date.
      id: "00000000-0000-4000-8000-000000000002",
      title: "Test risk scored",
      description: "Neutral scored risk",
      category: "operational",
      methodology: "nist_800_30",
      inherent_score: { likelihood: 4, impact: 5 },
      treatment: "mitigate",
      treatment_owner: "owner-b",
      residual_score: { likelihood: 2, impact: 3 },
      review_due_at: "2026-09-30T00:00:00Z",
      accepter: "",
      instrument_reference: "",
      linked_control_ids: [],
      created_at: "2026-01-01T00:00:00Z",
      updated_at: "2026-01-01T00:00:00Z",
      themes: [],
      severity: 20,
    },
  ];
}

test.describe("/risks new-risk pending-evaluation state (slice 680)", () => {
  test("a pending risk shows 'Pending evaluation' for residual + review-due, not a bare dash", async ({
    authedPage,
  }) => {
    // The page also fetches org-units for the Org unit pill; mock it so
    // no network reaches upstream. Registered FIRST so the broader
    // /api/risks pattern below does not swallow it.
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
        body: JSON.stringify({ risks: mockRisks() }),
      });
    });

    await authedPage.goto("/risks");

    // The risks table renders BOTH a desktop table (`list-table-wrap`)
    // and a mobile card stack (`list-cards-wrap`, slice 281
    // `mobileMode="cards"`) into the DOM at once — CSS hides one per
    // breakpoint — so every cell testid appears twice. Scope to the
    // desktop table so the per-row assertions resolve to exactly one
    // element regardless of viewport.
    const desktop = authedPage.locator('[data-testid="list-table-wrap"]');

    // The pending risk's residual + review-due cells read the explicit
    // affordance, exactly once each (one pending row in the desktop table).
    const pendingResidual = desktop.getByTestId("risks-row-residual-pending");
    await expect(pendingResidual).toHaveCount(1);
    await expect(pendingResidual).toContainText("Pending evaluation");

    const pendingReviewDue = desktop.getByTestId(
      "risks-row-review-due-pending",
    );
    await expect(pendingReviewDue).toHaveCount(1);
    await expect(pendingReviewDue).toContainText("Pending evaluation");

    // The scored risk renders a real residual magnitude (2×3/25 = 0.24),
    // NOT the pending affordance.
    const scoredResidual = desktop.getByTestId("risks-row-residual");
    await expect(scoredResidual).toHaveCount(1);
    await expect(scoredResidual).toContainText("0.24");

    // Anti-criterion / honesty: the pending affordance appears exactly
    // twice in the desktop table (residual + review-due of the one
    // pending row), and the old bare em-dash is gone from those cells.
    await expect(desktop.getByText("Pending evaluation")).toHaveCount(2);
  });

  test("column headers name the independent axes (Inherent severity vs Residual after controls)", async ({
    authedPage,
  }) => {
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
        body: JSON.stringify({ risks: mockRisks() }),
      });
    });

    await authedPage.goto("/risks");

    // Slice 680 / ATLAS-038: the headers explicitly name the axes so the
    // residual-vs-severity columns no longer read as inconsistent. Scope
    // to the desktop table header (the mobile card stack repeats the
    // column labels as inline `<dt>`s, so the page-wide text appears
    // more than once).
    const desktop = authedPage.locator('[data-testid="list-table-wrap"]');
    await expect(
      desktop.getByText("Inherent severity", { exact: true }),
    ).toBeVisible();
    await expect(
      desktop.getByText("Residual (after controls)", { exact: true }),
    ).toBeVisible();
  });
});
