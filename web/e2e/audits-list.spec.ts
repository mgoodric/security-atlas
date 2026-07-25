// Slice 102 — Playwright E2E for the /audits list view.
//
// Runner status (post-slice-069 / 071 audit):
// Playwright IS installed in `web/`. This spec is quarantined behind
// slice 082 (the seed-data harness) per slice 079's decision; when
// that harness lands, the un-commented assertions below become the
// gate. The test bodies are preserved verbatim as a reviewable
// contract per the slice 040 / 042 / 056 / 060 / 064 / 071 / 094 / 098
// precedent.
//
// Run locally:
//   cd web
//   npx playwright install chromium     # once per machine
//   npx playwright test e2e/audits-list.spec.ts
//
// Pre-conditions the seed-data harness (slice 082) must establish
// before the commented assertions are turned on:
//   - PLATFORM_BASE_URL points at a running platform instance
//   - TEST_BEARER carries a credential in a tenant with at least:
//     * 1 frozen audit period (drives AC-6 lock-icon check)
//     * 1 non-frozen audit period whose period_end is within 30 days
//       of now (drives the amber-cue check)
//     * 1 non-frozen audit period whose period_end is well beyond 30
//       days (drives the no-cue check)
//
// AC-9 coverage targets: list renders, filter narrows results,
// empty-state visible on no-match, row click navigates,
// frozen periods show the lock icon, in-progress urgent periods show
// the amber cue.

import { test } from "@playwright/test";

