// Slice 105 — Playwright E2E for the /risks/new risk-create form.
//
// Runner status (post-slice-069 / 079 audit):
// Playwright IS installed in `web/`. This spec is quarantined behind
// slice 082 (the seed-data harness) per slice 079's decision; when that
// harness lands, the un-commented assertions below become the gate. The
// test bodies are preserved verbatim as a reviewable contract per the
// slice 040 / 042 / 056 / 060 / 064 / 071 / 094 / 098 / 100 precedent.
//
// Run locally (once seed harness lands):
//   cd web
//   npx playwright install chromium      # once per machine
//   npx playwright test e2e/risks-create.spec.ts
//
// Pre-conditions the seed-data harness (slice 082) must establish
// before the commented assertions are turned on:
//   - PLATFORM_BASE_URL points at a running platform instance.
//   - TEST_BEARER carries a credential in a tenant where /risks renders
//     the true-zero empty state (no risks seeded for this tenant). The
//     `Add first risk` CTA on `/risks` must route to `/risks/new`.
//
// AC-8 coverage targets:
//   1. Empty-state CTA on /risks navigates to /risks/new.
//   2. Form submit creates a risk and routes back to /risks.
//   3. Newly created row appears in the list (cache invalidation).
//   4. Server-side validation error (empty title) surfaces inline
//      without losing user input.

import { test } from "@playwright/test";

test.describe("/risks/new risk-create form", () => {
  test.skip(
    !process.env.PLAYWRIGHT_RUN_QUARANTINED,
    "quarantined until slice 082 seed harness lands",
  );

  // test("empty-state CTA on /risks routes to /risks/new", async ({
  //   authedPage,
  // }) => {
  //   await authedPage.goto("/risks");
  //   const cta = authedPage.getByRole("button", { name: /add first risk/i });
  //   await cta.click();
  //   await expect(authedPage).toHaveURL(/\/risks\/new$/);
  // });
  //
  // test("submits a risk and routes back to /risks with the new row", async ({
  //   authedPage,
  // }) => {
  //   await authedPage.goto("/risks/new");
  //   await authedPage.getByTestId("risks-create-title").fill("E2E test risk");
  //   await authedPage
  //     .getByTestId("risks-create-treatment-owner")
  //     .fill("e2e-owner");
  //   // Defaults: category=operational, methodology=nist_800_30,
  //   // treatment=mitigate, likelihood=3, impact=3.
  //   await authedPage.getByTestId("risks-create-submit").click();
  //   await expect(authedPage).toHaveURL(/\/risks(\?|$)/);
  //   await expect(
  //     authedPage.getByTestId("risks-row-title").filter({ hasText: "E2E test risk" }),
  //   ).toBeVisible();
  // });
  //
  // test("upstream 4xx surfaces inline without clearing form input", async ({
  //   authedPage,
  // }) => {
  //   await authedPage.goto("/risks/new");
  //   // Leave title empty to trigger the slice-019 "title is required" 400.
  //   // (HTML required is also set; the test pre-conditions a browser run
  //   // that bypasses native validation to verify backend-error rendering.)
  //   await authedPage
  //     .getByTestId("risks-create-treatment-owner")
  //     .fill("e2e-owner");
  //   await authedPage.getByTestId("risks-create-submit").click();
  //   await expect(authedPage.getByTestId("risks-create-error")).toBeVisible();
  //   // Form state preserved.
  //   await expect(
  //     authedPage.getByTestId("risks-create-treatment-owner"),
  //   ).toHaveValue("e2e-owner");
  // });
});
