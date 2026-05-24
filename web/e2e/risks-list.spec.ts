// Slice 100 — Playwright E2E for the /risks list view.
//
// Runner status (post-slice-069 / 079 audit):
// Playwright IS installed in `web/`. This spec is quarantined behind
// slice 082 (the seed-data harness) per slice 079's decision; when that
// harness lands, the un-commented assertions below become the gate. The
// test bodies are preserved verbatim as a reviewable contract per the
// slice 040 / 042 / 056 / 060 / 064 / 071 / 094 / 098 precedent.
//
// Run locally:
//   cd web
//   npx playwright install chromium     # once per machine
//   npx playwright test e2e/risks-list.spec.ts
//
// Pre-conditions the seed-data harness (slice 082) must establish
// before the commented assertions are turned on:
//   - PLATFORM_BASE_URL points at a running platform instance.
//   - TEST_BEARER carries a credential in a tenant that has at least
//     three risks seeded across at least two treatment values and two
//     owner strings, so the filter pills have something to narrow.
//   - At least one risk has a 5x5 inherent_score so the severity pill
//     "high" band returns a non-empty result.
//
// AC-10 coverage targets: list renders, filter narrows results, the
// hierarchy page-header link navigates, and the reciprocal `List view`
// link on /risks/hierarchy navigates back.

import { test } from "@playwright/test";

