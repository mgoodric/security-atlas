// Slice 679 — Playwright E2E for the three vendor-surface fixes:
//   - ATLAS-030 (AC-1): vendor list name + domain render with clear
//     separation (separate elements), never as one concatenated string.
//   - ATLAS-031 (AC-2): the edit page exposes a Delete control whose
//     confirm dialog must be acknowledged before the DELETE fires.
//
// Hermetic mock pattern (feedback_e2e_shared_db_hermetic_mock, slice
// 594): every BFF response is route-mocked so the assertions do not
// depend on the slice-205 demo seed in the shared docker-compose DB.

import { expect, test } from "./fixtures";
import type { Page } from "@playwright/test";

const EMPTY_BURNDOWN = {
  as_of: "2026-06-10T00:00:00Z",
  bands: [],
  total: { criticality: "all", total: 1, overdue: 0, on_time_fraction: 1.0 },
};

// One vendor whose name and domain, if concatenated, would read
// "Northwind Supplynorthwind-supply.example" — the ATLAS-030 signature.
const ONE_VENDOR = {
  id: "00000000-0000-0000-0000-0000000000aa",
  name: "Northwind Supply",
  domain: "northwind-supply.example",
  criticality: "medium",
  contract_start: null,
  contract_end: null,
  dpa_signed: false,
  dpa_signed_at: null,
  review_cadence: "annual",
  last_review_date: null,
  overdue: false,
  owner_user: "owner@demo.example",
  linked_sow_uri: null,
  notes: "",
  scope_cell_ids: [],
  created_at: "2026-01-01T00:00:00Z",
  updated_at: "2026-01-01T00:00:00Z",
};

async function mockList(page: Page, vendors: unknown[]): Promise<void> {
  await page.route("**/api/vendors/burndown**", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify(EMPTY_BURNDOWN),
    }),
  );
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

test.describe("vendors UX/data fixes (slice 679)", () => {
  test("AC-1: name and domain render as separate elements", async ({
    authedPage: page,
  }) => {
    await mockList(page, [ONE_VENDOR]);
    await page.goto("/vendors");

    const name = page.getByTestId("vendor-name");
    const domain = page.getByTestId("vendor-domain");
    await expect(name).toBeVisible();
    await expect(domain).toBeVisible();
    // Separation: the two values live in distinct elements with the
    // exact, un-concatenated text. A concatenated render would fail the
    // exact-text match on each.
    await expect(name).toHaveText("Northwind Supply");
    await expect(domain).toHaveText("northwind-supply.example");
  });

  test("AC-2: delete requires confirmation and fires the DELETE only on confirm", async ({
    authedPage: page,
  }) => {
    let deleteCalled = false;

    // Single-vendor GET for the edit page.
    await page.route("**/api/vendors/" + ONE_VENDOR.id, async (route) => {
      const method = route.request().method();
      if (method === "DELETE") {
        deleteCalled = true;
        await route.fulfill({ status: 204, body: "" });
        return;
      }
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ vendor: ONE_VENDOR }),
      });
    });
    // The list the page redirects to after a successful delete.
    await mockList(page, []);

    // Slice 686 split the read path from the edit path: `/vendors/{id}`
    // is now the read-only detail view and the edit form (with its
    // Delete control) moved to `/vendors/{id}/edit`. The ATLAS-031
    // delete assertion follows the control to its new route.
    await page.goto("/vendors/" + ONE_VENDOR.id + "/edit");

    // The Delete control the copy promises exists.
    const trigger = page.getByTestId("vendor-delete-trigger");
    await expect(trigger).toBeVisible();

    // Opening the dialog does NOT fire the DELETE.
    await trigger.click();
    const dialog = page.getByTestId("vendor-delete-dialog");
    await expect(dialog).toBeVisible();
    expect(deleteCalled).toBe(false);

    // Confirm fires the DELETE and navigates back to the list.
    await page.getByTestId("vendor-delete-confirm").click();
    await page.waitForURL("**/vendors");
    expect(deleteCalled).toBe(true);
  });
});
