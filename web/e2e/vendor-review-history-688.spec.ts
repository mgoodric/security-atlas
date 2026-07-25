// Slice 688 — Playwright E2E for the vendor review-history timeline.
//
//   - AC-4/AC-7: the detail page's "Review history" card renders a
//     multi-row timeline (reviewer + date + outcome per row) from a mocked
//     `/api/vendors/{id}/reviews` series, newest-first.
//   - Empty series falls back to the honest scalar message.
//   - AC-5: the "Record review" affordance routes to the record form.
//
// Hermetic mock pattern (feedback_e2e_shared_db_hermetic_mock, slice 594):
// every BFF response is route-mocked so the assertions do not depend on the
// slice-205 demo seed in the shared docker-compose DB.

import { expect, test } from "./fixtures";
import type { Page } from "@playwright/test";

const VENDOR = {
  id: "00000000-0000-0000-0000-0000000000cc",
  name: "Northgate Systems",
  domain: "northgate.example",
  criticality: "high",
  contract_start: "2026-01-01",
  contract_end: "2026-12-31",
  dpa_signed: true,
  dpa_signed_at: "2026-01-05",
  review_cadence: "quarterly",
  last_review_date: "2026-05-01",
  overdue: false,
  owner_user: "owner@demo.example",
  linked_sow_uri: null,
  notes: "",
  scope_cell_ids: [],
  created_at: "2026-01-01T00:00:00Z",
  updated_at: "2026-05-01T00:00:00Z",
};

const REVIEWS = [
  {
    id: "10000000-0000-0000-0000-000000000003",
    vendor_id: VENDOR.id,
    reviewed_at: "2026-05-01",
    reviewer: "owner@demo.example",
    outcome: "pass",
    notes: "Annual SOC 2 received; no exceptions.",
    created_at: "2026-05-01T12:00:00Z",
  },
  {
    id: "10000000-0000-0000-0000-000000000002",
    vendor_id: VENDOR.id,
    reviewed_at: "2026-02-01",
    reviewer: "secops@demo.example",
    outcome: "pass_with_findings",
    notes: "One low finding tracked to remediation.",
    created_at: "2026-02-01T12:00:00Z",
  },
  {
    id: "10000000-0000-0000-0000-000000000001",
    vendor_id: VENDOR.id,
    reviewed_at: "2025-11-01",
    reviewer: "",
    outcome: "waived",
    notes: "",
    created_at: "2025-11-01T12:00:00Z",
  },
];

async function mockDetail(page: Page, vendor: typeof VENDOR): Promise<void> {
  await page.route(
    (url) => url.pathname === "/api/vendors/" + vendor.id,
    (route) =>
      route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ vendor }),
      }),
  );
}

async function mockReviews(page: Page, reviews: unknown[]): Promise<void> {
  await page.route(
    (url) => url.pathname === "/api/vendors/" + VENDOR.id + "/reviews",
    (route) =>
      route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ reviews }),
      }),
  );
}

test.describe("vendor review history (slice 688)", () => {
  test("AC-4/AC-7: history card renders a multi-row timeline newest-first", async ({
    authedPage: page,
  }) => {
    await mockDetail(page, VENDOR);
    await mockReviews(page, REVIEWS);
    await page.goto("/vendors/" + VENDOR.id);

    const card = page.getByTestId("vendor-detail-review-history-card");
    await expect(card).toBeVisible();

    const timeline = page.getByTestId("vendor-detail-review-history-timeline");
    await expect(timeline).toBeVisible();

    const rows = page.getByTestId("vendor-detail-review-row");
    await expect(rows).toHaveCount(3);

    // Newest-first ordering: the first row is the 2026-05-01 pass.
    const dates = page.getByTestId("vendor-detail-review-date");
    await expect(dates.nth(0)).toHaveText("2026-05-01");
    await expect(dates.nth(1)).toHaveText("2026-02-01");
    await expect(dates.nth(2)).toHaveText("2025-11-01");

    // Outcomes render as human-readable labels.
    const outcomes = page.getByTestId("vendor-detail-review-outcome");
    await expect(outcomes.nth(0)).toHaveText("Pass");
    await expect(outcomes.nth(1)).toHaveText("Pass with findings");
    await expect(outcomes.nth(2)).toHaveText("Waived");

    // Reviewer renders when present; the empty-reviewer waived row has none.
    await expect(
      page.getByTestId("vendor-detail-review-reviewer").nth(0),
    ).toHaveText("owner@demo.example");

    // The empty-series scalar fallback is NOT shown when there are rows.
    await expect(
      page.getByTestId("vendor-detail-review-history-scalar"),
    ).toHaveCount(0);
  });

  test("empty series falls back to the honest scalar message", async ({
    authedPage: page,
  }) => {
    await mockDetail(page, VENDOR);
    await mockReviews(page, []);
    await page.goto("/vendors/" + VENDOR.id);

    await expect(
      page.getByTestId("vendor-detail-review-history-timeline"),
    ).toHaveCount(0);
    const scalar = page.getByTestId("vendor-detail-review-history-scalar");
    await expect(scalar).toBeVisible();
    await expect(scalar).toContainText("No per-review history recorded yet");
    await expect(scalar).toContainText("2026-05-01");
  });

  test("AC-5: the Record review affordance routes to the record form", async ({
    authedPage: page,
  }) => {
    await mockDetail(page, VENDOR);
    await mockReviews(page, REVIEWS);
    await page.goto("/vendors/" + VENDOR.id);

    await page.getByTestId("vendor-detail-record-review").click();
    await page.waitForURL("**/vendors/" + VENDOR.id + "/reviews/new");
    await expect(page.getByTestId("vendor-record-review")).toBeVisible();
    await expect(
      page.getByTestId("vendor-record-review-outcome"),
    ).toBeVisible();
  });
});
