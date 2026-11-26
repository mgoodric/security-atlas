// Slice 102 — Playwright E2E for the /audits list view.
//
// Seeded by fixtures/e2e/audits-list.sql: two tenants of the spec's own.
// POPULATED holds one frozen period, one open period ending in ten days
// and one open period ending in 200 days; EMPTY holds no periods. Each
// test signs in to one of them with an admin JWT minted by
// /v1/test/issue-jwt, so counts, the tally and the empty states never
// read rows other specs add to the demo tenant. #2755 restored the
// assertions slice 102 left commented out behind the slice-082
// quarantine.
//
// Run locally:
//   cd web
//   npx playwright install chromium     # once per machine
//   npx playwright test e2e/audits-list.spec.ts
//
// AC-9 coverage targets: list renders, filter narrows results,
// empty-state visible on no-match, row click navigates,
// frozen periods show the lock icon, in-progress urgent periods show
// the amber cue.

import { expect, test } from "./fixtures";
import { seedFromFixture } from "./seed";
import { signInToTenant } from "./tenant-session";

// Mirror the tenants in fixtures/e2e/audits-list.sql.
const POPULATED = "00000000-0000-0000-0000-2755a0000021";
const EMPTY = "00000000-0000-0000-0000-2755a0000022";
const FROZEN_PERIOD = "cccccccc-cccc-cccc-cccc-2755a0000211";