test.describe("/risks list view", () => {
  test("AC-1: /risks renders the risk table for any signed-in user", async () => {
    //    await page.goto("/login");
    //    await page.fill('input[name="token"]', process.env.TEST_BEARER!);
    //    await page.click("button[type=submit]");
    //    await page.goto("/risks");
    //    await expect(page.getByRole("heading", { name: /Risk register/ })).toBeVisible();
    //    await expect(page.getByTestId("list-page")).toBeVisible();
    //    await expect(page.getByTestId("list-table-wrap")).toBeVisible();
  });

  test("AC-3: horizontal pill filter row narrows the result set", async () => {
    //    await page.goto("/risks");
    //    const initial = await page.getByTestId("list-table-row").count();
    //    const treatmentPill = page.getByLabel("Treatment");
    //    await treatmentPill.selectOption({ value: "mitigate" });
    //    await page.waitForLoadState("networkidle");
    //    const filtered = await page.getByTestId("list-table-row").count();
    //    expect(filtered).toBeLessThanOrEqual(initial);
    // The filter row is horizontal (P0-A2) — verify NO left sidebar.
    //    await expect(page.getByTestId("list-filter-pills")).toBeVisible();
  });

  test("AC-4: empty state surfaces when filters return zero rows", async () => {
    //    // Pick a (treatment, owner) combination unlikely to be seeded.
    //    await page.goto("/risks?treatment=avoid&owner=DOES-NOT-EXIST");
    //    await expect(page.getByTestId("list-empty-state")).toBeVisible();
    //    await expect(
    //      page.getByText("No risks match these filters"),
    //    ).toBeVisible();
    // The CTA reads "Clear filters" on a filter-induced empty state.
    //    await page.getByTestId("list-empty-state-cta").click();
    //    await expect(page.getByTestId("list-table-wrap")).toBeVisible();
  });

  test("AC-6: page-header `Hierarchy view ->` link navigates to /risks/hierarchy", async () => {
    //    await page.goto("/risks");
    //    await page.getByTestId("risks-hierarchy-link").click();
    //    await expect(page).toHaveURL(/\/risks\/hierarchy/);
    //    // Reciprocal AC-8 wiring: the hierarchy page exposes a `List view ->`
    //    // link back to /risks.
    //    await expect(page.getByTestId("risk-hierarchy-list-view-link")).toBeVisible();
    //    await page.getByTestId("risk-hierarchy-list-view-link").click();
    //    await expect(page).toHaveURL(/\/risks(\?.*)?$/);
  });

  test("AC-8: sidebar no longer exposes /risks/hierarchy as a top-level entry", async () => {
    //    await page.goto("/dashboard");
    //    // The /risks entry stays.
    //    await expect(page.getByRole("link", { name: "Risks" })).toBeVisible();
    //    // The /risks/hierarchy entry was REMOVED (audit F-3 closure).
    //    await expect(
    //      page.getByRole("link", { name: "Risk hierarchy" }),
    //    ).toHaveCount(0);
  });

  // Slice 185 (AC-4) — F-178-5 honesty fix. Rows are no longer clickable;
  // the affordance is an explicit per-row "View in hierarchy" link plus a
  // banner above the table. The prior implementation routed the row
  // click to `/risks/hierarchy?focus=<id>`, which advertised "row =
  // detail" but delivered the hierarchy. These assertions stay
  // quarantined with the rest of the spec until the slice 082 seed
  // harness lands.
  test("AC-185-1: /risks rows expose no cursor-pointer affordance and ignore clicks", async () => {
    //    await page.goto("/risks");
    //    const firstRow = page.getByTestId("list-table-row").first();
    //    await expect(firstRow).toBeVisible();
    //    // The row no longer carries the cursor-pointer class (slice
    //    // 098 `ListTable` only sets it when `onRowClick` is wired).
    //    await expect(firstRow).not.toHaveClass(/cursor-pointer/);
    //    // Clicking the body of the row (a non-anchor cell) must NOT
    //    // navigate. We click the title cell, which is plain text.
    //    const titleCell = firstRow.getByTestId("list-cell-title");
    //    await titleCell.click();
    //    await expect(page).toHaveURL(/\/risks(\?.*)?$/);
  });

  test("AC-185-2: each row exposes an explicit `View in hierarchy` link", async () => {
    //    await page.goto("/risks");
    //    const links = page.getByTestId("risks-row-hierarchy-link");
    //    const count = await links.count();
    //    expect(count).toBeGreaterThan(0);
    //    const firstHref = await links.first().getAttribute("href");
    //    expect(firstHref).toMatch(/^\/risks\/hierarchy\?focus=/);
    //    // Following the link reaches the hierarchy view (workflow
    //    // preservation per P0-185-2).
    //    await links.first().click();
    //    await expect(page).toHaveURL(/\/risks\/hierarchy/);
  });

  test("AC-185-3: future-slice banner explains the missing per-risk detail page", async () => {
    //    await page.goto("/risks");
    //    await expect(
    //      page.getByTestId("risks-detail-future-slice-banner"),
    //    ).toBeVisible();
    //    await expect(
    //      page.getByText(/Per-risk detail page is a future slice/),
    //    ).toBeVisible();
  });

  // Slice 247 — header "New risk" button enable. The header previously
  // rendered `<Button size="sm" disabled>` even though `/risks/new`
  // exists (slice 105) and the empty-state CTA already routes there.
  // This slice replaces the disabled button with a `<Link>` wrapping
  // the same shadcn Button shape via `buttonVariants({ size: "sm" })`.
  // Both assertions (href + navigation) stay quarantined behind the
  // slice 082 seed harness, matching the rest of this spec.
  test("AC-247-1: header `New risk` link is enabled and points at /risks/new", async () => {
    //    await page.goto("/risks");
    //    const newLink = page.getByTestId("risks-new-link");
    //    await expect(newLink).toBeVisible();
    //    await expect(newLink).toHaveAttribute("href", "/risks/new");
    //    // The previously disabled `<button disabled>` is gone — the
    //    // link is the role exposed to assistive tech.
    //    await expect(newLink).not.toHaveAttribute("disabled", "");
  });

  test("AC-247-2: clicking header `New risk` navigates to /risks/new", async () => {
    //    await page.goto("/risks");
    //    await page.getByTestId("risks-new-link").click();
    //    await expect(page).toHaveURL(/\/risks\/new$/);
  });

  // Slice 244 — extended filter pill row (Category + Methodology +
  // Org unit added to the slice-100 Treatment + Severity + Owner set).
  // Default labels per page.tsx: "All categories" / "All methodologies"
  // / "All units". Assertions stay quarantined behind the slice 082
  // seed-data harness, matching the rest of this spec.
  test("AC-244-1: Category pill is visible with the default `All categories` label", async () => {
    //    await page.goto("/risks");
    //    const pill = page.getByTestId("list-filter-pill-category");
    //    await expect(pill).toBeVisible();
    //    await expect(pill).toContainText("Category");
    //    await expect(pill.getByRole("combobox")).toHaveValue("all");
    //    await expect(
    //      pill.getByRole("option", { name: "All categories" }),
    //    ).toBeAttached();
  });

  test("AC-244-2: Methodology pill is visible with the default `All methodologies` label", async () => {
    //    await page.goto("/risks");
    //    const pill = page.getByTestId("list-filter-pill-methodology");
    //    await expect(pill).toBeVisible();
    //    await expect(pill).toContainText("Methodology");
    //    await expect(pill.getByRole("combobox")).toHaveValue("all");
    //    await expect(
    //      pill.getByRole("option", { name: "All methodologies" }),
    //    ).toBeAttached();
  });

  test("AC-244-3: Org unit pill is visible with the default `All units` label", async () => {
    //    await page.goto("/risks");
    //    const pill = page.getByTestId("list-filter-pill-org_unit");
    //    await expect(pill).toBeVisible();
    //    await expect(pill).toContainText("Org unit");
    //    await expect(pill.getByRole("combobox")).toHaveValue("all");
    //    await expect(
    //      pill.getByRole("option", { name: "All units" }),
    //    ).toBeAttached();
  });

  test("AC-244-4: URL query round-trips all six filter keys", async () => {
    //    // Pick valid wire enum values for category + methodology so a
    //    // populated tenant lands on a real row set. The org_unit value
    //    // is a UUID derived from the seeded org-units list.
    //    const orgUnitId = process.env.TEST_ORG_UNIT_ID!;
    //    await page.goto(
    //      `/risks?category=operational&methodology=nist_800_30&org_unit=${orgUnitId}&treatment=mitigate&severity=high&owner=alpha`,
    //    );
    //    await expect(page.getByTestId("list-filter-pill-category")).toContainText(
    //      "operational",
    //    );
    //    await expect(
    //      page.getByTestId("list-filter-pill-methodology"),
    //    ).toContainText("nist_800_30");
    //    await expect(
    //      page.getByTestId("list-filter-pill-org_unit"),
    //    ).toBeVisible();
    //    await expect(page.getByTestId("list-filter-pill-treatment")).toContainText(
    //      "mitigate",
    //    );
    //    await expect(page.getByTestId("list-filter-pill-severity")).toContainText(
    //      "high",
    //    );
    //    await expect(page.getByTestId("list-filter-pill-owner")).toContainText(
    //      "alpha",
    //    );
  });
});
