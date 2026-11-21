// Slice 099 — Playwright E2E for the /evidence list view.
//
// Seeded by fixtures/evidence-list.sql: a tenant of the spec's own with
// one control carrying six evidence records (one per result value plus
// two older passes, four inside the last 7 days), a second anchored
// control with one 45-day-old record, and one control with none. The spec signs in to that tenant through /v1/test/issue-jwt
// (web/e2e/tenant-session.ts). #2755 restored the assertions slice 099
// and its follow-ons left commented out behind the slice-082 quarantine.
//
// Run locally:
//   cd web
//   npx playwright install chromium     # once per machine
//   npx playwright test e2e/evidence-list.spec.ts
//
// AC-9 coverage targets: the tenant-wide ledger renders, the true-empty
// state surfaces "Clear filters" + "Set up a connector →" when filters
// return zero rows, a row click opens the record (the evidence detail
// page since #2137), the hash cell is click-to-copy, the Since pill
// narrows, and the Control, Scope and Source pills narrow to their
// matching records (#2771).

import { createHash } from "node:crypto";

import type { Page } from "@playwright/test";

import { expect, test } from "./fixtures";
import { seedFromFixture } from "./seed";
import { signInToTenant } from "./tenant-session";

// Mirrors the ids in fixtures/e2e/evidence-list.sql.
const TENANT = "00000000-0000-0000-0000-2755c0000001";
const CONTROL_WITH_ROWS = "00000000-0000-0000-0000-2755c000a001";
const CONTROL_NO_ROWS = "00000000-0000-0000-0000-2755c000a002";
// The newest record (observed one hour before seeding).
const NEWEST_ID = "00000000-0000-0000-0000-2755c1000001";
// The fixture's hash is sha256 over the record id's text.
const NEWEST_HASH = createHash("sha256").update(NEWEST_ID).digest("hex");
const SEEDED_ROWS = 7;
// Records under CONTROL_WITH_ROWS; the seventh belongs to the second
// anchored control and is 45 days old (#2794).
const ROWS_FOR_CONTROL = 6;
// The default Since window is the last 30 days (#2794), which leaves
// out the 45-day-old record.
const ROWS_IN_DEFAULT_WINDOW = 6;
const ROWS_IN_LAST_7_DAYS = 4;
// Records pushed in the seeded cell (prod · us-east-1), and records
// pushed by test-connector-a.
const ROWS_IN_SEEDED_CELL = 3;
const ROWS_FROM_CONNECTOR_A = 4;

const PUSH_DOC =
  "https://mgoodric.github.io/security-atlas/primitives/evidence/#pushing-evidence-from-your-own-tools";

// The table branch only: at `mobileMode="cards"` the list also mounts a
// hidden card stack that repeats every cell testid.
function table(page: Page) {
  return page.getByTestId("list-table-wrap");
}

