// #2089 — the create / update / error conventions.
//
//   - create -> redirect to the new record, plus a toast;
//   - errors -> inline beside the field, never a native browser bubble.
//
// Hermetic mock pattern (slice 594): every BFF call the flow depends on
// is route-mocked, so the assertions do not depend on the shared
// docker-compose DB. Waits are web-first (`expect(...)` auto-wait and
// `waitForResponse`); no sleeps.
//
// Audit periods have no detail page yet, so their create lands on the
// /audits list, which is the record's only home today.
//
// Hard rule (P0-A9): neutral test strings only; no vendor-prefixed
// tokens.

import type { Page, Route } from "@playwright/test";

import { expect, test } from "./fixtures";

const RISK_ID = "00000000-0000-4000-8000-0000000b2089";
const VENDOR_ID = "00000000-0000-4000-8000-0000000c2089";
const PLAN_ID = "00000000-0000-4000-8000-0000000d2089";
const PERIOD_ID = "00000000-0000-4000-8000-0000000e2089";
const OWNER_ID = "00000000-0000-4000-8000-0000000f2089";

async function json(route: Route, status: number, body: unknown) {
  await route.fulfill({
    status,
    contentType: "application/json",
    body: JSON.stringify(body),
  });
}

async function expectToast(page: Page, text: RegExp) {
  const toast = page.getByTestId("toast");
  await expect(toast).toHaveText(text, { timeout: 30_000 });
  // The live region announces politely and the toast is dismissible.
  const viewport = page.getByTestId("toast-viewport");
  await expect(viewport).toHaveAttribute("role", "status");
  await expect(viewport).toHaveAttribute("aria-live", "polite");
  await page.getByTestId("toast-dismiss").click();
  await expect(toast).toHaveCount(0);
}

const RISK = {
  id: RISK_ID,
  title: "E2E toast risk",
  description: "",
  category: "operational",
  methodology: "nist_800_30",
  inherent_score: { likelihood: 3, impact: 3 },
  treatment: "untreated",
  treatment_owner: "e2e-owner",
  residual_score: {},
  accepter: "",
  instrument_reference: "",
  linked_control_ids: [],
  created_at: "2026-11-14T00:00:00Z",
  updated_at: "2026-11-14T00:00:00Z",
  themes: [],
  level: "team",
  severity: 9,
};

