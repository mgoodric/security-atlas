// Slice 073 — Playwright E2E for the first-time login UX (AC-12).
//
// Two assertions, both driven by page.route() mocking of /v1/install-state:
//
//   (a) When the mocked endpoint returns first_install=true, the login
//       page renders the FirstInstallGuidance card with the three-bullet
//       discovery list.
//   (b) When the mocked endpoint returns first_install=false, the
//       guidance card is absent.
//
// We use page.route() rather than requiring a real fresh-install fixture
// because the seed-data harness gap from slice 069's AC-5 PARTIAL still
// applies. The route mock pattern mirrors slice 069 + dashboard.spec.ts
// AC-7 (route.abort / continue with delay).
//
// Hard rule (P0-A9 inherited from slice 069's fixtures): no
// vendor-prefixed tokens in test strings. The mocked install-state body
// is metadata only — no token plaintext ever appears here.

import { expect, test } from "@playwright/test";

test.describe("first-time login UX", () => {
  test("renders the first-install guidance when /v1/install-state reports fresh", async ({
    page,
  }) => {
    await page.route("**/v1/install-state", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ first_install: true }),
      });
    });

    await page.goto("/login");

    const guidance = page.getByTestId("first-install-card");
    await expect(guidance).toBeVisible();
    await expect(guidance).toContainText("First time signing in?");
    await expect(guidance).toContainText("docker-compose:");
    await expect(guidance).toContainText("Helm:");
    await expect(guidance).toContainText("Bare binary:");
    // The grep-line shape is in the discovery commands; assert at least
    // one BOOTSTRAP_TOKEN mention.
    await expect(guidance).toContainText("BOOTSTRAP_TOKEN");
    // The existing token form is preserved (P0-A5).
    await expect(page.getByLabel("Bearer token")).toBeVisible();
  });

  test("hides the first-install guidance when /v1/install-state reports not-fresh", async ({
    page,
  }) => {
    await page.route("**/v1/install-state", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ first_install: false }),
      });
    });

    await page.goto("/login");

    await expect(page.getByTestId("first-install-card")).toHaveCount(0);
    // The existing copy renders unchanged (P0-A5 regression guard).
    await expect(page.getByLabel("Bearer token")).toBeVisible();
  });

  test("falls back to not-fresh when the metadata endpoint 503s", async ({
    page,
  }) => {
    // P0-A5: a metadata failure must NOT block the production sign-in
    // path. The login page renders the existing copy verbatim and the
    // guidance card is absent.
    await page.route("**/v1/install-state", async (route) => {
      await route.fulfill({ status: 503, body: "" });
    });

    await page.goto("/login");

    await expect(page.getByTestId("first-install-card")).toHaveCount(0);
    await expect(page.getByLabel("Bearer token")).toBeVisible();
  });
});
