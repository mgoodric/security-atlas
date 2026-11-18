// #2365 — calendar days never shift across a day boundary west of UTC.
//
// Runs only in the `chromium-denver` project (playwright.config.ts), whose
// browser timezone is America/Denver (UTC-7 / UTC-6). Every other spec runs
// pinned to UTC, where a calendar day formatted in local time and one
// formatted in UTC read the same, so this regression would slip past them.
//
// The backend carries a calendar day either as a bare `YYYY-MM-DD` or as
// midnight UTC. Formatted in the viewer's zone, midnight UTC on 1 Dec is
// still 30 Nov in Denver. `<Timestamp style="date">` formats calendar
// days in UTC, so both shapes must read as the day the backend sent.
//
// HERMETIC: route-mocks the vendor detail BFF (slice 594 convention).

import { expect, test } from "./fixtures";

const VENDOR = {
  id: "00000000-0000-0000-0000-000000002365",
  name: "Test Vendor 2365",
  domain: "test-vendor-2365.example",
  criticality: "high",
  contract_start: "2026-01-01",
  contract_end: "2026-12-31",
  dpa_signed: false,
  dpa_signed_at: null,
  review_cadence: "quarterly",
  // A bare calendar day.
  last_review_date: "2026-03-15",
  // A calendar day carried as midnight UTC: 30 Nov in Denver local time.
  renewal_date: "2026-12-01T00:00:00Z",
  overdue: false,
  owner_user: "owner@test.example",
  linked_sow_uri: null,
  notes: "",
  scope_cell_ids: [],
  created_at: "2026-01-01T00:00:00Z",
  updated_at: "2026-03-15T00:00:00Z",
};

test.describe("calendar days in a UTC-minus timezone (#2365)", () => {
  test("vendor due dates read as the day the backend sent", async ({
    authedPage: page,
  }) => {
    // Guard: the project really runs west of UTC, or this proves nothing.
    const offset = await page.evaluate(() =>
      new Date("2026-12-01T00:00:00Z").getTimezoneOffset(),
    );
    expect(offset).toBeGreaterThan(0);

    await page.route("**/api/vendors/" + VENDOR.id, (route) =>
      route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ vendor: VENDOR }),
      }),
    );
    await page.goto("/vendors/" + VENDOR.id);

    await expect(page.getByTestId("vendor-detail-last-review")).toHaveText(
      "Mar 15, 2026",
      { timeout: 30_000 },
    );
    await expect(page.getByTestId("vendor-detail-renewal-date")).toHaveText(
      "Dec 1, 2026",
    );
    await expect(page.getByTestId("vendor-detail-contract-end")).toHaveText(
      "Dec 31, 2026",
    );
  });
});
