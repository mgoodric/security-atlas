// Slice 041 — Playwright E2E for the control detail view.
//
// Seeded by fixtures/e2e/control-detail.sql: a tenant of the spec's own
// (#2755) with one control anchored to an SCF anchor that two frameworks
// crosswalk to. The control is in the Alpha framework's activated scope
// and out of the Beta framework's, so the coverage table carries one
// numeric row and one dashed out-of-scope row. One `pass` evaluation gives
// the 30-day effectiveness data the numeric coverage needs; the tenant has
// no evidence, policies, risks or exceptions. The spec signs in to that
// tenant with an admin JWT (web/e2e/tenant-session.ts). #2755 restored the
// assertions slice 082 left commented out, re-pointed at the page as it
// ships after #2140: the overview holds Evidence, Coverage and Linked; the
// framework graph is on the Framework map tab; the evaluation history and
// the freshness clock are on the History tab.
//
// Run locally:
//   cd web
//   npx playwright install chromium     # once per machine
//   npx playwright test e2e/control-detail.spec.ts

import type { Page } from "@playwright/test";

import { expect, test } from "./fixtures";
import { seedFromFixture } from "./seed";
import { signInToTenant } from "./tenant-session";

// Mirror the ids in fixtures/e2e/control-detail.sql.
const TENANT = "00000000-0000-0000-0000-2755b0100001";
const CONTROL = "00000000-0000-0000-0000-2755b0100c02";
const ALPHA_FV = "00000000-0000-0000-0000-2755b0100a02";
const BETA_FV = "00000000-0000-0000-0000-2755b0100b02";

// Navigate to the control and wait for the coverage read: the page shows
// a skeleton until it settles.
async function gotoControl(page: Page, tab?: string): Promise<void> {
  const coverage = page.waitForResponse(
    (r) =>
      r.url().includes(`/api/controls/${CONTROL}/coverage`) &&
      r.status() === 200,
    { timeout: 30_000 },
  );
  await page.goto(
    tab ? `/controls/${CONTROL}?tab=${tab}` : `/controls/${CONTROL}`,
  );
  await coverage;
  await expect(page.getByTestId("control-detail")).toBeVisible();
}

function coverageRow(page: Page, code: string) {
  return page.getByTestId("coverage-row").filter({ hasText: code });
}

