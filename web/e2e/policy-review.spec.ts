// #1806 — Playwright E2E for policy review dates on the list view.
//
// The policy list shows each policy's next review date with an overdue /
// due-soon marker, and a Review filter narrows to the overdue policies:
//
//   1. An overdue policy shows "Overdue", a due-soon one "Due soon", and
//      an upcoming or undated one no marker.
//   2. The Review filter set to overdue (`?review=overdue`, also what the
//      pill writes) leaves exactly the overdue policy.
//
// Hermetic mock discipline (slice 594): the list BFF is route-mocked with
// a body hand-written to the production wire shape (`wireFromPolicy` in
// internal/api/policies/handlers.go, including the computed
// `review_status`), so the assertions do not depend on shared-DB seed
// state or on today's date.

import { expect, test } from "./fixtures";

function policy(
  id: string,
  title: string,
  nextReviewAt: string | null,
  reviewStatus: string | null,
) {
  return {
    id,
    title,
    version: "1.0",
    body_md: `# ${title}\n`,
    owner_role: "security_lead",
    approver_role: "cto",
    linked_control_ids: ["00000000-0000-0000-0000-00000000c001"],
    acknowledgment_required_roles: [],
    status: "published",
    source_attribution: "tenant_authored",
    created_by: "user-1",
    effective_date: "2026-01-15",
    published_at: "2026-01-15T00:00:00Z",
    ...(nextReviewAt ? { next_review_at: nextReviewAt } : {}),
    ...(reviewStatus ? { review_status: reviewStatus } : {}),
    imported: false,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-15T00:00:00Z",
    ack_rate: null,
  };
}

const OVERDUE = policy(
  "00000000-0000-0000-0000-000000001806",
  "Overdue Review Policy",
  "2026-03-01T00:00:00Z",
  "overdue",
);
const DUE_SOON = policy(
  "00000000-0000-0000-0000-000000001807",
  "Due Soon Review Policy",
  "2026-12-01T00:00:00Z",
  "due-soon",
);
const UPCOMING = policy(
  "00000000-0000-0000-0000-000000001808",
  "Upcoming Review Policy",
  "2027-06-01T00:00:00Z",
  "upcoming",
);
const UNDATED = policy(
  "00000000-0000-0000-0000-000000001809",
  "Undated Review Policy",
  null,
  null,
);

test.describe("policy review dates (#1806)", () => {
  test.beforeEach(async ({ authedPage: page }) => {
    await page.route("**/api/policies", (route) => {
      if (route.request().method() !== "GET") return route.fallback();
      return route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          policies: [OVERDUE, DUE_SOON, UPCOMING, UNDATED],
        }),
      });
    });
  });

  test("the list shows the next review date with an overdue / due-soon marker", async ({
    authedPage: page,
  }) => {
    await page.goto("/policies");
    const rows = page.locator("tr", {
      has: page.getByTestId("policies-row-title"),
    });
    const row = (title: string) => rows.filter({ hasText: title });
    await expect(row(OVERDUE.title)).toHaveCount(1, { timeout: 30_000 });

    await expect(
      row(OVERDUE.title).getByTestId("policies-row-next-review"),
    ).toHaveText("2026-03-01");
    await expect(
      row(OVERDUE.title).getByTestId("policies-row-review-marker"),
    ).toHaveText("Overdue");
    await expect(
      row(DUE_SOON.title).getByTestId("policies-row-review-marker"),
    ).toHaveText("Due soon");
    await expect(
      row(UPCOMING.title).getByTestId("policies-row-review-marker"),
    ).toHaveCount(0);
    await expect(
      row(UNDATED.title).getByTestId("policies-row-next-review-missing"),
    ).toBeVisible();
    await expect(
      row(UNDATED.title).getByTestId("policies-row-review-marker"),
    ).toHaveCount(0);
  });

  test("the Review filter narrows to exactly the overdue policies", async ({
    authedPage: page,
  }) => {
    await page.goto("/policies");
    const titles = page.getByTestId("policies-row-title");
    await expect(titles).toHaveCount(4, { timeout: 30_000 });

    await expect(page.getByTestId("list-filter-pill-review")).toBeVisible();
    await page.getByLabel("Review", { exact: true }).selectOption("overdue");

    await expect(page).toHaveURL(/[?&]review=overdue(&|$)/);
    await expect(titles).toHaveCount(1);
    await expect(titles).toHaveText(OVERDUE.title);
  });
});
