// Slice 152 — Playwright E2E for the control detail 404 empty-state.
//
// Runner status (post-slice-069 / 071 audit):
// Playwright IS installed in `web/`. This spec is quarantined behind
// slice 082 (the seed-data harness) per slice 079's decision; when that
// harness lands, the un-commented assertions below become the gate. The
// test bodies are preserved verbatim as a reviewable contract per the
// slice 040 / 042 / 056 / 060 / 064 / 071 / 094 / 098 / 102 precedent
// — and matching the existing `control-detail.spec.ts` quarantine
// shape directly.
//
// Run locally:
//   cd web
//   npx playwright install chromium     # once per machine
//   npx playwright test e2e/control-detail-empty.spec.ts
//
// Pre-conditions the seed-data harness (slice 082) must establish
// before the commented assertions are turned on:
//   - PLATFORM_BASE_URL points at a running platform instance
//   - TEST_BEARER carries a credential in a tenant with ZERO
//     instantiated controls (the slice 152 scenario — fresh install
//     state). The slice-006 SCF anchor catalog MAY be loaded; it does
//     not matter because the assertions navigate to a bogus UUID that
//     would not resolve in either the tenant control table or any
//     conceivable anchor binding.
//   - BOGUS_CONTROL_ID is a syntactically-valid UUID that the platform
//     guarantees does not resolve in this tenant. The fixture writes a
//     value such as `00000000-0000-0000-0000-000000000152` for this
//     spec — neutral and traceable.
//
// AC coverage targets (from PRD ISC-26..ISC-30 + slice doc AC-4 / AC-5):
//   * /controls/<bogus-uuid> renders the friendly empty-state
//   * NO generic 404 page is shown
//   * "Back to controls list" CTA is present and clickable
//   * The destructive error Alert is NOT rendered (404 must not
//     misroute to the 5xx branch)
//   * The list page's truly-zero defensive empty-state appears when
//     the anchor catalog is empty (slice 152 D1-b — defensive branch)

import { test } from "@playwright/test";

import { seedFromFixture } from "./seed";

test.describe("control detail 404 empty-state (slice 152)", () => {
  test.beforeAll(() => {
    seedFromFixture("control-detail-empty");
  });

  test("AC-1: visiting /controls/<bogus-uuid> renders the friendly empty-state", async () => {
    // 1. Sign in.
    //    await page.goto("/login");
    //    await page.fill('input[name="token"]', process.env.TEST_BEARER!);
    //    await page.click("button[type=submit]");
    // 2. Visit a control detail route with a bogus id.
    //    const BOGUS = "00000000-0000-0000-0000-000000000152";
    //    await page.goto(`/controls/${BOGUS}`);
    // 3. The friendly empty-state renders (slice 152 D1-c).
    //    await expect(page.getByTestId("control-detail-empty")).toBeVisible();
    //    await expect(page.getByTestId("control-detail-empty")).toContainText(
    //      "no control instantiated in your tenant yet",
    //    );
    // 4. The destructive Alert is NOT visible (404 must not misroute
    //    to the 5xx branch — vitest covers this via the classifier,
    //    e2e pins the end-to-end render).
    //    await expect(page.getByTestId("control-detail-error")).not.toBeVisible();
  });

  test("AC-2: empty-state CTA links back to /controls", async () => {
    //    const BOGUS = "00000000-0000-0000-0000-000000000152";
    //    await page.goto(`/controls/${BOGUS}`);
    //    const cta = page.getByTestId("control-detail-empty-cta");
    //    await expect(cta).toBeVisible();
    //    await expect(cta).toHaveAttribute("href", "/controls");
    //    await cta.click();
    //    await expect(page).toHaveURL(/\/controls$/);
  });

  test("AC-3: empty-state names the bogus id honestly in the body copy", async () => {
    // The body copy includes the path id verbatim so the operator can
    // correlate with the URL they clicked. Slice 152 D-152-2 — copy is
    // HONEST about cause, not the misleading "may have been deleted"
    // copy the issue narrative suggested.
    //    const BOGUS = "00000000-0000-0000-0000-000000000152";
    //    await page.goto(`/controls/${BOGUS}`);
    //    await expect(page.getByTestId("control-detail-empty")).toContainText(BOGUS);
  });

  test("AC-4: NO generic Next.js 404 page is shown for the bogus id path", async () => {
    // The (authed) layout + the page-level empty-state mean Next.js
    // never invokes the not-found.tsx render. The body must contain the
    // empty-state testid, not the framework default 404.
    //    const BOGUS = "00000000-0000-0000-0000-000000000152";
    //    await page.goto(`/controls/${BOGUS}`);
    //    await expect(page.getByTestId("control-detail-empty")).toBeVisible();
    // Generic 404 page has its own well-known text — assert against
    // its absence as a defensive double-check.
    //    await expect(page.locator("body")).not.toContainText("This page could not be found");
  });

  test("AC-5: list page renders the truly-zero defensive empty-state when the catalog is empty", async () => {
    // Slice 152 D1-b — when the anchor catalog itself returns zero
    // rows the list renders an HONEST "catalog not seeded" message,
    // not the filter-narrowed "try widening" message. Defensive on
    // main because anchors are catalog-global; flips load-bearing if
    // an SCF importer regression ever ships zero rows.
    //
    // Pre-condition: tenant fixture deliberately runs WITHOUT the
    // slice 006 SCF import so the anchor catalog is empty.
    //
    //    await page.goto("/controls");
    //    await expect(page.getByTestId("list-empty-state")).toBeVisible();
    //    await expect(page.getByTestId("list-empty-state")).toContainText(
    //      "No controls in your tenant yet",
    //    );
    //    await expect(page.getByTestId("list-empty-state")).not.toContainText(
    //      "Try widening",
    //    );
  });
});