test.describe("control detail view", () => {
  test.beforeAll(() => {
    seedFromFixture("control-detail");
  });

  test.beforeEach(async ({ page }) => {
    await signInToTenant(page, {
      tenantId: TENANT,
      roles: ["admin"],
      superAdmin: true,
    });
  });

  test("AC-1: /controls/:id renders the full detail layout", async ({
    page,
  }) => {
    await gotoControl(page);
    // Header: title, SCF anchor pill, lifecycle badge.
    await expect(page.getByTestId("control-title")).toHaveText(
      "Control detail e2e control",
    );
    await expect(page.getByTestId("scf-anchor-pill")).toHaveText("CD-E2E-A1");
    await expect(page.getByTestId("lifecycle-badge")).toBeVisible();
    // The state strip took the KPI strip's place (#2140).
    await expect(page.getByTestId("control-state-strip")).toBeVisible();
    // Overview: Evidence, Coverage and Linked, in that order.
    await expect(page.getByTestId("control-detail-section")).toHaveCount(3);
    const sections = await page
      .getByTestId("control-detail-section")
      .evaluateAll((els) => els.map((el) => el.getAttribute("data-section")));
    expect(sections).toEqual(["evidence", "coverage", "linked"]);
    for (const id of [
      "evidence-stream-section",
      "coverage-section",
      "effective-scope-section",
      "policies-section",
      "risks-section",
    ]) {
      await expect(page.getByTestId(id)).toBeVisible();
    }
    // The graph is on the Framework map tab, never the overview.
    await expect(page.getByTestId("ucf-viz-section")).toHaveCount(0);
    await page.getByTestId("control-tab-map").click();
    await expect(page.getByTestId("ucf-viz-section")).toBeVisible();
    // History holds the evaluation history and the freshness clock.
    await page.getByTestId("control-tab-history").click();
    await expect(page.getByTestId("audit-log-section")).toBeVisible();
    await expect(page.getByTestId("freshness-section")).toBeVisible();
  });

  test("AC-2: coverage table shows STRM types + strengths per row", async ({
    page,
  }) => {
    await gotoControl(page);
    await expect(page.getByTestId("coverage-table")).toBeVisible();
    await expect(page.getByTestId("coverage-row")).toHaveCount(2);
    // Each row carries the raw backend relationship_type on its STRM
    // badge, its strength, and a strength bar.
    const alpha = coverageRow(page, "CD-ALPHA-1");
    await expect(alpha.locator("[data-strm]")).toHaveAttribute(
      "data-strm",
      "equal",
    );
    await expect(alpha).toContainText("0.90");
    await expect(alpha.getByTestId("strength-bar")).toBeAttached();
    const beta = coverageRow(page, "CD-BETA-1");
    await expect(beta.locator("[data-strm]")).toHaveAttribute(
      "data-strm",
      "subset_of",
    );
    await expect(beta).toContainText("0.60");
    await expect(beta.getByTestId("strength-bar")).toBeAttached();
  });

  test("AC-3: UCF mini-viz renders control -> anchor -> requirements", async ({
    page,
  }) => {
    await gotoControl(page, "map");
    await expect(page.getByTestId("ucf-mini-viz")).toBeVisible();
    // Node labels wrap across <tspan>s, so match the control's bundle id.
    await expect(page.getByTestId("ucf-viz-control")).toContainText(
      "cd_e2e_c1",
    );
    await expect(page.getByTestId("ucf-viz-anchor")).toContainText("CD-E2E-A1");
    // One requirement node per mapped requirement, each edge drawn from
    // the anchor (P0-1: no requirement-to-requirement edge exists in the
    // component).
    await expect(page.getByTestId("ucf-viz-requirement")).toHaveCount(2);
    await expect(
      page.getByTestId("ucf-viz-requirement").locator("path[data-strm]"),
    ).toHaveCount(2);
  });

  test("AC-4: evidence stream — bound to /v1/evidence?control_id=… (slice 253)", async ({
    page,
  }) => {
    // Slice 253 — the section renders the latest records or, when there
    // are none, the truly-empty copy, never the pre-slice-253
    // endpoint-pending placeholder. The seeded tenant has no evidence.
    const evidence = page.waitForResponse(
      (r) =>
        r.url().includes("/api/evidence") &&
        r.url().includes(`control_id=${CONTROL}`),
      { timeout: 30_000 },
    );
    await gotoControl(page);
    expect((await evidence).status()).toBe(200);
    const section = page.getByTestId("evidence-stream-section");
    await expect(section).toBeVisible();
    await expect(page.getByTestId("evidence-stream-placeholder")).toHaveCount(
      0,
    );
    const empty = page.getByTestId("evidence-stream-empty");
    await expect(empty).toHaveText(
      "No evidence records for this control in the last 30 days.",
    );
    await expect(section).not.toContainText("not yet wired");
    await expect(section).not.toContainText("not on main yet");
  });

  test("AC-4-b: right-rail Policies / Risks / Audit-log render real backings (slice 253)", async ({
    page,
  }) => {
    // Slice 253 — the cards bind to /policies, /risks and /history. The
    // tenant links no policies or risks, so both show their honest empty
    // states; the one seeded evaluation fills the history.
    await gotoControl(page);
    for (const id of ["policies-section", "risks-section"]) {
      await expect(page.getByTestId(id)).toBeVisible();
      await expect(page.getByTestId(id)).not.toContainText("not on main yet");
      await expect(page.getByTestId(id)).not.toContainText("endpoint pending");
    }
    await expect(page.getByTestId("policies-empty")).toBeVisible();
    await expect(page.getByTestId("risks-empty")).toBeVisible();
    await page.getByTestId("control-tab-history").click();
    const log = page.getByTestId("audit-log-section");
    await expect(log).not.toContainText("not on main yet");
    await expect(log).not.toContainText("endpoint pending");
    await expect(page.getByTestId("audit-log-entry")).toHaveCount(1);
  });

  test("AC-5: freshness clock binds to control state", async ({ page }) => {
    // The clock reads slice 012's /state: the seeded evaluation is
    // `fresh` with its latest evidence an hour old.
    await gotoControl(page, "history");
    await expect(page.getByTestId("freshness-clock")).toBeVisible();
    await expect(page.getByTestId("freshness-since")).not.toHaveText("");
    await expect(page.getByTestId("freshness-status")).toHaveText("fresh");
  });

  test("AC-6: effective-scope panel calls /effective-scope per framework", async ({
    page,
  }) => {
    // One row per distinct framework_version_id in the coverage
    // requirements, each backed by its own effective-scope call.
    const requests: string[] = [];
    page.on("request", (r) => requests.push(r.url()));
    await gotoControl(page);
    const rows = page.getByTestId("effective-scope-row");
    await expect(rows).toHaveCount(2);
    await expect(
      rows.filter({ hasText: "Control Detail Alpha" }),
    ).toContainText("1 cells");
    const beta = rows.filter({ hasText: "Control Detail Beta" });
    await expect(beta).toContainText("0 cells");
    await expect(beta).toContainText("out of scope");
    const scopeCalls = requests.filter((u) =>
      u.includes(`/api/controls/${CONTROL}/effective-scope?framework_version=`),
    );
    expect(scopeCalls.some((u) => u.includes(ALPHA_FV))).toBe(true);
    expect(scopeCalls.some((u) => u.includes(BETA_FV))).toBe(true);
  });

  test("slice 256: coverage column renders both numeric and n/a rows", async ({
    page,
  }) => {
    // The Coverage column renders the backend's per-row value: numeric
    // (two decimals) in scope, "n/a" out of scope, with the bar filled to
    // the coverage percent (0% when out of scope).
    await gotoControl(page);
    const numeric = page.locator(
      '[data-testid="coverage-cell"][data-coverage-state="numeric"]',
    );
    await expect(numeric).toHaveCount(1);
    await expect(numeric).toHaveText(/^\d\.\d{2}$/);
    const oosCell = page.locator(
      '[data-testid="coverage-cell"][data-coverage-state="out-of-scope"]',
    );
    await expect(oosCell).toHaveCount(1);
    await expect(oosCell).toHaveText("n/a");
    await expect(
      coverageRow(page, "CD-BETA-1").getByTestId("strength-bar"),
    ).toHaveAttribute("data-coverage-percent", "0");
    await expect(page.getByTestId("coverage-footer")).toContainText(
      "strength × 30-day effectiveness",
    );
    await expect(page.getByTestId("coverage-row-chevron")).toHaveCount(2);
    await expect(
      page.getByTestId("coverage-row-chevron").first(),
    ).toBeVisible();
  });

  test("slice 482: each coverage row shows a confidence band badge", async ({
    page,
  }) => {
    await gotoControl(page);
    const bands = page.getByTestId("confidence-band");
    await expect(bands).toHaveCount(2);
    // The in-scope numeric row's band agrees with its coverage value.
    const numericRow = page.getByTestId("coverage-row").filter({
      has: page.locator(
        '[data-testid="coverage-cell"][data-coverage-state="numeric"]',
      ),
    });
    await expect(numericRow.getByTestId("confidence-band")).toHaveAttribute(
      "data-band",
      /^(strong|partial|weak)$/,
    );
    // The out-of-scope row has no in-scope evidence: uncovered.
    const oosRow = page.locator(
      '[data-testid="coverage-row"][data-out-of-scope="true"]',
    );
    await expect(oosRow.getByTestId("confidence-band")).toHaveAttribute(
      "data-band",
      "uncovered",
    );
    await expect(page.getByTestId("coverage-footer")).toContainText(
      "confidence band",
    );
  });

  test("slice 255: header action buttons + last-evaluated timestamp", async ({
    page,
  }) => {
    // #2082 retired Run query and Edit YAML; #2140 moved last evaluated
    // into the state strip. An admin on an automated control sees
    // Evaluate now, Download bundle, Upload new version and Request
    // exception, in that order.
    await gotoControl(page);
    const actions = page.getByTestId("control-header-actions");
    await expect(actions).toBeVisible();
    await expect(page.getByTestId("control-action-run-query")).toHaveCount(0);
    await expect(page.getByTestId("control-action-edit-yaml")).toHaveCount(0);
    const evaluate = page.getByTestId("control-action-evaluate");
    const download = page.getByTestId("control-action-download-bundle");
    const upload = page.getByTestId("control-action-upload-toggle");
    const requestException = page.getByTestId(
      "control-action-request-exception",
    );
    for (const button of [evaluate, download, upload, requestException]) {
      await expect(button).toBeVisible();
    }
    // P0-255-3: no `<a href="#">` anywhere in the action well.
    await expect(actions.locator('a[href="#"]')).toHaveCount(0);
    // Request exception links to the real create route.
    await expect(requestException).toHaveAttribute(
      "href",
      `/exceptions/new?control_id=${CONTROL}`,
    );
    // Last evaluated reads the seeded evaluation's relative time.
    await expect(page.getByTestId("control-last-evaluated")).toBeVisible();
    await expect(
      page.getByTestId("control-last-evaluated-value"),
    ).not.toHaveText(/^(—|never)$/);
    // AC-6: the buttons are keyboard-reachable in DOM order.
    await evaluate.focus();
    await expect(evaluate).toBeFocused();
    await page.keyboard.press("Tab");
    await expect(download).toBeFocused();
    await page.keyboard.press("Tab");
    await expect(upload).toBeFocused();
    await page.keyboard.press("Tab");
    await expect(requestException).toBeFocused();
  });

  test("AC-7: out-of-scope framework rows render dashed/greyed", async ({
    page,
  }) => {
    // The control is out of the Beta framework's scope. That row is
    // marked and greyed, NEVER hidden (anti-criterion P0-2).
    await gotoControl(page);
    const oosRow = page.locator(
      '[data-testid="coverage-row"][data-out-of-scope="true"]',
    );
    await expect(oosRow).toHaveCount(1);
    await expect(oosRow).toBeVisible();
    await expect(oosRow).toContainText("CD-BETA-1");
    await expect(oosRow).toContainText("out of FrameworkScope");
    await expect(oosRow).toHaveClass(/border-dashed/);
    await expect(
      page.locator('[data-testid="coverage-row"][data-out-of-scope="false"]'),
    ).toContainText("CD-ALPHA-1");
    // And the matching Framework map edge is dashed.
    await page.getByTestId("control-tab-map").click();
    const oosEdge = page.locator(
      '[data-testid="ucf-viz-requirement"] path[data-out-of-scope="true"]',
    );
    await expect(oosEdge).toHaveCount(1);
    await expect(oosEdge).toHaveAttribute("stroke-dasharray", "3 3");
  });

  test("responsive: layout collapses to a single column at 375px", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 375, height: 812 });
    await gotoControl(page);
    await expect(page.getByTestId("coverage-section")).toBeVisible();
    // The Linked cards share a column below lg: same left edge, stacked.
    const risks = await page.getByTestId("risks-section").boundingBox();
    const policies = await page.getByTestId("policies-section").boundingBox();
    expect(risks).not.toBeNull();
    expect(policies).not.toBeNull();
    expect(Math.abs(risks!.x - policies!.x)).toBeLessThan(1);
    expect(policies!.y).toBeGreaterThan(risks!.y + risks!.height - 1);
    // No section overflows the 375px viewport.
    expect(risks!.x + risks!.width).toBeLessThanOrEqual(375);
    await page.getByTestId("control-tab-history").click();
    await expect(page.getByTestId("freshness-section")).toBeVisible();
  });

  test("auth: a 401 from a bound endpoint bounces to /login", async ({
    page,
  }) => {
    // A cookie that expires mid-session: the coverage read answers 401
    // and the page's effect redirects to /login, naming where it came
    // from. The mock drops the cookie as it answers, as an expiry would;
    // with the cookie still valid, /login sends the browser straight back.
    await page.route(`**/api/controls/${CONTROL}/coverage`, async (route) => {
      await page.context().clearCookies();
      await route.fulfill({
        status: 401,
        contentType: "application/json",
        body: JSON.stringify({ error: "unauthorized" }),
      });
    });
    await page.goto(`/controls/${CONTROL}`);
    await expect(page).toHaveURL(/\/login\?from=/, { timeout: 30_000 });
    expect(new URL(page.url()).searchParams.get("from")).toBe(
      `/controls/${CONTROL}`,
    );
  });

  test("auth: with no session cookie the route redirects to /login", async ({
    page,
  }) => {
    await page.context().clearCookies();
    await page.goto(`/controls/${CONTROL}`);
    await expect(page).toHaveURL(/\/login/, { timeout: 30_000 });
  });
});
