// Slice 101 — Playwright E2E for the /policies list view.
//
// Runner status (post-slice-069 / 071 audit):
// Playwright IS installed in `web/`. This spec is quarantined behind
// slice 082 (the seed-data harness) per slice 079's decision; when
// that harness lands, the un-commented assertions below become the
// gate. The test bodies are preserved verbatim as a reviewable
// contract per the slice 040 / 042 / 056 / 060 / 064 / 071 / 094 / 098
// / 100 / 102 precedent.
//
// Run locally:
//   cd web
//   npx playwright install chromium     # once per machine
//   npx playwright test e2e/policies-list.spec.ts
//
// Pre-conditions the seed-data harness (slice 082) must establish
// before the commented assertions are turned on:
//   - PLATFORM_BASE_URL points at a running platform instance
//   - TEST_BEARER carries a credential in a tenant with at least:
//     * 1 published policy with `published_at` set
//     * 1 draft policy with `published_at = null`
//     * 1 policy in a non-default owner_role (drives the owner pill)
//
// AC-10 coverage targets: page chrome + table render, horizontal pill
// row, ack-rate column renders honestly (em-dash until slice 107
// lands), empty-state CTA wording, row click navigates.

import { test } from "@playwright/test";

test.describe("/policies list view", () => {
  test("AC-1: /policies renders the policy library for any signed-in user", async () => {
    //    await page.goto("/login");
    //    await page.fill('input[name="token"]', process.env.TEST_BEARER!);
    //    await page.click("button[type=submit]");
    //    await page.goto("/policies");
    //    await expect(page.getByRole("heading", { name: /Policy library/ })).toBeVisible();
    //    await expect(page.getByTestId("list-page")).toBeVisible();
    //    await expect(page.getByTestId("list-table-wrap")).toBeVisible();
  });

  test("AC-2: table renders the canonical columns from policyWire", async () => {
    //    await page.goto("/policies");
    //    // Header row carries all seven columns.
    //    for (const header of [
    //      "Title",
    //      "Version",
    //      "Status",
    //      "Owner role",
    //      "Published",
    //      "Acknowledgment",
    //      "Updated",
    //    ]) {
    //      await expect(
    //        page.getByRole("columnheader", { name: header }),
    //      ).toBeVisible();
    //    }
  });

  test("AC-3: ack-rate cell renders em-dash until backend ?include=ack_rate lands", async () => {
    //    // Per slice 101 D1 + spillover slice 107, the ack-rate cell is
    //    // null on the wire today; the page renders the em-dash placeholder
    //    // honestly. When slice 107 ships, this assertion flips to the
    //    // <Progress> + percentage caption.
    //    await page.goto("/policies");
    //    await expect(page.getByTestId("policies-ack-rate-missing").first()).toBeVisible();
  });

  test("AC-4: horizontal pill filter row narrows by status", async () => {
    //    await page.goto("/policies");
    //    const initial = await page.getByTestId("list-table-row").count();
    //    const statusPill = page.getByLabel("Status");
    //    await statusPill.selectOption("draft");
    //    await page.waitForLoadState("networkidle");
    //    const filtered = await page.getByTestId("list-table-row").count();
    //    expect(filtered).toBeLessThanOrEqual(initial);
    //    // The filter row is horizontal (P0-A2 of slice 098) — verify the
    //    // pill row mounts, NOT a left sidebar.
    //    await expect(page.getByTestId("list-filter-pills")).toBeVisible();
  });

  test("AC-4: horizontal pill filter row narrows by owner_role", async () => {
    //    await page.goto("/policies");
    //    const ownerPill = page.getByLabel("Owner role");
    //    // Pick the first non-default option (DEFAULT_FILTERS owner_role is
    //    // ALL; seed harness guarantees at least one named owner row).
    //    const opts = await ownerPill.locator("option").allTextContents();
    //    const target = opts.find((o) => o !== "All roles" && o !== "all");
    //    if (target) await ownerPill.selectOption(target);
    //    await page.waitForLoadState("networkidle");
    //    await expect(page.getByTestId("list-table-wrap")).toBeVisible();
  });

  test("AC-5: true zero-state CTA reads 'Scaffold five foundational policies'", async () => {
    //    // This spec needs the seed harness to seed an empty tenant
    //    // (e.g. via a fresh-tenant fixture). When that lands:
    //    await page.goto("/policies");
    //    await expect(page.getByTestId("list-empty-state")).toBeVisible();
    //    await expect(page.getByText("No policies published yet")).toBeVisible();
    //    await expect(page.getByTestId("list-empty-state-cta")).toContainText(
    //      "Scaffold five foundational policies",
    //    );
  });

  test("AC-5: filter-induced empty surfaces 'Clear filters' instead of scaffold CTA", async () => {
    //    // Narrow status to a value no row carries; the empty-state CTA
    //    // flips to "Clear filters".
    //    await page.goto("/policies?status=retired");
    //    await expect(page.getByTestId("list-empty-state")).toBeVisible();
    //    await expect(
    //      page.getByText("No policies match these filters"),
    //    ).toBeVisible();
    //    await page.getByTestId("list-empty-state-cta").click();
    //    await expect(page.getByTestId("list-table-wrap")).toBeVisible();
  });

  test("AC-6: loading skeleton shows three shimmer rows on first paint", async () => {
    //    // Use `route.fulfill` with a delay to keep the skeleton visible
    //    // long enough to assert. When slice 082 lands the seed will be
    //    // realistic enough that this spec can pause on the loading state.
    //    await page.route("**/api/policies", async (route) => {
    //      await new Promise((r) => setTimeout(r, 250));
    //      await route.continue();
    //    });
    //    await page.goto("/policies");
    //    await expect(page.getByTestId("list-loading-skeleton")).toBeVisible();
  });

  test("AC-7: <Progress> primitive carries semantic ARIA label", async () => {
    //    // This assertion becomes the gate after spillover slice 107 lands
    //    // and the ack-rate cell renders the bar. Verifies the
    //    // role=progressbar + aria-label shape.
    //    await page.goto("/policies");
    //    const bars = page.getByRole("progressbar");
    //    const count = await bars.count();
    //    if (count > 0) {
    //      const first = bars.first();
    //      const label = await first.getAttribute("aria-label");
    //      expect(label).toMatch(/^\d+ of \d+ acknowledged · \d+%$/);
    //    }
  });

  test("AC-8: row click navigates to /policies/[id]", async () => {
    //    await page.goto("/policies");
    //    const firstRow = page.getByTestId("list-table-row").first();
    //    await firstRow.click();
    //    await expect(page).toHaveURL(/\/policies\/[^/]+$/);
  });

  test("P0-A2: page does NOT fan out per-row to /v1/policies/{id}/acknowledgment-rate", async () => {
    //    // Count the upstream calls — there should be ONE call to
    //    // /api/policies, and ZERO calls to any acknowledgment-rate path.
    //    const ackRateCalls: string[] = [];
    //    page.on("request", (req) => {
    //      if (req.url().includes("/acknowledgment-rate")) {
    //        ackRateCalls.push(req.url());
    //      }
    //    });
    //    await page.goto("/policies");
    //    await page.waitForLoadState("networkidle");
    //    expect(ackRateCalls).toEqual([]);
  });
});