test.describe("/audits list view", () => {
  test.beforeAll(() => {
    seedFromFixture("audits-list");
  });

  test.describe("tenant with periods", () => {
    test.beforeEach(async ({ page }) => {
      await signInToTenant(page, {
        tenantId: POPULATED,
        roles: ["admin"],
        superAdmin: true,
      });
    });

    test("AC-1: /audits renders the period table for any signed-in user", async ({
      page,
    }) => {
      await page.goto("/audits");
      await expect(
        page.getByRole("heading", { name: /Audit periods/ }),
      ).toBeVisible({ timeout: 30_000 });
      await expect(page.getByTestId("list-page")).toBeVisible();
      await expect(page.getByTestId("list-table-wrap")).toBeVisible();
      await expect(page.getByTestId("list-table-row")).toHaveCount(3);
    });

    test("AC-3: horizontal pill filter row narrows the result set (status pill)", async ({
      page,
    }) => {
      await page.goto("/audits");
      const rows = page.getByTestId("list-table-row");
      await expect(rows).toHaveCount(3, { timeout: 30_000 });
      // The filter row is horizontal (P0-A2 of slice 098) — the pill row
      // mounts, NOT a left sidebar.
      await expect(page.getByTestId("list-filter-pills")).toBeVisible();
      await page.getByLabel("Status", { exact: true }).selectOption("frozen");
      await expect(page).toHaveURL(/[?&]status=frozen/);
      await expect(rows).toHaveCount(1);
      await expect(rows.first()).toContainText("E2E audits-list frozen period");
    });

    test("AC-4: empty state surfaces when filters return zero rows", async ({
      page,
    }) => {
      // 1820 is intentionally out-of-range — no periods can match.
      await page.goto("/audits?year=1820");
      await expect(page.getByTestId("list-empty-state")).toBeVisible({
        timeout: 30_000,
      });
      await expect(
        page.getByText("No periods match these filters"),
      ).toBeVisible();
      // The Clear CTA returns the user to a populated table.
      await page.getByTestId("list-empty-state-cta").click();
      await expect(page.getByTestId("list-table-wrap")).toBeVisible();
      await expect(page.getByTestId("list-table-row")).toHaveCount(3);
    });

    test("AC-6: frozen periods render the lock icon with a frozen-meta tooltip", async ({
      page,
    }) => {
      await page.goto("/audits");
      const lock = page.getByTestId("audits-row-lock");
      // Exactly one seeded period is frozen.
      await expect(lock).toHaveCount(1, { timeout: 30_000 });
      await expect(lock).toBeVisible();
      await expect(lock).toHaveAttribute(
        "title",
        "frozen at 2026-04-02 by e2e:audits-list",
      );
    });

    test("AC-6: in-progress periods within 30 days of period_end show the amber cue", async ({
      page,
    }) => {
      await page.goto("/audits");
      const urgent = page.getByTestId("audits-row-urgent-cue");
      // Only the period ending in ten days carries the cue; the one
      // ending in 200 days and the frozen one do not.
      await expect(urgent).toHaveCount(1, { timeout: 30_000 });
      await expect(urgent).toBeVisible();
      await expect(urgent).toHaveAttribute(
        "title",
        /^ends in \d+d — start fieldwork soon$/,
      );
      await expect(
        page
          .getByTestId("list-table-row")
          .filter({ hasText: "E2E audits-list period ending soon" })
          .getByTestId("audits-row-urgent-cue"),
      ).toHaveCount(1);
    });

    test("AC-7: a row opens the period detail page", async ({ page }) => {
      // #2117 restored the original AC-7 behavior that slice 184 had
      // removed while /audits/[id] did not exist. The detail page itself
      // is covered by e2e/audit-period-detail.spec.ts.
      await page.goto("/audits");
      await page
        .getByTestId("list-table-row")
        .filter({ hasText: "E2E audits-list frozen period" })
        .getByTestId("audits-row-name")
        .click({ timeout: 30_000 });
      await expect(page).toHaveURL(new RegExp(`/audits/${FROZEN_PERIOD}$`));
    });

    test("P0-A1: /audits is distinct from /audit/[controlId] — no collision", async ({
      page,
    }) => {
      // The plural index and the singular workspace are independent
      // routes with different content (slice 042 workspace shows the
      // walk-through chrome, slice 102 list shows the period table).
      await page.goto("/audits");
      await expect(
        page.getByRole("heading", { name: /Audit periods/ }),
      ).toBeVisible({ timeout: 30_000 });
      await page.goto("/audit/test-control-01");
      await expect(
        page.getByRole("heading", { name: "Audit workspace" }),
      ).toBeVisible({ timeout: 30_000 });
      await expect(
        page.getByRole("heading", { name: /Audit periods/ }),
      ).toHaveCount(0);
    });

    test("slice 215 / AC-5: status tally appears in the header with mixed-state fixture", async ({
      page,
    }) => {
      await page.goto("/audits");
      const tally = page.getByTestId("audits-status-tally");
      await expect(tally).toBeVisible({ timeout: 30_000 });
      // The tally is aria-labeled for screen readers (AC-3).
      await expect(tally).toHaveAttribute(
        "aria-label",
        "audit period status tally",
      );
      // Segments are "<N> <status label>" joined by " · ", in the
      // canonical order (frozen before open). Zero-count statuses
      // (P0-215-1) do not appear.
      await expect(tally).toHaveText("1 Frozen · 2 Open");
    });

    test("slice 457 / AC-5: the slice-217 disclosure becomes the working OSCAL download affordance", async ({
      page,
    }) => {
      // Slice 217 replaced a permanently-disabled "Export OSCAL bundle"
      // button with a `<span>` disclosing the future state. Slice 457
      // ships the capability, so the honesty property migrates: the
      // disclosure is gone, a toolbar note names the frozen-period home,
      // and every frozen row carries a working download link. The live
      // `download` event is asserted in e2e/oscal-export-e2e.spec.ts.
      await page.goto("/audits");
      const note = page.getByTestId("audits-oscal-export-toolbar");
      await expect(note).toBeVisible({ timeout: 30_000 });
      await expect(note).toContainText(/frozen period/i);
      // 1. The old future-state disclosure span is gone.
      await expect(page.getByTestId("audits-oscal-export-future")).toHaveCount(
        0,
      );
      // 2. The one frozen row carries the download link, pointing at the
      //    BFF download route.
      const dl = page.getByTestId("audits-oscal-export-download");
      await expect(dl).toHaveCount(1);
      await expect(dl).toBeVisible();
      await expect(dl).toHaveAttribute(
        "href",
        `/api/audits/${FROZEN_PERIOD}/oscal-export`,
      );
      await expect(dl).toHaveAttribute(
        "download",
        `oscal-bundle-${FROZEN_PERIOD}-2026-04-02.json`,
      );
      // 3. No disabled <button> with the original label survives.
      await expect(
        page.locator("button[disabled]", { hasText: "Export OSCAL bundle" }),
      ).toHaveCount(0);
    });

    test("P0-A2: frozen periods are NOT editable from the list (no inline mutation)", async ({
      page,
    }) => {
      await page.goto("/audits");
      // No edit buttons, no input fields, no delete affordances in the
      // frozen-period row: the cell tree is read-only.
      const frozenRows = page.getByTestId("list-table-row").filter({
        has: page.getByTestId("audits-row-lock"),
      });
      await expect(frozenRows).toHaveCount(1, { timeout: 30_000 });
      await expect(frozenRows.locator("button")).toHaveCount(0);
      await expect(frozenRows.locator("input")).toHaveCount(0);
      await expect(frozenRows.locator("select")).toHaveCount(0);
      await expect(frozenRows.locator("textarea")).toHaveCount(0);
    });
  });

  test.describe("tenant with no periods", () => {
    test.beforeEach(async ({ page }) => {
      await signInToTenant(page, {
        tenantId: EMPTY,
        roles: ["admin"],
        superAdmin: true,
      });
    });

    test("AC-4 true-zero: empty-state CTA reads 'Create audit period' when the tenant has no periods", async ({
      page,
    }) => {
      await page.goto("/audits");
      await expect(page.getByTestId("list-empty-state")).toBeVisible({
        timeout: 30_000,
      });
      await expect(page.getByText("No audit periods yet")).toBeVisible();
      const cta = page.getByTestId("list-empty-state-cta");
      await expect(cta).toContainText("Create audit period");
      await cta.click();
      await expect(page).toHaveURL(/\/audits\/new$/);
    });

    test("slice 215 / AC-2: tally hides when no periods exist (empty tenant)", async ({
      page,
    }) => {
      // With zero periods the H1 renders solo and the tally is absent —
      // consistent with the existing empty-state pattern.
      await page.goto("/audits");
      await expect(page.getByTestId("list-empty-state")).toBeVisible({
        timeout: 30_000,
      });
      await expect(
        page.getByRole("heading", { name: /Audit periods/ }),
      ).toBeVisible();
      await expect(page.getByTestId("audits-status-tally")).toHaveCount(0);
    });
  });
});
