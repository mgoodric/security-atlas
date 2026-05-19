// Slice 151 — Playwright E2E for the /risks/new control-link multi-select.
//
// Runner status: Playwright IS installed in `web/`. This spec is
// quarantined behind slice 082 (seed-data harness) per the
// established precedent for slice 094 / 098 / 100 / 105 / 147 / 149 / 157.
// When that harness lands, the un-commented assertions below become
// the gate. Test bodies are preserved verbatim as a reviewable contract.
//
// Run locally (once seed harness lands):
//   cd web
//   npx playwright install chromium      # once per machine
//   PLAYWRIGHT_RUN_QUARANTINED=1 npx playwright test e2e/risks-create-control-link.spec.ts
//
// Pre-conditions the seed-data harness (slice 082) must establish
// before the commented assertions are turned on:
//   - PLATFORM_BASE_URL points at a running platform instance.
//   - TEST_BEARER carries a credential in a tenant that already has
//     >=2 active controls (slice 065 SOC 2 starter kit suffices).
//   - At least one control's title is searchable by the substring
//     "access" so the search filter assertion is deterministic.
//
// AC coverage:
//   AC-3  Multi-select renders ONLY when treatment === 'mitigate'.
//   AC-4  Client-side validation blocks submit with mitigate + 0 selected.
//   AC-5  Form posts linked_control_ids when mitigate + selection exists.
//   AC-6  Newly created risk appears in the risk list with linked control.

import { test } from "@playwright/test";

test.describe("/risks/new control-link multi-select", () => {
  test.skip(
    !process.env.PLAYWRIGHT_RUN_QUARANTINED,
    "quarantined until slice 082 seed harness lands",
  );

  // test("multi-select renders only when treatment is mitigate (AC-3)", async ({
  //   authedPage,
  // }) => {
  //   await authedPage.goto("/risks/new");
  //   // Default treatment is mitigate per initialState(); picker visible.
  //   await expect(
  //     authedPage.getByTestId("risks-create-control-multi-select"),
  //   ).toBeVisible();
  //
  //   // Switch to avoid; picker should disappear.
  //   await authedPage
  //     .getByTestId("risks-create-treatment")
  //     .selectOption("avoid");
  //   await expect(
  //     authedPage.getByTestId("risks-create-control-multi-select"),
  //   ).toHaveCount(0);
  //
  //   // Switch back to mitigate; picker returns.
  //   await authedPage
  //     .getByTestId("risks-create-treatment")
  //     .selectOption("mitigate");
  //   await expect(
  //     authedPage.getByTestId("risks-create-control-multi-select"),
  //   ).toBeVisible();
  // });
  //
  // test("client-side validation blocks submit with mitigate + 0 links (AC-4)", async ({
  //   authedPage,
  // }) => {
  //   await authedPage.goto("/risks/new");
  //   await authedPage.getByTestId("risks-create-title").fill("E2E ctrl-link risk");
  //   await authedPage
  //     .getByTestId("risks-create-treatment-owner")
  //     .fill("e2e-owner");
  //   // Treatment defaults to mitigate; no controls selected.
  //   await authedPage.getByTestId("risks-create-submit").click();
  //
  //   // Stays on /risks/new and shows the required-error inline.
  //   await expect(authedPage).toHaveURL(/\/risks\/new$/);
  //   await expect(
  //     authedPage.getByTestId("risks-create-control-multi-select-required-error"),
  //   ).toBeVisible();
  // });
  //
  // test("submits with linked_control_ids when at least one control selected (AC-5+AC-6)", async ({
  //   authedPage,
  // }) => {
  //   await authedPage.goto("/risks/new");
  //   await authedPage
  //     .getByTestId("risks-create-title")
  //     .fill("E2E ctrl-link mitigate risk");
  //   await authedPage
  //     .getByTestId("risks-create-treatment-owner")
  //     .fill("e2e-owner");
  //
  //   // Filter and pick the first matching control via the search box.
  //   await authedPage
  //     .getByTestId("risks-create-control-multi-select-filter")
  //     .fill("access");
  //   const firstOption = authedPage
  //     .getByTestId(/risks-create-control-multi-select-checkbox-/)
  //     .first();
  //   await firstOption.check();
  //
  //   await authedPage.getByTestId("risks-create-submit").click();
  //   await expect(authedPage).toHaveURL(/\/risks(\?|$)/);
  //   await expect(
  //     authedPage
  //       .getByTestId("risks-row-title")
  //       .filter({ hasText: "E2E ctrl-link mitigate risk" }),
  //   ).toBeVisible();
  // });
  //
  // test("clear-selection button empties the picker (AC-1 behavior)", async ({
  //   authedPage,
  // }) => {
  //   await authedPage.goto("/risks/new");
  //   const firstOption = authedPage
  //     .getByTestId(/risks-create-control-multi-select-checkbox-/)
  //     .first();
  //   await firstOption.check();
  //   await expect(
  //     authedPage.getByTestId("risks-create-control-multi-select-summary"),
  //   ).toContainText("1 selected");
  //   await authedPage
  //     .getByTestId("risks-create-control-multi-select-clear")
  //     .click();
  //   await expect(
  //     authedPage.getByTestId("risks-create-control-multi-select-summary"),
  //   ).toContainText("0 selected");
  // });
});
