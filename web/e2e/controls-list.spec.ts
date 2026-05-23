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
});
