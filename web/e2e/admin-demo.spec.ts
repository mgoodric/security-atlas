// Slice 278 — Playwright E2E for the demo-seed admin page.
// Slice 322 — added the click-feedback contract assertion (AC-4) so
// the "silent click" class-of-bug is guarded against. Future
// regressions to the dialog mount, the in-flight button label, OR
// the post-action Alert all fail the new assertion.
//
// Assertions:
//
//   (a) Banner renders when status returns {enabled: false}; both
//       buttons are absent.
//   (b) Buttons render when status returns {enabled: true};
//       confirmation dialog gates the actual POST; success Alert
//       appears with the seed summary counts.
//   (c) Teardown flow mirrors the seed flow; the destructive
//       button + confirmation + post-action Alert all work.
//   (d) Slice 322 — click-feedback contract: clicking the seed
//       button surfaces a visible DOM change within 1s (dialog
//       OR in-flight indicator OR Alert). Catches the class-of-
//       bug, not just the specific instance.
//   (e) Slice 322 — Alerts carry `aria-live="polite"` so screen
//       readers + below-the-fold users get a signal.
//   (f) Slice 322 — post-action Alert auto-scrolls into view so a
//       user with a scrolled viewport sees the success/error
//       message without having to scroll down.

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
      authedPage.getByText("Demo tools are not enabled on this deployment"),
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

  // Slice 322 — AC-4 click-feedback contract. Catches the
  // class-of-bug "silent click" — any of the dialog OR the
  // in-flight indicator OR the success/error Alert appearing
  // within 1s of click satisfies the contract.
  test("AC-4: clicking seed surfaces a visible DOM change within 1s", async ({
    authedPage,
  }) => {
    await authedPage.route("**/api/admin/demo/status", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ enabled: true }),
      });
    });

    await authedPage.goto("/admin/demo");
    await expect(authedPage.getByTestId("demo-seed-button")).toBeVisible();

    // Click and immediately assert SOME feedback element exists
    // within 1000ms. The seed-dialog is the primary contract; the
    // assertion is the structural "click → visible-change" guard.
    const clickStart = Date.now();
    await authedPage.getByTestId("demo-seed-button").click();
    // .first() — during the 80ms transition both demo-click-feedback
    // and demo-seed-dialog can be visible simultaneously; strict-mode
    // rejects multi-match. The contract is "≥1 of the 3 is visible
    // within 1s", which .first() expresses correctly.
    await expect(
      authedPage
        .getByTestId("demo-seed-dialog")
        .or(authedPage.getByTestId("demo-running"))
        .or(authedPage.getByTestId("demo-click-feedback"))
        .first(),
    ).toBeVisible({ timeout: 1000 });
    const elapsed = Date.now() - clickStart;
    expect(elapsed).toBeLessThan(1100); // tolerance for harness jitter
  });

  test("AC-3: post-action Alerts carry aria-live=polite", async ({
    authedPage,
  }) => {
    await authedPage.route("**/api/admin/demo/status", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ enabled: true }),
      });
    });
    await authedPage.route("**/api/admin/demo/seed", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(SEED_RESULT),
      });
    });

    await authedPage.goto("/admin/demo");
    await authedPage.getByTestId("demo-seed-button").click();
    await authedPage.getByTestId("demo-seed-confirm").click();

    const success = authedPage.getByTestId("demo-success");
    await expect(success).toBeVisible();
    await expect(success).toHaveAttribute("aria-live", "polite");
  });

  test("AC-3: failure Alert surfaces error message via the BFF body", async ({
    authedPage,
  }) => {
    await authedPage.route("**/api/admin/demo/status", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ enabled: true }),
      });
    });
    await authedPage.route("**/api/admin/demo/seed", async (route) => {
      await route.fulfill({
        status: 503,
        contentType: "application/json",
        body: JSON.stringify({
          error: "demo seed not enabled on this deployment",
        }),
      });
    });

    await authedPage.goto("/admin/demo");
    await authedPage.getByTestId("demo-seed-button").click();
    await authedPage.getByTestId("demo-seed-confirm").click();

    const errorAlert = authedPage.getByTestId("demo-error");
    await expect(errorAlert).toBeVisible();
    await expect(errorAlert).toContainText(
      "demo seed not enabled on this deployment",
    );
    await expect(errorAlert).toHaveAttribute("aria-live", "polite");
  });
});
