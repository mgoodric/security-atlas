// Slice 515 — Playwright E2E for the /csf/[framework_version_id] assessment
// page (NIST CSF 2.0 Tier + Current-vs-Target gap view).
//
// Assertions are driven by page.route() mocking of the BFF GET at
// /api/csf/gap (the b219 project lesson: a server-backed Playwright spec must
// route-mock the BFF GET, NOT rely on the shared docker-compose seed). The Go
// integration tests (515) assert the real RLS-isolated behaviour against
// Postgres; this spec asserts the page renders the gap rows + tier the BFF
// returns.
//
// Coverage:
//   (a) the tier card renders the rated tier;
//   (b) the gap table renders one row per Subcategory with the gap delta;
//   (c) the empty state renders when no selections exist.

import { expect, test } from "./fixtures";

const FV = "11111111-1111-4111-8111-111111111111";

const GAP_BODY = {
  framework_version_id: FV,
  gap: [
    {
      subcategory_code: "GV.OC-01",
      subcategory_title: "Organizational context",
      requirement_id: "22222222-2222-4222-8222-222222222222",
      current_outcome: "partial",
      target_outcome: "fully",
      gap_delta: 2,
      met: false,
    },
    {
      subcategory_code: "PR.AA-01",
      subcategory_title: "Identities managed",
      requirement_id: "33333333-3333-4333-8333-333333333333",
      current_outcome: "fully",
      target_outcome: "largely",
      gap_delta: -1,
      met: true,
    },
  ],
  gap_count: 2,
  tier_rating: {
    id: "44444444-4444-4444-8444-444444444444",
    framework_version_id: FV,
    tier: "tier3_repeatable",
    rationale: "established",
    rated_by: "u-1",
    rated_at: "2026-06-08T00:00:00Z",
  },
};

const EMPTY_BODY = {
  framework_version_id: FV,
  gap: [],
  gap_count: 0,
  tier_rating: null,
};

test.describe("CSF 2.0 assessment page", () => {
  test("renders tier + Current-vs-Target gap table", async ({ authedPage }) => {
    await authedPage.route("**/api/csf/gap*", (route) =>
      route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(GAP_BODY),
      }),
    );

    await authedPage.goto(`/csf/${FV}`);

    await expect(authedPage.getByTestId("csf-assessment")).toBeVisible();
    await expect(authedPage.getByTestId("csf-tier-value")).toContainText(
      "Tier 3",
    );

    const table = authedPage.getByTestId("csf-gap-table");
    await expect(table).toBeVisible();
    await expect(authedPage.getByTestId("csf-gap-row-GV.OC-01")).toContainText(
      "+2",
    );
    await expect(authedPage.getByTestId("csf-gap-row-PR.AA-01")).toContainText(
      "Met",
    );
  });

  test("renders the empty state when no selections exist", async ({
    authedPage,
  }) => {
    await authedPage.route("**/api/csf/gap*", (route) =>
      route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(EMPTY_BODY),
      }),
    );

    await authedPage.goto(`/csf/${FV}`);

    await expect(authedPage.getByTestId("csf-gap-empty")).toBeVisible();
    await expect(authedPage.getByTestId("csf-tier-empty")).toBeVisible();
  });
});