test.describe("/evidence list view", () => {
  test.beforeAll(() => {
    seedFromFixture("evidence-list");
  });

  test.beforeEach(async ({ page }) => {
    await signInToTenant(page, {
      tenantId: TENANT,
      roles: ["admin"],
      superAdmin: true,
    });
  });

  test("AC-1: /evidence renders the tenant-wide ledger by default (slice 106)", async ({
    page,
  }) => {
    await page.goto("/evidence");
    await expect(
      page.getByRole("heading", { name: "Evidence", exact: true }),
    ).toBeVisible({ timeout: 30_000 });
    await expect(page.getByTestId("list-page")).toBeVisible();
    // Slice 106: the tenant-wide ledger renders immediately (no
    // pick-a-control gate). #2794: the default Since window is the last
    // 30 days, so the 45-day-old record is not in it; "All time" adds it.
    await expect(table(page)).toBeVisible();
    const rows = table(page).getByTestId("list-table-row");
    await expect(rows).toHaveCount(ROWS_IN_DEFAULT_WINDOW);
    await expect(
      page.getByTestId("list-filter-pills").getByLabel("Since"),
    ).toHaveValue("30d");
    await page
      .getByTestId("list-filter-pills")
      .getByLabel("Since")
      .selectOption({ label: "All time" });
    await expect(page).toHaveURL(/[?&]since_preset=all/);
    await expect(rows).toHaveCount(SEEDED_ROWS);
  });

  // #2771: the Control pill's option value is the tenant control behind
  // the anchor, the id GET /v1/evidence?control_id= matches. #2794: the
  // second anchored control's record drops out, so the count proves the
  // pill narrows.
  test("AC-3: selecting a control narrows the ledger (slice 106 — filter, not gate)", async ({
    page,
  }) => {
    await page.goto("/evidence?since_preset=all");
    await expect(table(page).getByTestId("list-table-row")).toHaveCount(
      SEEDED_ROWS,
      { timeout: 30_000 },
    );
    await page
      .getByTestId("list-filter-pills")
      .getByLabel("Control")
      .selectOption(CONTROL_WITH_ROWS);
    await expect(page).toHaveURL(
      new RegExp(`[?&]control_id=${CONTROL_WITH_ROWS}`),
    );
    await expect(table(page).getByTestId("list-table-row")).toHaveCount(
      ROWS_FOR_CONTROL,
    );
  });

  test("AC-5 result-cell: each row renders its result enum value (slice 106)", async ({
    page,
  }) => {
    await page.goto("/evidence");
    const cells = table(page).getByTestId("evidence-row-result");
    await expect(cells).toHaveCount(ROWS_IN_DEFAULT_WINDOW, {
      timeout: 30_000,
    });
    // Newest first; the four newest rows carry one result value each.
    await expect(cells.nth(0)).toHaveText("Pass");
    await expect(cells.nth(1)).toHaveText("Fail");
    await expect(cells.nth(2)).toHaveText("Not applicable");
    await expect(cells.nth(3)).toHaveText("Inconclusive");
    // The raw enum value rides on the title attribute.
    await expect(cells.nth(2)).toHaveAttribute("title", "na");
  });

  test("AC-3 hash: hash cell renders as 8-char prefix only", async ({
    page,
  }) => {
    await page.goto(`/evidence?control_id=${CONTROL_WITH_ROWS}`);
    const hashCell = table(page).getByTestId("evidence-row-hash").first();
    // Render is "<8 chars>…".
    await expect(hashCell).toHaveText(`${NEWEST_HASH.slice(0, 8)}…`, {
      timeout: 30_000,
    });
  });

  test("AC-5: empty state surfaces with two CTAs when control has zero records", async ({
    page,
  }) => {
    await page.goto(`/evidence?control_id=${CONTROL_NO_ROWS}`);
    await expect(page.getByTestId("list-empty-state")).toBeVisible({
      timeout: 30_000,
    });
    await expect(page.getByTestId("evidence-empty-title")).toHaveText(
      "No evidence records match these filters",
    );
    // Two CTAs: Clear filters (enabled, a filter is narrowing) + Set up a
    // connector ->.
    await expect(page.getByTestId("evidence-empty-clear")).toBeEnabled();
    await expect(page.getByTestId("evidence-empty-connector")).toHaveText(
      "Set up a connector →",
    );
  });

  // #2137 replaced the inline drawer with the evidence detail page: a row
  // click navigates there, and the page shows the FULL hash.
  test("#2137 (supersedes AC-7): row click opens /evidence/[id]", async ({
    page,
  }) => {
    await page.goto(`/evidence?control_id=${CONTROL_WITH_ROWS}`);
    await table(page)
      .getByTestId("list-table-row")
      .first()
      .click({ timeout: 30_000 });
    await expect(page).toHaveURL(new RegExp(`/evidence/${NEWEST_ID}$`));
    await expect(page.getByTestId("evidence-detail-hash")).toHaveText(
      NEWEST_HASH,
      { timeout: 30_000 },
    );
  });

  test("AC-3 hash-copy: clicking the hash cell copies the full hash", async ({
    page,
  }) => {
    await page
      .context()
      .grantPermissions(["clipboard-read", "clipboard-write"]);
    await page.goto(`/evidence?control_id=${CONTROL_WITH_ROWS}`);
    const hashCell = table(page).getByTestId("evidence-row-hash").first();
    await hashCell.click({ timeout: 30_000 });
    // The cell briefly shows "Copied!" feedback, and the click does not
    // also open the record.
    await expect(hashCell).toHaveText("Copied!");
    await expect(page).toHaveURL(/\/evidence\?/);
    const clip = await page.evaluate(() => navigator.clipboard.readText());
    expect(clip).toBe(NEWEST_HASH);
  });

  test("P0-A3: filter row is horizontal pill row, NOT a left sidebar", async ({
    page,
  }) => {
    await page.goto("/evidence");
    const pills = page.getByTestId("list-filter-pills");
    await expect(pills).toBeVisible({ timeout: 30_000 });
    await expect(pills.locator("select")).toHaveCount(6);
    // The pill row sits above the table, not beside it.
    await expect(table(page)).toBeVisible();
    const row = await pills.boundingBox();
    const tbl = await table(page).boundingBox();
    expect(row).not.toBeNull();
    expect(tbl).not.toBeNull();
    expect(row!.y + row!.height).toBeLessThanOrEqual(tbl!.y);
    expect(row!.x + row!.width).toBeGreaterThan(tbl!.x + tbl!.width / 2);
  });

  // Slice 234 — three new filter pills bring the row to six-pill
  // mockup parity (Plans/_archive/mockups/evidence.html lines 125-184):
  // Source (composite actor_type|actor_id), Scope (scope_cell_id), and
  // Since (preset window mapped client-side to an RFC3339 cutoff).
  test("slice 234 — Source / Scope / Since pills render in the filter row", async ({
    page,
  }) => {
    await page.goto("/evidence");
    const filterRow = page.getByTestId("list-filter-pills");
    await expect(filterRow.getByLabel("Source")).toBeVisible({
      timeout: 30_000,
    });
    await expect(filterRow.getByLabel("Scope")).toBeVisible();
    await expect(filterRow.getByLabel("Since")).toBeVisible();
    await expect(filterRow.getByLabel("Since").locator("option")).toHaveText([
      "All time",
      "Last 24 hours",
      "Last 7 days",
      "Last 30 days",
      "Audit period (current)",
    ]);
    // The tenant's one scope cell is offered by its label.
    await expect(filterRow.getByLabel("Scope").locator("option")).toHaveText([
      "All cells",
      "prod · us-east-1",
    ]);
  });

  test("slice 234 — Since pill 'Last 7 days' narrows the result set", async ({
    page,
  }) => {
    await page.goto("/evidence");
    const rows = table(page).getByTestId("list-table-row");
    await expect(rows).toHaveCount(ROWS_IN_DEFAULT_WINDOW, { timeout: 30_000 });
    await page
      .getByTestId("list-filter-pills")
      .getByLabel("Since")
      .selectOption("7d");
    // The 10- and 20-day-old records drop out.
    await expect(rows).toHaveCount(ROWS_IN_LAST_7_DAYS);
    // URL carries the preset key, not the resolved RFC3339 cutoff
    // (a sliding window must not stale on bookmark reload).
    expect(page.url()).toContain("since_preset=7d");
    expect(page.url()).not.toContain("since=");
  });

  // #2771: a record is in a cell when the scope it was pushed with
  // covers the cell's dimensions. The record with no pushed scope drops
  // out, and the hint says why.
  test("slice 234 — Scope pill narrows the ledger to one cell", async ({
    page,
  }) => {
    await page.goto("/evidence");
    await expect(table(page).getByTestId("list-table-row")).toHaveCount(
      ROWS_IN_DEFAULT_WINDOW,
      { timeout: 30_000 },
    );
    await expect(page.getByTestId("evidence-scope-hint")).toHaveCount(0);
    await page
      .getByTestId("list-filter-pills")
      .getByLabel("Scope")
      .selectOption({ index: 1 });
    await expect(page).toHaveURL(/[?&]scope_cell_id=/);
    await expect(table(page).getByTestId("list-table-row")).toHaveCount(
      ROWS_IN_SEEDED_CELL,
    );
    await expect(page.getByTestId("evidence-scope-hint")).toHaveText(
      "Scope matches the scope each record was pushed with. Records pushed before scope capture have none, so no Scope filter matches them.",
    );
  });

  // #2771: the Source column and pill read the row's
  // `source_attribution`, the column the source_actor_* filters match.
  test("slice 234 — Source pill sets BOTH source_actor_type + source_actor_id atomically", async ({
    page,
  }) => {
    await page.goto("/evidence");
    await expect(
      table(page).getByTestId("evidence-row-source").first(),
    ).toHaveText("connector · test-connector-a", { timeout: 30_000 });
    await page
      .getByTestId("list-filter-pills")
      .getByLabel("Source")
      .selectOption({ index: 1 });
    await expect(page).toHaveURL(/[?&]source_actor_type=connector/);
    await expect(page).toHaveURL(/[?&]source_actor_id=test-connector-a/);
    await expect(table(page).getByTestId("list-table-row")).toHaveCount(
      ROWS_FROM_CONNECTOR_A,
    );
  });

  test("slice 234 — clearing filters drops all three new params", async ({
    page,
  }) => {
    await page.goto(
      "/evidence?since_preset=7d&scope_cell_id=22222222-2222-2222-2222-222222222222&source_actor_type=connector&source_actor_id=aws-connector",
    );
    await expect(page.getByTestId("list-empty-state")).toBeVisible({
      timeout: 30_000,
    });
    await page.getByTestId("evidence-empty-clear").click();
    await expect(page).toHaveURL(/\/evidence$/);
    await expect(table(page).getByTestId("list-table-row")).toHaveCount(
      ROWS_IN_DEFAULT_WINDOW,
    );
    expect(page.url()).not.toContain("since_preset=");
    expect(page.url()).not.toContain("scope_cell_id=");
    expect(page.url()).not.toContain("source_actor_type=");
    expect(page.url()).not.toContain("source_actor_id=");
  });

  // Slice 233 — UI honesty: "Push evidence" CTA is no longer
  // permanently-disabled. It is a primary-styled `<a>` pointing at the
  // canonical CLI push doc (the evidence-primitive doc's "Pushing
  // evidence from your own tools" section). The subtitle's second
  // sentence carries the same link inline so both surfaces point to the
  // same destination.
  test("slice 233 — Push evidence CTA links to the CLI push doc and is navigable", async ({
    page,
  }) => {
    await page.goto("/evidence");
    const cta = page.getByTestId("evidence-push-cta");
    await expect(cta).toBeVisible({ timeout: 30_000 });
    await expect(cta).toBeEnabled();
    await expect(cta).not.toHaveAttribute("aria-disabled", /.*/);
    await expect(cta).toHaveAttribute("href", PUSH_DOC);
    await expect(cta).toHaveAttribute("target", "_blank");
    await expect(cta).toHaveAttribute("rel", "noopener noreferrer");
    const inline = page.getByTestId("evidence-push-cta-inline");
    await expect(inline).toBeVisible();
    await expect(inline).toHaveAttribute("href", PUSH_DOC);
  });
});
