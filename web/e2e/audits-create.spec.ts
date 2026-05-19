// Slice 149 — Playwright E2E for the `/audits/new` audit-period-create form.
//
// Runner status (post-slice-069 / 079 audit):
// Playwright IS installed in `web/`. This spec is quarantined behind
// slice 082 (the seed-data harness) per slice 079's decision; when that
// harness lands, the un-commented assertions below become the gate. The
// test bodies are preserved verbatim as a reviewable contract per the
// slice 040 / 042 / 056 / 060 / 064 / 071 / 094 / 098 / 100 / 105
// precedent.
//
// Run locally (once seed harness lands):
//   cd web
//   npx playwright install chromium     # once per machine
//   PLAYWRIGHT_RUN_QUARANTINED=1 npx playwright test e2e/audits-create.spec.ts
//
// Pre-conditions the seed-data harness (slice 082) must establish
// before the commented assertions are turned on:
//   - PLATFORM_BASE_URL points at a running platform instance.
//   - TEST_BEARER carries a credential in a tenant with the
//     `grc_engineer` role (the slice-028 Create handler requires
//     IsAdmin or OwnerRoles containing "grc_engineer").
//   - At least one FrameworkVersion UUID is seeded so the form's
//     UUID-paste input has a valid value to round-trip with the
//     backend.
//
// AC coverage targets (slice 149):
//   1. (P0-AUD-1) Empty-state CTA on /audits routes to /audits/new
//      (NOT /admin — the operator-reported bug).
//   2. (P0-AUD-1) Toolbar CTA on /audits routes to /audits/new.
//   3. (AC-3) Form submit creates a period and returns to /audits with
//      the new row appended (cache invalidation).
//   4. (AC-3) Server-side 4xx (empty name) surfaces inline without
//      losing user input.
//
// Test bearer tokens MUST be neutral test strings — NO vendor token
// prefixes (`ghp_*`, `sk_*`, `eyJ*`, `AKIA*`) per the parallel-batch
// protocol §3 banned-prefix list.

import { test } from "@playwright/test";

test.describe("/audits/new audit-period-create form", () => {
  test.skip(
    !process.env.PLAYWRIGHT_RUN_QUARANTINED,
    "quarantined until slice 082 seed harness lands",
  );

  // test("P0-AUD-1: empty-state CTA on /audits routes to /audits/new (not /admin)", async ({
  //   authedPage,
  // }) => {
  //   await authedPage.goto("/audits");
  //   const cta = authedPage.getByRole("button", { name: /create audit period/i });
  //   await cta.click();
  //   await expect(authedPage).toHaveURL(/\/audits\/new$/);
  //   // Critical: did NOT bounce to /admin (the bug the slice fixes).
  //   await expect(authedPage).not.toHaveURL(/\/admin/);
  // });
  //
  // test("P0-AUD-1: toolbar 'New audit period' CTA routes to /audits/new", async ({
  //   authedPage,
  // }) => {
  //   await authedPage.goto("/audits");
  //   await authedPage.getByTestId("audits-create-cta").click();
  //   await expect(authedPage).toHaveURL(/\/audits\/new$/);
  // });
  //
  // test("AC-3: submits a period and routes back to /audits with the new row", async ({
  //   authedPage,
  // }) => {
  //   const frameworkVersionUUID = process.env.TEST_FRAMEWORK_VERSION_UUID!;
  //   await authedPage.goto("/audits/new");
  //   await authedPage.getByTestId("audits-create-name").fill("E2E test period");
  //   await authedPage
  //     .getByTestId("audits-create-framework-version-id")
  //     .fill(frameworkVersionUUID);
  //   await authedPage
  //     .getByTestId("audits-create-period-start")
  //     .fill("2026-07-01");
  //   await authedPage
  //     .getByTestId("audits-create-period-end")
  //     .fill("2026-09-30");
  //   await authedPage.getByTestId("audits-create-submit").click();
  //   await expect(authedPage).toHaveURL(/\/audits(\?|$)/);
  //   await expect(
  //     authedPage.getByTestId("audits-row-name").filter({ hasText: "E2E test period" }),
  //   ).toBeVisible();
  // });
  //
  // test("AC-3: upstream 4xx surfaces inline without clearing form input", async ({
  //   authedPage,
  // }) => {
  //   await authedPage.goto("/audits/new");
  //   // Leave name empty to trigger the slice-028 "name must be non-empty"
  //   // 400. HTML required is also set; the test pre-conditions a browser
  //   // run that bypasses native validation to verify backend-error
  //   // rendering.
  //   await authedPage
  //     .getByTestId("audits-create-framework-version-id")
  //     .fill("00000000-0000-0000-0000-0000000000ff");
  //   await authedPage
  //     .getByTestId("audits-create-period-start")
  //     .fill("2026-07-01");
  //   await authedPage
  //     .getByTestId("audits-create-period-end")
  //     .fill("2026-09-30");
  //   await authedPage.getByTestId("audits-create-submit").click();
  //   await expect(authedPage.getByTestId("audits-create-error")).toBeVisible();
  //   // Form state preserved.
  //   await expect(
  //     authedPage.getByTestId("audits-create-framework-version-id"),
  //   ).toHaveValue("00000000-0000-0000-0000-0000000000ff");
  // });
});
