// Slice 686 — Playwright E2E for the read-only vendor detail page.
//
//   - AC-1: the detail view renders the vendor summary WITHOUT form
//     inputs (no <input>/<select>/<textarea> for the summary fields).
//   - AC-2: the vendor-list row name links to the read-only detail; the
//     detail view's "Edit" affordance routes to `/vendors/{id}/edit`
//     (the relocated form).
//   - AC-3: the owner renders as a `mailto:` link when it is a valid
//     email.
//
// Hermetic mock pattern (feedback_e2e_shared_db_hermetic_mock, slice
// 594): every BFF response is route-mocked so the assertions do not
// depend on the slice-205 demo seed in the shared docker-compose DB.

import { expect, test } from "./fixtures";
import type { Page } from "@playwright/test";

const EMPTY_BURNDOWN = {
  as_of: "2026-06-11T00:00:00Z",
  bands: [],
  total: { criticality: "all", total: 1, overdue: 0, on_time_fraction: 1.0 },
};

const VENDOR = {
  id: "00000000-0000-0000-0000-0000000000bb",
  name: "Eastwind Analytics",
  domain: "eastwind-analytics.example",
  criticality: "high",
  contract_start: "2026-01-01",
  contract_end: "2026-12-31",
  dpa_signed: true,
  dpa_signed_at: "2026-01-05T00:00:00Z",
  review_cadence: "quarterly",
  last_review_date: "2026-03-15",
  overdue: false,
  owner_user: "owner@demo.example",
  linked_sow_uri: null,
  notes: "Quarterly review on file; SOC 2 Type II received.",
  scope_cell_ids: [],
  created_at: "2026-01-01T00:00:00Z",
  updated_at: "2026-03-15T00:00:00Z",
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

async function mockDetail(page: Page, vendor: typeof VENDOR): Promise<void> {
  await page.route("**/api/vendors/" + vendor.id, (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ vendor }),
    }),
  );
}

test.describe("read-only vendor detail (slice 686)", () => {
  test("AC-1: detail renders the summary with no form inputs", async ({
    authedPage: page,
  }) => {
    await mockDetail(page, VENDOR);
    await page.goto("/vendors/" + VENDOR.id);

    // The read-only shell + the summary card the copy promises.
    await expect(page.getByTestId("vendor-detail")).toBeVisible();
    await expect(page.getByTestId("vendor-detail-name")).toHaveText(
      "Eastwind Analytics",
    );
    await expect(page.getByTestId("vendor-detail-domain")).toHaveText(
      "eastwind-analytics.example",
    );
    await expect(page.getByTestId("vendor-detail-cadence")).toHaveText(
      "quarterly",
    );
    await expect(page.getByTestId("vendor-detail-last-review")).toHaveText(
      "2026-03-15",
    );
    await expect(page.getByTestId("vendor-detail-dpa")).toHaveText(
      "Signed (2026-01-05)",
    );

    // Read-only: the page renders NO editable form controls. The edit
    // form lives behind the Edit affordance, not on this route.
    await expect(page.locator("input")).toHaveCount(0);
    await expect(page.locator("select")).toHaveCount(0);
    await expect(page.locator("textarea")).toHaveCount(0);
  });

  test("AC-3: a valid email owner renders as a mailto link", async ({
    authedPage: page,
  }) => {
    await mockDetail(page, VENDOR);
    await page.goto("/vendors/" + VENDOR.id);

    const mailto = page.getByTestId("vendor-detail-owner-mailto");
    await expect(mailto).toBeVisible();
    await expect(mailto).toHaveText("owner@demo.example");
    await expect(mailto).toHaveAttribute("href", "mailto:owner@demo.example");
  });

  test("AC-3: a non-email owner renders as plain text (no mailto)", async ({
    authedPage: page,
  }) => {
    const roleOwner = { ...VENDOR, owner_user: "Head of Security" };
    await mockDetail(page, roleOwner);
    await page.goto("/vendors/" + roleOwner.id);

    await expect(page.getByTestId("vendor-detail-owner")).toHaveText(
      "Head of Security",
    );
    await expect(page.getByTestId("vendor-detail-owner-mailto")).toHaveCount(0);
  });

  test("AC-2: list name links to read-only detail; Edit affordance routes to the form", async ({
    authedPage: page,
  }) => {
    await mockList(page, [VENDOR]);
    await mockDetail(page, VENDOR);

    // From the list, the name link lands on the read-only detail.
    await page.goto("/vendors");
    await page.getByTestId("vendor-name").click();
    await page.waitForURL("**/vendors/" + VENDOR.id);
    await expect(page.getByTestId("vendor-detail")).toBeVisible();

    // The detail's Edit affordance routes to the relocated edit form.
    await page.getByTestId("vendor-detail-edit").click();
    await page.waitForURL("**/vendors/" + VENDOR.id + "/edit");
    // The edit route renders the form (an editable Name input exists).
    await expect(
      page.getByRole("heading", { name: "Edit vendor" }),
    ).toBeVisible();
    await expect(page.getByTestId("vendor-owner-input")).toBeVisible();
  });
});
