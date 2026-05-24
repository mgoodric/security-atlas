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

  test("AC-1 (slice 238): ack_status pill renders between owner_role and meta", async () => {
    //    // Slice 238 — the third filter pill ("Ack status") sits between
    //    // the Owner role pill and the right-aligned "Showing N of M"
    //    // meta counter. The mockup at Plans/mockups/policies.html
    //    // lines 154-165 names four options: All / >= 95% / < 95% /
    //    // < 50%.
    //    await page.goto("/policies");
    //    await expect(page.getByTestId("list-filter-pill-ack_status")).toBeVisible();
    //    const ackPill = page.getByLabel("Ack status");
    //    const opts = await ackPill.locator("option").allTextContents();
    //    expect(opts).toContain("All ack rates");
    //    expect(opts.some((o) => o.includes("95%"))).toBe(true);
    //    expect(opts.some((o) => o.includes("50%"))).toBe(true);
  });

  test("AC-2/AC-3 (slice 238): ack_status=ge95 narrows + URL is bookmarkable", async () => {
    //    // Slice 238 — band narrowing is client-side over
    //    // `policiesQ.data?.policies`; the URL serializes the active
    //    // band so `/policies?ack_status=ge95` is shareable.
    //    await page.goto("/policies?ack_status=ge95");
    //    await expect(page.getByTestId("list-filter-pill-ack_status")).toBeVisible();
    //    const ackPill = page.getByLabel("Ack status");
    //    await expect(ackPill).toHaveValue("ge95");
    //    // Every visible row carries an ack-rate cell at >= 95%. Rows
    //    // with `policies-ack-rate-missing` (null cells) MUST NOT appear
    //    // under a non-ALL band (slice 238 AC-2).
    //    await page.waitForLoadState("networkidle");
    //    const missing = await page.getByTestId("policies-ack-rate-missing").count();
    //    expect(missing).toBe(0);
  });

  test("AC-5 (slice 242 update): true zero-state replaces the lying scaffold CTA with a label-honest body disclosure", async () => {
    //    // Slice 242 closed the slice 101 P0-A4 honesty-gap. The
    //    // empty-state previously rendered a primary CTA "Scaffold
    //    // five foundational policies" whose onClick pointed at
    //    // /admin/credentials (an unrelated admin surface — slice 100
    //    // "land somewhere usable" placeholder pattern). Slice 242
    //    // retired that lying CTA: the `cta` prop is dropped in the
    //    // zero-state branch and the disclosure is folded into the
    //    // empty-state body, which names the operator's concrete next
    //    // action (drafting policies via POST /v1/policies on the
    //    // platform API).
    //    //
    //    // This spec needs the seed harness to seed an empty tenant
    //    // (e.g. via a fresh-tenant fixture). When that lands:
    //    await page.goto("/policies");
    //    await expect(page.getByTestId("list-empty-state")).toBeVisible();
    //    await expect(page.getByText("No policies published yet")).toBeVisible();
    //    // The lying CTA must be gone (slice 242 P0-242-4: "does NOT
    //    // redirect the CTA to yet another unrelated admin page").
    //    await expect(
    //      page.getByTestId("list-empty-state-cta"),
    //    ).toHaveCount(0);
    //    // The disclosure body wrapper is visible and carries the
    //    // load-bearing capability phrase + the platform-API signpost.
    //    const disclosure = page.getByTestId("policies-scaffold-future");
    //    await expect(disclosure).toBeVisible();
    //    await expect(disclosure).toContainText(/policy scaffold/i);
    //    await expect(disclosure).toContainText("POST /v1/policies");
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
