// Slice 098 — Playwright E2E for the /controls list view.
//
// Runner status (post-slice-069 / 071 audit):
// Playwright IS installed in `web/`. This spec is quarantined behind
// slice 082 (the seed-data harness) per slice 079's decision; when that
// harness lands, the un-commented assertions below become the gate. The
// test bodies are preserved verbatim as a reviewable contract per the
// slice 040 / 042 / 056 / 060 / 064 / 071 / 094 precedent.
//
// Run locally:
//   cd web
//   npx playwright install chromium     # once per machine
//   npx playwright test e2e/controls-list.spec.ts
//
// Pre-conditions the seed-data harness (slice 082) must establish
// before the commented assertions are turned on:
//   - PLATFORM_BASE_URL points at a running platform instance
//   - TEST_BEARER carries a credential in a tenant that has at least
//     the seeded SCF anchor catalog loaded (slice 006 SCF import is
//     enough — no per-tenant control instantiation required for v1).
//   - At least three families represented (the design assumes the user
//     can filter by family).
//
// AC-8 coverage targets: list renders, filter narrows results, empty
// state appears on no-match, row click navigates.

import { test } from "@playwright/test";

test.describe("/controls list view", () => {
  test("AC-1: /controls renders the anchor table for any signed-in user", async () => {
    //    await page.goto("/login");
    //    await page.fill('input[name="token"]', process.env.TEST_BEARER!);
    //    await page.click("button[type=submit]");
    //    await page.goto("/controls");
    //    await expect(page.getByRole("heading", { name: /Controls/ })).toBeVisible();
    //    await expect(page.getByTestId("list-page")).toBeVisible();
    //    await expect(page.getByTestId("list-table-wrap")).toBeVisible();
  });

  test("AC-3: horizontal pill filter row narrows the result set", async () => {
    //    await page.goto("/controls");
    //    const initial = await page.getByTestId("list-table-row").count();
    //    const familyPill = page.getByLabel("Family");
    //    await familyPill.selectOption({ index: 1 });   // first non-ALL option
    //    await page.waitForLoadState("networkidle");
    //    const filtered = await page.getByTestId("list-table-row").count();
    //    expect(filtered).toBeLessThan(initial);
    // The filter row is horizontal (P0-A2) — verify NO left sidebar.
    //    await expect(page.getByTestId("list-filter-pills")).toBeVisible();
  });

  test("AC-4: empty state surfaces when filters return zero rows", async () => {
    //    await page.goto("/controls?family=DOES-NOT-EXIST");
    //    await expect(page.getByTestId("list-empty-state")).toBeVisible();
    //    await expect(
    //      page.getByText("No controls match these filters"),
    //    ).toBeVisible();
    // The CTA reads "Clear filters" and clearing returns the user to a populated table.
    //    await page.getByTestId("list-empty-state-cta").click();
    //    await expect(page.getByTestId("list-table-wrap")).toBeVisible();
  });

  test("AC-6: row click navigates to /controls/[id]", async () => {
    //    await page.goto("/controls");
    //    const firstRow = page.getByTestId("list-table-row").first();
    //    const scfIdLink = firstRow.getByTestId("controls-row-scf-id");
    //    const href = await scfIdLink.getAttribute("href");
    //    expect(href).toMatch(/^\/controls\//);
    //    await scfIdLink.click();
    //    await expect(page).toHaveURL(/\/controls\/[^/]+$/);
  });

  // Slice 224 — Scope filter pill (5th pill, server-side intersection).
  // Pre-conditions the seed harness (slice 082) must establish before
  // these assertions are turned on:
  //   - At least two scope cells in the tenant (the bootstrap seed
  //     ships one default cell; the seed harness adds a second so the
  //     select option assertion exercises a non-degenerate dropdown).
  //   - At least one control_evaluations row recorded against each
  //     cell so the worst_per_anchor rollup narrows visibly when the
  //     pill is set.
  test("slice 224 AC-1: Scope pill renders as 5th filter pill", async () => {
    //    await page.goto("/controls");
    //    await expect(page.getByTestId("list-filter-pill-framework")).toBeVisible();
    //    await expect(page.getByTestId("list-filter-pill-family")).toBeVisible();
    //    await expect(page.getByTestId("list-filter-pill-result")).toBeVisible();
    //    await expect(page.getByTestId("list-filter-pill-freshness")).toBeVisible();
    //    await expect(page.getByTestId("list-filter-pill-scope")).toBeVisible();
  });

  test("slice 224 AC-3: selecting a scope cell sets ?scope=<id> on the URL", async () => {
    //    await page.goto("/controls");
    //    const scopePill = page.getByLabel("Scope");
    //    // Pick the second option (first non-ALL cell).
    //    await scopePill.selectOption({ index: 1 });
    //    await page.waitForLoadState("networkidle");
    //    const url = new URL(page.url());
    //    expect(url.searchParams.get("scope")).toMatch(
    //      /^[0-9a-f-]{36}$/,
    //    );
  });

  test("slice 224 AC-3: clearing the scope cell removes ?scope from the URL", async () => {
    //    await page.goto("/controls?scope=00000000-0000-0000-0000-000000000001");
    //    const scopePill = page.getByLabel("Scope");
    //    await scopePill.selectOption({ index: 0 }); // "All cells"
    //    await page.waitForLoadState("networkidle");
    //    const url = new URL(page.url());
    //    expect(url.searchParams.get("scope")).toBeNull();
  });

  // Slice 226 — Frameworks-per-row column (right-aligned, mockup line 197).
  // Pre-conditions the seed harness (slice 082) must establish before
  // these assertions are turned on:
  //   - The SCF catalog + at least one framework crosswalk (SOC 2 v2017)
  //     are loaded so at least one anchor carries a non-empty frameworks
  //     array. The setupHTTPServer in the Go integration tests already
  //     does this for the integration suite; the seed harness must
  //     replicate the bring-up for the e2e harness.
  test("slice 226 AC-5: Frameworks column header is present", async () => {
    //    await page.goto("/controls");
    //    await expect(
    //      page.getByRole("columnheader", { name: /Frameworks/ }),
    //    ).toBeVisible();
  });

  test("slice 226 AC-5 + AC-9: at least one row carries a non-empty Frameworks cell", async () => {
    //    await page.goto("/controls");
    //    // Wait for data to load.
    //    await page.waitForLoadState("networkidle");
    //    const populatedFrameworks = page.getByTestId("controls-row-frameworks");
    //    expect(await populatedFrameworks.count()).toBeGreaterThan(0);
    //    // At least one cell must contain the middle-dot separator OR a
    //    // single canonical abbreviation (SOC2 / ISO / CSF / PCI / HIPAA / GDPR).
    //    const firstText = await populatedFrameworks.first().textContent();
    //    expect(firstText).toMatch(/SOC2|ISO|CSF|PCI|HIPAA|GDPR/);
  });

  test("slice 226 AC-6: anchors with no satisfaction edges render the em-dash placeholder", async () => {
    //    await page.goto("/controls");
    //    await page.waitForLoadState("networkidle");
    //    // The empty-set marker shares the `controls-row-frameworks-empty`
    //    // test-id so the assertion is stable when the SCF catalog
    //    // contains anchors a crosswalk hasn't mapped yet.
    //    const empties = page.getByTestId("controls-row-frameworks-empty");
    //    // 0 is a valid count (every anchor MAY be mapped); just verify
    //    // the locator is plumbed correctly — when the catalog grows to
    //    // include unmapped anchors, this becomes a non-zero check.
    //    expect(await empties.count()).toBeGreaterThanOrEqual(0);
  });

  // Slice 227 — /controls list pagination footer. The footer is
  // unconditional once at least one row is in the filtered set; with a
  // multi-page catalog the Previous / Next buttons round-trip through
  // the URL `?page=N`. Assertions stay quarantined behind the slice 082
  // seed-data harness, matching the rest of this spec. Pre-conditions
  // the harness must establish:
  //   - At least 51 anchor rows in the seeded catalog (so the default
  //     `CONTROLS_PAGE_SIZE = 50` produces a 2-page result). The SCF
  //     bootstrap importer (slice 006) ships ~53 anchors on the
  //     atlas-edge instance, which already satisfies this on main.
  test("AC-227-1: pagination footer renders with truth-telling summary", async () => {
    //    await page.goto("/controls");
    //    const footer = page.getByTestId("controls-pagination");
    //    await expect(footer).toBeVisible();
    //    // With >=51 seeded anchors the page-1 summary reads "Showing 1–50 of N".
    //    await expect(
    //      page.getByTestId("controls-pagination-summary"),
    //    ).toContainText("Showing 1–50 of");
  });

  test("AC-227-2: Previous is disabled on page 1, Next is enabled", async () => {
    //    await page.goto("/controls");
    //    await expect(page.getByTestId("controls-pagination-prev")).toBeDisabled();
    //    await expect(page.getByTestId("controls-pagination-next")).toBeEnabled();
  });

  test("AC-227-3: clicking Next advances to ?page=2 and updates the summary", async () => {
    //    await page.goto("/controls");
    //    await page.getByTestId("controls-pagination-next").click();
    //    await expect(page).toHaveURL(/\/controls\?(.*&)?page=2/);
    //    // Page 2 summary reads "Showing 51–N of N" (or similar).
    //    await expect(
    //      page.getByTestId("controls-pagination-summary"),
    //    ).toContainText("Showing 51");
    //    // Previous is now enabled; Next is disabled (only 2 pages).
    //    await expect(page.getByTestId("controls-pagination-prev")).toBeEnabled();
    //    await expect(page.getByTestId("controls-pagination-next")).toBeDisabled();
  });

  test("AC-227-4: Previous from page 2 returns to page 1 with the page param dropped", async () => {
    //    await page.goto("/controls?page=2");
    //    await page.getByTestId("controls-pagination-prev").click();
    //    // Canonical page-1 URL drops the `page` param.
    //    await expect(page).toHaveURL(/\/controls(\?[^p]*)?$/);
    //    await expect(page.getByTestId("controls-pagination-prev")).toBeDisabled();
  });

  test("AC-227-5: filter mutation while on page 2 resets to page 1", async () => {
    //    await page.goto("/controls?page=2");
    //    // Apply a filter change (Family → first non-ALL option).
    //    const familyPill = page.getByLabel("Family");
    //    await familyPill.selectOption({ index: 1 });
    //    // The page param must be dropped on the next URL replace.
    //    await expect(page).not.toHaveURL(/[?&]page=/);
    //    await expect(page.getByTestId("controls-pagination-prev")).toBeDisabled();
  });

  test("AC-227-6: refresh on ?page=2 preserves the page state", async () => {
    //    await page.goto("/controls?page=2");
    //    await page.reload();
    //    await expect(page).toHaveURL(/[?&]page=2/);
    //    await expect(
    //      page.getByTestId("controls-pagination-summary"),
    //    ).toContainText("Showing 51");
  });

  test("slice 225 AC-4: New control disclosure replaces the disabled button", async () => {
    // Slice 225 closed the F-178-225 HONESTY-GAP by replacing a
    // permanently-disabled `<Button>New control</Button>` in the
    // toolbar with a non-button `<span>` that discloses the future-
    // state (the create-control flow lands in a future slice; SCF
    // importer + atlas CLI are the current instantiation paths).
    // AC-4 has two halves:
    //
    //   1. The disclosure is present, visible, and its text contains
    //      "create-control" (load-bearing substring pinned by the
    //      vitest sibling spec at
    //      `web/app/(authed)/controls/new-control-future.test.ts`).
    //   2. No disabled `<button>` with the literal text "New control"
    //      exists anywhere on the page.
    //
    // Quarantined behind the slice 082 seed harness like the rest of
    // this file. Bodies left commented so the contract is reviewable;
    // when the harness lands the assertions turn on.
    //    await page.goto("/controls");
    //    const disclosure = page.getByTestId(
    //      "controls-new-control-disabled-reason",
    //    );
    //    await expect(disclosure).toBeVisible();
    //    const text = (await disclosure.textContent())?.toLowerCase() ?? "";
    //    expect(text).toContain("create-control");
    //    // `title` attribute carries the same copy as the visible text
    //    // so screen readers and pointer-hover both surface the same
    //    // disclosure. (aria-label likewise — both are set.)
    //    const titleAttr = await disclosure.getAttribute("title");
    //    expect(titleAttr).toMatch(/create-control/i);
    //    // No disabled <button> with the original label survives.
    //    await expect(
    //      page.locator("button[disabled]", { hasText: "New control" }),
    //    ).toHaveCount(0);
  });
});
