// Slice 278 — Playwright E2E for the demo-seed admin page.
//
// Three assertions, all driven by page.route() mocking of the BFF
// endpoints. No live seed runs during e2e — the Go integration
// test covers the real DB-bound path.
//
//   (a) Banner renders when status returns {enabled: false}; both
//       buttons are absent.
//   (b) Buttons render when status returns {enabled: true};
//       confirmation dialog gates the actual POST; success Alert
//       appears with the seed summary counts.
//   (c) Teardown flow mirrors the seed flow; the destructive
//       button + confirmation + post-action Alert all work.

import { expect, test } from "./fixtures";

const SEED_RESULT = {
  tenant_id: "11111111-1111-4111-8111-111111111111",
  tenant_slug: "demo",
  controls: 50,
  risks: 20,
  evidence: 200,
  audit_periods: 3,
  samples: 12,
  idempotent: false,
};

const TEARDOWN_RESULT = {
  tenant_slug: "demo",
  status: "deleted",
};

test.describe("admin demo-seed page", () => {
  test("renders the disabled banner when status reports enabled=false", async ({
    authedPage,
  }) => {
    await authedPage.route("**/api/admin/demo/status", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ enabled: false }),
      });
    });

    await authedPage.goto("/admin/demo");

    await expect(
      authedPage.getByText(
        "Demo tools are not enabled on this deployment",
      ),
    ).toBeVisible();
    // Buttons must NOT render in the disabled branch.
    await expect(authedPage.getByTestId("demo-seed-button")).toHaveCount(0);
    await expect(authedPage.getByTestId("demo-teardown-button")).toHaveCount(0);
  });

  test("renders both buttons + runs reseed through the confirmation dialog", async ({
    authedPage,
  }) => {
    await authedPage.route("**/api/admin/demo/status", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ enabled: true }),
      });
    });
    let seedCalls = 0;
    await authedPage.route("**/api/admin/demo/seed", async (route) => {
      seedCalls++;
      expect(route.request().method()).toBe("POST");
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(SEED_RESULT),
      });
    });

    await authedPage.goto("/admin/demo");

    // Both buttons visible.
    await expect(authedPage.getByTestId("demo-seed-button")).toBeVisible();
    await expect(authedPage.getByTestId("demo-teardown-button")).toBeVisible();

    // Click Reseed → dialog appears → cancel → no POST yet.
    await authedPage.getByTestId("demo-seed-button").click();
    await expect(authedPage.getByTestId("demo-seed-dialog")).toBeVisible();
    expect(seedCalls).toBe(0);

    // Click Confirm in the dialog → POST fires → success Alert
    // surfaces the summary.
    await authedPage.getByTestId("demo-seed-confirm").click();
    await expect(authedPage.getByTestId("demo-success")).toBeVisible();
    await expect(authedPage.getByTestId("demo-success")).toContainText(
      "50 controls",
    );
    await expect(authedPage.getByTestId("demo-success")).toContainText(
      "20 risks",
    );
    expect(seedCalls).toBe(1);
  });

  test("teardown button triggers the destructive confirmation + success Alert", async ({
    authedPage,
  }) => {
    await authedPage.route("**/api/admin/demo/status", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ enabled: true }),
      });
    });
    let teardownCalls = 0;
    await authedPage.route("**/api/admin/demo/teardown", async (route) => {
      teardownCalls++;
      expect(route.request().method()).toBe("POST");
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(TEARDOWN_RESULT),
      });
    });

    await authedPage.goto("/admin/demo");

    // Click Tear down → dialog appears → confirm → success.
    await authedPage.getByTestId("demo-teardown-button").click();
    await expect(authedPage.getByTestId("demo-teardown-dialog")).toBeVisible();
    await authedPage.getByTestId("demo-teardown-confirm").click();
    await expect(authedPage.getByTestId("demo-success")).toBeVisible();
    await expect(authedPage.getByTestId("demo-success")).toContainText(
      "torn down",
    );
    expect(teardownCalls).toBe(1);
  });
});