test.describe("create lands on the new record with a toast (#2089)", () => {
  test("creating a risk lands on its detail page with the toast", async ({
    authedPage: page,
  }) => {
    await page.route("**/api/risks", (route, req) =>
      req.method() === "POST"
        ? json(route, 201, { risk: RISK })
        : json(route, 200, { risks: [], count: 0 }),
    );
    await page.route(`**/api/risks/${RISK_ID}`, (route) =>
      json(route, 200, { risk: RISK }),
    );
    await page.route(`**/api/risks/${RISK_ID}/action-plans`, (route) =>
      json(route, 200, { action_plans: [] }),
    );
    await page.route(`**/api/risks/${RISK_ID}/parents`, (route) =>
      json(route, 200, { parents: [], count: 0 }),
    );
    await page.route(`**/api/risks/${RISK_ID}/children`, (route) =>
      json(route, 200, { children: [], count: 0 }),
    );

    await page.goto("/risks/new");
    await page.getByTestId("risks-create-title").fill(RISK.title);
    await page.getByTestId("risks-create-treatment-owner").fill("e2e-owner");
    await page.getByTestId("risks-create-submit").click();

    await expect(page).toHaveURL(new RegExp(`/risks/${RISK_ID}$`), {
      timeout: 30_000,
    });
    await expect(page.getByTestId("risk-detail")).toBeVisible({
      timeout: 30_000,
    });
    await expectToast(page, /Risk created/);
  });

  test("Add risk with a missing required field shows an inline error, not a browser bubble", async ({
    authedPage: page,
  }) => {
    let posted = false;
    await page.route("**/api/risks", (route, req) => {
      if (req.method() === "POST") posted = true;
      return json(route, 200, { risks: [], count: 0 });
    });

    await page.goto("/risks/new");
    await page.getByTestId("risks-create-title").fill(RISK.title);
    // Treatment owner is required and left empty.
    await page.getByTestId("risks-create-submit").click();

    const err = page.getByTestId("risks-create-treatment-owner-error");
    await expect(err).toBeVisible({ timeout: 30_000 });
    const owner = page.getByTestId("risks-create-treatment-owner");
    await expect(owner).toHaveAttribute("aria-invalid", "true");
    await expect(owner).toHaveAttribute(
      "aria-describedby",
      "risk-treatment-owner-error",
    );
    await expect(err).toHaveAttribute("id", "risk-treatment-owner-error");
    // The browser's constraint validation never ran: the form opts out,
    // so the submit reached React, which rendered the inline error.
    expect(
      await page
        .getByTestId("risks-create-form")
        .evaluate((f) => (f as HTMLFormElement).noValidate),
    ).toBe(true);
    await expect(page.getByTestId("toast")).toHaveCount(0);
    await expect(page).toHaveURL(/\/risks\/new$/);
    expect(posted).toBe(false);
  });

  test("creating a vendor lands on its detail page with the toast", async ({
    authedPage: page,
  }) => {
    const vendor = {
      id: VENDOR_ID,
      name: "Northwind Ledger",
      domain: "",
      criticality: "medium",
      contract_start: null,
      contract_end: null,
      dpa_signed: false,
      dpa_signed_at: null,
      review_cadence: "annual",
      last_review_date: null,
      overdue: false,
      owner_user: "",
      linked_sow_uri: null,
      notes: "",
      scope_cell_ids: [],
      created_at: "2026-11-14T00:00:00Z",
      updated_at: "2026-11-14T00:00:00Z",
    };
    await page.route("**/api/vendors", (route, req) =>
      req.method() === "POST"
        ? json(route, 201, { vendor })
        : json(route, 200, { vendors: [] }),
    );
    await page.route(`**/api/vendors/${VENDOR_ID}`, (route) =>
      json(route, 200, { vendor }),
    );
    await page.route(`**/api/vendors/${VENDOR_ID}/reviews`, (route) =>
      json(route, 200, { reviews: [] }),
    );

    await page.goto("/vendors/new");
    await page.getByTestId("vendor-name-input").fill(vendor.name);
    await page.getByTestId("vendor-form-submit").click();

    await expect(page).toHaveURL(new RegExp(`/vendors/${VENDOR_ID}$`), {
      timeout: 30_000,
    });
    await expect(page.getByTestId("vendor-detail")).toBeVisible({
      timeout: 30_000,
    });
    await expectToast(page, /Vendor created/);
  });

  test("creating an action plan lands on its detail page with the toast", async ({
    authedPage: page,
  }) => {
    const plan = {
      id: PLAN_ID,
      title: "Rotate break-glass credentials",
      description: "",
      triggering_event: "",
      owner_id: OWNER_ID,
      status: "open",
      created_at: "2026-11-14T00:00:00Z",
      updated_at: "2026-11-14T00:00:00Z",
    };
    await page.route("**/api/action-plans", (route, req) =>
      req.method() === "POST"
        ? json(route, 201, { action_plan: plan })
        : json(route, 200, { action_plans: [], count: 0 }),
    );
    await page.route(`**/api/action-plans/${PLAN_ID}`, (route) =>
      json(route, 200, {
        action_plan: plan,
        linkage: { risks: [], controls: [] },
      }),
    );

    await page.goto("/action-plans/new");
    await page.getByTestId("action-plan-title").fill(plan.title);
    await page.getByTestId("action-plan-owner").fill(OWNER_ID);
    await page.getByTestId("action-plan-submit").click();

    await expect(page).toHaveURL(new RegExp(`/action-plans/${PLAN_ID}$`), {
      timeout: 30_000,
    });
    await expect(page.getByTestId("action-plan-detail")).toBeVisible({
      timeout: 30_000,
    });
    await expectToast(page, /Action plan created/);
  });

  test("creating an audit period lands on the audits list with the toast", async ({
    authedPage: page,
  }) => {
    const name = "Q1 2027 SOC 2 Type II";
    let created = false;
    await page.route("**/api/audits", (route, req) => {
      if (req.method() === "POST") {
        created = true;
        return json(route, 201, { id: PERIOD_ID, name, status: "open" });
      }
      const periods = created
        ? [
            {
              id: PERIOD_ID,
              name,
              framework_version_id: "11111111-1111-1111-1111-111111110002",
              period_start: "2027-01-01T00:00:00Z",
              period_end: "2027-03-31T00:00:00Z",
              status: "open",
              created_by: "e2e",
              created_at: "2026-11-14T00:00:00Z",
              updated_at: "2026-11-14T00:00:00Z",
            },
          ]
        : [];
      return json(route, 200, {
        audit_periods: periods,
        count: periods.length,
      });
    });

    await page.goto("/audits/new");
    await page.getByTestId("audits-create-name").fill(name);
    await page
      .getByTestId("audits-create-framework-version-id")
      .fill("11111111-1111-1111-1111-111111110002");
    await page.getByTestId("audits-create-period-start").fill("2027-01-01");
    await page.getByTestId("audits-create-period-end").fill("2027-03-31");
    await page.getByTestId("audits-create-submit").click();

    await expect(page).toHaveURL(/\/audits(\?|$)/, { timeout: 30_000 });
    await expect(
      page.getByTestId("audits-row-name").filter({ hasText: name }).first(),
    ).toBeVisible({ timeout: 30_000 });
    await expectToast(page, /Audit period created/);
  });
});