test.describe("/audits list view", () => {
  test("AC-1: /audits renders the period table for any signed-in user", async () => {
    //    await page.goto("/login");
    //    await page.fill('input[name="token"]', process.env.TEST_BEARER!);
    //    await page.click("button[type=submit]");
    //    await page.goto("/audits");
    //    await expect(page.getByRole("heading", { name: /Audit periods/ })).toBeVisible();
    //    await expect(page.getByTestId("list-page")).toBeVisible();
    //    await expect(page.getByTestId("list-table-wrap")).toBeVisible();
  });

  test("AC-3: horizontal pill filter row narrows the result set (status pill)", async () => {
    //    await page.goto("/audits");
    //    const initial = await page.getByTestId("list-table-row").count();
    //    const statusPill = page.getByLabel("Status");
    //    await statusPill.selectOption("frozen");
    //    await page.waitForLoadState("networkidle");
    //    const filtered = await page.getByTestId("list-table-row").count();
    //    expect(filtered).toBeLessThanOrEqual(initial);
    //    // The filter row is horizontal (P0-A2 of slice 098) — verify the
    //    // pill row mounts, NOT a left sidebar.
    //    await expect(page.getByTestId("list-filter-pills")).toBeVisible();
  });

  test("AC-4: empty state surfaces when filters return zero rows", async () => {
    //    // 1820 is intentionally out-of-range — no periods can match.
    //    await page.goto("/audits?year=1820");
    //    await expect(page.getByTestId("list-empty-state")).toBeVisible();
    //    await expect(
    //      page.getByText("No periods match these filters"),
    //    ).toBeVisible();
    //    // The Clear CTA returns the user to a populated table.
    //    await page.getByTestId("list-empty-state-cta").click();
    //    await expect(page.getByTestId("list-table-wrap")).toBeVisible();
  });

  test("AC-4 true-zero: empty-state CTA reads 'Create audit period' when the tenant has no periods", async () => {
    //    // This spec needs the seed harness to optionally seed an empty
    //    // tenant (e.g. via a fresh-tenant fixture). When that lands:
    //    await page.goto("/audits");
    //    await expect(page.getByTestId("list-empty-state")).toBeVisible();
    //    await expect(page.getByText("No audit periods yet")).toBeVisible();
    //    await expect(page.getByTestId("list-empty-state-cta")).toContainText(
    //      "Create audit period",
    //    );
  });

  test("AC-6: frozen periods render the lock icon with a frozen-meta tooltip", async () => {
    //    await page.goto("/audits");
    //    const lock = page.getByTestId("audits-row-lock").first();
    //    await expect(lock).toBeVisible();
    //    const title = await lock.getAttribute("title");
    //    expect(title).toMatch(/^frozen( at \d{4}-\d{2}-\d{2})?( by .+)?$/);
  });

  test("AC-6: in-progress periods within 30 days of period_end show the amber cue", async () => {
    //    await page.goto("/audits");
    //    const urgent = page.getByTestId("audits-row-urgent-cue");
    //    await expect(urgent.first()).toBeVisible();
    //    // Cue title carries the days-until-end label.
    //    const title = await urgent.first().getAttribute("title");
    //    expect(title).toMatch(/ends in \d+d — start fieldwork soon/);
  });

  test("AC-7 (slice 184): rows are NOT clickable — detail page is a future slice", async () => {
    //    // Slice 184 reversed the original AC-7 behavior (row click →
    //    // /audits/[id]) because the destination did not exist and 404'd
    //    // on every click (slice-178 first-pass F-178-4 HONESTY-GAP).
    //    // Until the per-period detail page lands, rows are read-only.
    //    await page.goto("/audits");
    //    const firstRow = page.getByTestId("list-table-row").first();
    //    // The list-table component drops `cursor-pointer` when
    //    // onRowClick is undefined — we assert the absence of the class
    //    // as a cheap proxy for the row no longer signaling "clickable".
    //    await expect(firstRow).not.toHaveClass(/cursor-pointer/);
    //    // Clicking must NOT navigate away from /audits.
    //    await firstRow.click();
    //    await expect(page).toHaveURL(/\/audits(\?.*)?$/);
    //    // The honesty banner must be visible.
    //    await expect(
    //      page.getByTestId("audits-detail-coming-soon-banner"),
    //    ).toBeVisible();
  });

  test("P0-A1: /audits is distinct from /audit/[controlId] — no collision", async () => {
    //    // Asserts that the plural index and the singular workspace are
    //    // independent routes. Both paths should resolve to different
    //    // pages with different content (slice 042 workspace shows the
    //    // walk-through chrome, slice 102 list shows the period table).
    //    await page.goto("/audits");
    //    await expect(page.getByRole("heading", { name: /Audit periods/ })).toBeVisible();
    //    await page.goto("/audit/test-control-01");
    //    // The singular workspace will show its own heading (not "Audit
    //    // periods"); the exact text comes from slice 042.
    //    await expect(page.getByRole("heading", { name: /Audit periods/ })).toHaveCount(0);
  });

  test("slice 215 / AC-5: status tally appears in the header with mixed-state fixture", async () => {
    // Seed harness (slice 082) pre-conditions assume the tenant has at
    // least three distinct statuses — see the `audit_periods` rows of
    // the seed fixture. With those rows the tally must render and the
    // text must read as a `· `-separated count list.
    //    await page.goto("/audits");
    //    const tally = page.getByTestId("audits-status-tally");
    //    await expect(tally).toBeVisible();
    //    // The tally is aria-labeled for screen readers (AC-3).
    //    await expect(tally).toHaveAttribute(
    //      "aria-label",
    //      "audit period status tally",
    //    );
    //    // Format: each segment is "<N> <status>", segments joined by " · ".
    //    const text = (await tally.textContent())?.trim() ?? "";
    //    expect(text).toMatch(
    //      /^\d+ [a-z_]+( · \d+ [a-z_]+)*$/,
    //    );
    //    // Zero-count statuses (P0-215-1) must NOT appear.
    //    expect(text).not.toMatch(/\b0 /);
  });

  test("slice 215 / AC-2: tally hides when no periods exist (empty tenant)", async () => {
    // Empty-tenant fixture (seed harness must support a "no audit
    // periods" credential). With zero periods, the H1 renders solo and
    // the tally is absent — consistent with the existing empty-state
    // pattern.
    //    await page.goto("/audits");
    //    await expect(page.getByTestId("audits-status-tally")).toHaveCount(0);
    //    // The H1 itself still renders.
    //    await expect(
    //      page.getByRole("heading", { name: /Audit periods/ }),
    //    ).toBeVisible();
  });

  test("slice 457 / AC-5: the slice-217 disclosure becomes the working OSCAL download affordance", async () => {
    // Slice 217 closed the F-178-217 HONESTY-GAP by replacing a
    // permanently-disabled "Export OSCAL bundle" button with a non-button
    // `<span>` disclosing the FUTURE state ("ships with the per-period
    // detail view"). Slice 457 ships the capability: the honesty property
    // MIGRATES — the disclosure is replaced by the working action it
    // signposted (AC-5: the assertion is updated, NOT silently deleted).
    //
    // The migrated contract has three halves:
    //
    //   1. The old future-state disclosure testid
    //      (`audits-oscal-export-future`) is GONE — the coming-soon span
    //      no longer exists.
    //   2. A toolbar note (`audits-oscal-export-toolbar`) describes the
    //      now-live capability and names the frozen-period home.
    //   3. Every FROZEN period row carries a working "Export OSCAL bundle"
    //      download link (`audits-oscal-export-download`) whose href is
    //      the BFF download route.
    //
    // Quarantined behind the slice 082 seed harness like the rest of this
    // file. Bodies left commented so the contract is reviewable; when the
    // harness lands the assertions turn on. The live `download` event +
    // filename + content-type are asserted in
    // `web/e2e/oscal-export-e2e.spec.ts` (AC-3).
    //    await page.goto("/audits");
    //    // 1. The old future-state disclosure span is gone.
    //    await expect(
    //      page.getByTestId("audits-oscal-export-future"),
    //    ).toHaveCount(0);
    //    // 2. The toolbar note describes the now-working capability.
    //    const note = page.getByTestId("audits-oscal-export-toolbar");
    //    await expect(note).toBeVisible();
    //    expect((await note.textContent())?.toLowerCase() ?? "").toContain(
    //      "frozen period",
    //    );
    //    // 3. At least one frozen row carries the working download link,
    //    //    pointing at the BFF download route.
    //    const dl = page.getByTestId("audits-oscal-export-download").first();
    //    await expect(dl).toBeVisible();
    //    const href = await dl.getAttribute("href");
    //    expect(href).toMatch(/^\/api\/audits\/.+\/oscal-export$/);
    //    expect(await dl.getAttribute("download")).not.toBeNull();
    //    // No disabled <button> with the original label survives.
    //    await expect(
    //      page.locator("button[disabled]", { hasText: "Export OSCAL bundle" }),
    //    ).toHaveCount(0);
  });

  test("P0-A2: frozen periods are NOT editable from the list (no inline mutation)", async () => {
    //    await page.goto("/audits");
    //    // No edit buttons, no input fields, no delete affordances in
    //    // any frozen-period row. Walks every row and asserts the cell
    //    // tree is read-only.
    //    const frozenRows = page.getByTestId("list-table-row").filter({
    //      has: page.getByTestId("audits-row-lock"),
    //    });
    //    const count = await frozenRows.count();
    //    for (let i = 0; i < count; i++) {
    //      const row = frozenRows.nth(i);
    //      await expect(row.locator("button")).toHaveCount(0);
    //      await expect(row.locator("input")).toHaveCount(0);
    //    }
  });
});
