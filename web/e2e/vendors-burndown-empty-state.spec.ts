// Slice 664 — Playwright E2E pinning the Vendors "Review burndown"
// zero-vendor empty state.
//
// The bug: with zero vendors the widget rendered "100% ON-TIME / 0
// vendors" (the platform short-circuits the 0/0 on-time fraction to 1.0,
// so the numerator/denominator both vanish but the rate reads 100%). An
// empty population is not 100% compliant — the rate must render "—".
//
// Hermetic mock pattern (per the feedback_e2e_shared_db_hermetic_mock
// lesson, slice 594): this spec route-mocks both BFF GETs the page issues
// — the vendor LIST (`/api/vendors`) and the BURNDOWN
// (`/api/vendors/burndown`) — so the assertions do not depend on the
// slice-205 demo seed in the shared docker-compose DB. The `authedPage`
// fixture supplies the session cookie that gets past the (authed) layout
// proxy; every DATA response is fulfilled by the mock.

import { expect, test } from "./fixtures";
import type { Page } from "@playwright/test";

// Zero-population burndown: the platform sends on_time_fraction 1.0 for an
// empty population (total/overdue both 0). The frontend must NOT surface
// that as "100%".
const EMPTY_BURNDOWN = {
  as_of: "2026-06-10T00:00:00Z",
  bands: [],
  total: { criticality: "all", total: 0, overdue: 0, on_time_fraction: 1.0 },
};

// A populated burndown for the no-regression assertion (75% on-time).
const POPULATED_BURNDOWN = {
  as_of: "2026-06-10T00:00:00Z",
  bands: [
    { criticality: "high", total: 4, overdue: 1, on_time_fraction: 0.75 },
  ],
  total: { criticality: "all", total: 4, overdue: 1, on_time_fraction: 0.75 },
};

async function mockVendors(
  page: Page,
  burndown: unknown,
  vendors: unknown[],
): Promise<void> {
  await page.route("**/api/vendors/burndown**", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify(burndown),
    }),
  );
  // The vendors-list BFF returns a `{ vendors: [...] }` envelope (see
  // fetchVendors' ListResp). The empty-state assertions only care about
  // the burndown card, but the list query must still resolve cleanly so
  // the page does not render an error alert.
  await page.route(
    (url) =>
      url.pathname === "/api/vendors" ||
      url.pathname.startsWith("/api/vendors?"),
    (route) =>
      route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ vendors }),
      }),
  );
}

test.describe("vendors review-burndown empty state (slice 664)", () => {
  test("AC-1: zero vendors renders the empty rate token, never 100%", async ({
    authedPage: page,
  }) => {
    await mockVendors(page, EMPTY_BURNDOWN, []);
    await page.goto("/vendors");

    const onTime = page.getByTestId("vendor-stat-on-time");
    await expect(onTime).toBeVisible();
    // The headline rate is the empty token — not the misleading 100%.
    await expect(onTime).toHaveText("—");
    await expect(onTime).not.toHaveText("100%");

    // AC-1: the "0 vendors" count stays accurate.
    await expect(page.getByText("0 vendors", { exact: true })).toBeVisible();
  });

  test("AC-2: a populated tenant still renders the computed percent (no regression)", async ({
    authedPage: page,
  }) => {
    await mockVendors(page, POPULATED_BURNDOWN, []);
    await page.goto("/vendors");

    const onTime = page.getByTestId("vendor-stat-on-time");
    await expect(onTime).toBeVisible();
    await expect(onTime).toHaveText("75%");
  });
});
