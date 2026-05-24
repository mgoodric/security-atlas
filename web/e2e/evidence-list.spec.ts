// Slice 099 — Playwright E2E for the /evidence list view.
//
// Runner status (post-slice-069 / 071 audit):
// Playwright IS installed in `web/`. This spec is quarantined behind
// slice 082 (the seed-data harness) per slice 079's decision; when
// that harness lands, the un-commented assertions below become the
// gate. The test bodies are preserved verbatim as a reviewable
// contract per the slice 040 / 042 / 056 / 060 / 064 / 071 / 094 /
// 098 / 100 / 102 precedent.
//
// Run locally:
//   cd web
//   npx playwright install chromium     # once per machine
//   npx playwright test e2e/evidence-list.spec.ts
//
// Pre-conditions the seed-data harness (slice 082) must establish
// before the commented assertions are turned on:
//   - PLATFORM_BASE_URL points at a running platform instance
//   - TEST_BEARER carries a credential in a tenant with at least:
//     * 1 SCF anchor with a tenant control instantiated
//     * 1 anchor that has >=3 evidence records ingested (drives
//       row-render + filter narrowing)
//     * 1 anchor that has zero evidence records (drives empty state)
//
// AC-9 coverage targets: list renders for a selected control, control
// pill narrows the result set, true-empty state surfaces "Clear
// filters" + "Set up a connector →" when filters return zero rows,
// row click opens the drawer, hash cell is click-to-copy.

import { test } from "@playwright/test";

test.describe("/evidence list view", () => {
  test("AC-1: /evidence renders the tenant-wide ledger by default (slice 106)", async () => {
    //    await page.goto("/login");
    //    await page.fill('input[name="token"]', process.env.TEST_BEARER!);
    //    await page.click("button[type=submit]");
    //    await page.goto("/evidence");
    //    await expect(page.getByRole("heading", { name: /Evidence ledger/ })).toBeVisible();
    //    await expect(page.getByTestId("list-page")).toBeVisible();
    //    // Slice 106: the tenant-wide ledger renders immediately (no
    //    // pick-a-control gate). The pre-106 `evidence-pick-control-title`
    //    // testid was retired.
    //    await expect(page.getByTestId("list-table-wrap")).toBeVisible();
  });

  test("AC-3: selecting a control narrows the ledger (slice 106 — filter, not gate)", async () => {
    //    await page.goto("/evidence");
    //    const controlPill = page.getByLabel("Control");
    //    // Select the first non-sentinel option (the harness-seeded anchor
    //    // with evidence rows). Slice 106: this NARROWS the tenant-wide
    //    // list to that control rather than unlocking the data fetch.
    //    await controlPill.selectOption({ index: 1 });
    //    await page.waitForLoadState("networkidle");
    //    await expect(page.getByTestId("list-table-wrap")).toBeVisible();
    //    const rowCount = await page.getByTestId("list-table-row").count();
    //    expect(rowCount).toBeGreaterThan(0);
  });

  test("AC-5 result-cell: each row renders its result enum value (slice 106)", async () => {
    //    await page.goto("/evidence");
    //    const resultCell = page.getByTestId("evidence-row-result").first();
    //    const text = await resultCell.innerText();
    //    // The cell renders one of pass / fail / na / inconclusive.
    //    expect(["pass", "fail", "na", "inconclusive"]).toContain(text.trim());
  });

  test("AC-3 hash: hash cell renders as 8-char prefix only", async () => {
    //    await page.goto("/evidence?control_id=__seeded-anchor-with-rows__");
    //    const hashCell = page.getByTestId("evidence-row-hash").first();
    //    const text = await hashCell.innerText();
    //    // Render is "<8 chars>…" — strip the trailing ellipsis, check 8.
    //    const prefix = text.replace("…", "").trim();
    //    expect(prefix.length).toBe(8);
  });

  test("AC-5: empty state surfaces with two CTAs when control has zero records", async () => {
    //    await page.goto("/evidence?control_id=__seeded-anchor-no-rows__");
    //    await expect(page.getByTestId("list-empty-state")).toBeVisible();
    //    await expect(page.getByTestId("evidence-empty-title")).toBeVisible();
    //    // Two CTAs: Clear filters + Set up a connector ->
    //    await expect(page.getByTestId("evidence-empty-clear")).toBeVisible();
    //    await expect(page.getByTestId("evidence-empty-connector")).toBeVisible();
  });

  test("AC-7: row click opens the inline record drawer", async () => {
    //    await page.goto("/evidence?control_id=__seeded-anchor-with-rows__");
    //    await page.getByTestId("list-table-row").first().click();
    //    await expect(page.getByTestId("evidence-row-drawer")).toBeVisible();
    //    // Drawer shows the FULL hash (not just the 8-char prefix).
    //    const fullHash = page.getByTestId("evidence-drawer-full-hash");
    //    await expect(fullHash).toBeVisible();
    //    const text = await fullHash.innerText();
    //    expect(text.length).toBeGreaterThanOrEqual(40);
  });

  test("AC-3 hash-copy: clicking the hash cell copies the full hash", async () => {
    //    // Grant clipboard permissions for the test context.
    //    await page.context().grantPermissions(["clipboard-read", "clipboard-write"]);
    //    await page.goto("/evidence?control_id=__seeded-anchor-with-rows__");
    //    const hashCell = page.getByTestId("evidence-row-hash").first();
    //    await hashCell.click();
    //    // Cell briefly shows "Copied!" feedback.
    //    await expect(hashCell).toContainText(/Copied!/);
    //    // Clipboard contains the FULL hash (>= 40 chars sha256).
    //    const clip = await page.evaluate(() => navigator.clipboard.readText());
    //    expect(clip.length).toBeGreaterThanOrEqual(40);
  });

  test("P0-A3: filter row is horizontal pill row, NOT a left sidebar", async () => {
    //    await page.goto("/evidence");
    //    await expect(page.getByTestId("list-filter-pills")).toBeVisible();
    //    // No left filter sidebar exists. The pill row sits above the table.
  });

  // Slice 234 — three new filter pills bring the row to six-pill
  // mockup parity (Plans/mockups/evidence.html lines 125-184):
  // Source (composite actor_type|actor_id), Scope (scope_cell_id), and
  // Since (preset window mapped client-side to an RFC3339 cutoff).
  // Each pill renders, selecting a non-sentinel option narrows the
  // result count, and clearing returns to the unfiltered ledger.
  test("slice 234 — Source / Scope / Since pills render in the filter row", async () => {
    //    await page.goto("/evidence");
    //    const filterRow = page.getByTestId("list-filter-pills");
    //    await expect(filterRow.getByLabel("Source")).toBeVisible();
    //    await expect(filterRow.getByLabel("Scope")).toBeVisible();
    //    await expect(filterRow.getByLabel("Since")).toBeVisible();
  });

  test("slice 234 — Since pill 'Last 7 days' narrows the result set", async () => {
    //    await page.goto("/evidence");
    //    // Baseline: count the rows in the default window (last 30 days).
    //    const before = await page.getByTestId("list-table-row").count();
    //    await page.getByLabel("Since").selectOption("7d");
    //    await page.waitForLoadState("networkidle");
    //    const after = await page.getByTestId("list-table-row").count();
    //    expect(after).toBeLessThanOrEqual(before);
    //    // URL carries the preset key, not the resolved RFC3339 cutoff
    //    // (a sliding window must not stale on bookmark reload).
    //    expect(page.url()).toContain("since_preset=7d");
  });

  test("slice 234 — Scope pill narrows the ledger to one cell", async () => {
    //    await page.goto("/evidence");
    //    // Pick the first non-sentinel cell.
    //    await page.getByLabel("Scope").selectOption({ index: 1 });
    //    await page.waitForLoadState("networkidle");
    //    expect(page.url()).toContain("scope_cell_id=");
  });

  test("slice 234 — Source pill sets BOTH source_actor_type + source_actor_id atomically", async () => {
    //    await page.goto("/evidence");
    //    // Pick the first non-sentinel observed (type, id) tuple.
    //    await page.getByLabel("Source").selectOption({ index: 1 });
    //    await page.waitForLoadState("networkidle");
    //    expect(page.url()).toContain("source_actor_type=");
    //    expect(page.url()).toContain("source_actor_id=");
  });

  test("slice 234 — clearing filters drops all three new params", async () => {
    //    await page.goto(
    //      "/evidence?since_preset=7d&scope_cell_id=22222222-2222-2222-2222-222222222222&source_actor_type=connector&source_actor_id=aws-connector",
    //    );
    //    await expect(page.getByTestId("list-empty-state")).toBeVisible();
    //    await page.getByTestId("evidence-empty-clear").click();
    //    await page.waitForLoadState("networkidle");
    //    expect(page.url()).not.toContain("since_preset=");
    //    expect(page.url()).not.toContain("scope_cell_id=");
    //    expect(page.url()).not.toContain("source_actor_type=");
    //    expect(page.url()).not.toContain("source_actor_id=");
  });

  // Slice 233 — UI honesty: "Push evidence" CTA is no longer
  // permanently-disabled. It is a primary-styled `<a>` pointing at the
  // canonical CLI push doc (the evidence-primitive doc's "Pushing
  // evidence from your own tools" section). The subtitle's second
  // sentence carries the same link inline so operators surfaces both
  // points to the same destination.
  test("slice 233 — Push evidence CTA links to the CLI push doc and is navigable", async () => {
    //    await page.goto("/evidence");
    //    const cta = page.getByTestId("evidence-push-cta");
    //    // The button is replaced by an `<a>` — assert it is enabled
    //    // (no `disabled` or `aria-disabled` attribute) and its href
    //    // resolves to the canonical CLI quickstart doc.
    //    await expect(cta).toBeVisible();
    //    await expect(cta).toBeEnabled();
    //    await expect(cta).toHaveAttribute(
    //      "href",
    //      "/docs/primitives/evidence#pushing-evidence-from-your-own-tools",
    //    );
    //    await expect(cta).toHaveAttribute("target", "_blank");
    //    // The subtitle ALSO carries an inline link to the same destination.
    //    const inline = page.getByTestId("evidence-push-cta-inline");
    //    await expect(inline).toBeVisible();
    //    await expect(inline).toHaveAttribute(
    //      "href",
    //      "/docs/primitives/evidence#pushing-evidence-from-your-own-tools",
    //    );
  });
});
