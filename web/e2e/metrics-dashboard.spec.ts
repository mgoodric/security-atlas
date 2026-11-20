// Slice 097 — Playwright E2E for the metrics dashboard + cascade-tree
// view.
//
// Seeded by fixtures/e2e/metrics-dashboard.sql: a tenant of the spec's
// own with an observation series + target for the board metric
// `audit_readiness_score` and two manual inputs + a target for its
// program-level child `framework_coverage_pct` (whose own child is
// `walkthrough_completion_rate`). The catalog and cascade edges are
// global, loaded by migration. The spec signs in to the tenant as its
// admin user through /v1/test/issue-jwt. #2755 restored the assertions
// slice 097 left commented out behind the slice-082 quarantine, against
// the metric ids the catalog ships (the slice doc's `audit_readiness` /
// `per_framework_coverage` were placeholders).
//
// Run locally (against a running platform):
//   cd web
//   npx playwright install chromium     # once per machine
//   npx playwright test e2e/metrics-dashboard.spec.ts
//
// AC mapping:
//   AC-14 narrative: navigate to /dashboards/metrics, expand the
//   audit-readiness cascade, click into framework coverage, submit a
//   manual input, assert the new value appears in the series.

import { expect, test } from "./fixtures";
import { seedFromFixture } from "./seed";
import { signInToTenant } from "./tenant-session";

// Mirror the tenant and admin user in fixtures/e2e/metrics-dashboard.sql.
const TENANT = "00000000-0000-0000-0000-2755d0000001";
const ADMIN_USER = "44444444-4444-4444-4444-2755d0000011";

const BOARD = "audit_readiness_score";
const CHILD = "framework_coverage_pct";

test.describe("metrics dashboard view", () => {
  test.beforeAll(() => {
    seedFromFixture("metrics-dashboard");
  });

  test.beforeEach(async ({ page }) => {
    await signInToTenant(page, {
      tenantId: TENANT,
      userId: ADMIN_USER,
      roles: ["admin"],
      superAdmin: true,
    });
  });

  test("AC-1: /dashboards/metrics renders the board-level summary panel", async ({
    page,
  }) => {
    await page.goto("/dashboards/metrics");
    await expect(page.getByTestId("metrics-dashboard")).toBeVisible({
      timeout: 30_000,
    });
    await expect(page.getByTestId(`board-metric-${BOARD}`)).toBeVisible();
    await expect(page.getByTestId("metrics-dashboard-error")).toHaveCount(0);
  });

  test("AC-2/3: each card shows value, target, sparkline, and threshold badge", async ({
    page,
  }) => {
    await page.goto("/dashboards/metrics");
    const card = page.getByTestId(`board-metric-${BOARD}`);
    // Latest seeded observation is 0.80; the target is 0.90.
    await expect(card.getByTestId(`board-metric-${BOARD}-value`)).toHaveText(
      "80.0%",
      { timeout: 30_000 },
    );
    await expect(card).toContainText("target 90.0%");
    await expect(
      card.getByTestId(`board-metric-${BOARD}-sparkline`),
    ).toBeVisible();
    const badge = card.getByTestId(`board-metric-${BOARD}-badge`);
    await expect(badge).toBeVisible();
    const color = await badge.getAttribute("data-threshold-color");
    expect(["green", "yellow", "red", "neutral"]).toContain(color);
    // 0.80 sits between the warning (0.75) and the target: not neutral.
    expect(color).not.toBe("neutral");
  });

  test("AC-4: empty-state copy when no observations exist", async ({
    page,
  }) => {
    // Override the observations BFF for one card to return an empty
    // page; the card renders its empty-state badge instead of the
    // toggle button.
    await page.route(`**/api/metrics/${BOARD}/observations**`, (r) =>
      r.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ observations: [], count: 0 }),
      }),
    );
    await page.goto("/dashboards/metrics");
    await expect(page.getByTestId(`board-metric-${BOARD}-empty`)).toContainText(
      "No data yet",
      { timeout: 30_000 },
    );
    await expect(page.getByTestId(`board-metric-${BOARD}-toggle`)).toHaveCount(
      0,
    );
  });

  test("AC-5/6/7: clicking a card expands the cascade tree and navigation works", async ({
    page,
  }) => {
    await page.goto("/dashboards/metrics");
    await page
      .getByTestId(`board-metric-${BOARD}-toggle`)
      .click({ timeout: 30_000 });
    const tree = page.getByTestId(`cascade-${BOARD}`);
    await expect(tree).toBeVisible();
    // The framework-coverage child row is present, one level below the
    // board root.
    const root = tree.getByTestId(`cascade-row-${BOARD}`);
    const row = root.getByTestId(`cascade-row-${CHILD}`);
    await expect(row).toBeVisible();
    const rootDepth = Number(await root.getAttribute("data-depth"));
    await expect(row).toHaveAttribute("data-depth", String(rootDepth + 1));
    // Clicking it navigates to the detail page.
    await row.getByRole("link").first().click();
    await expect(page).toHaveURL(new RegExp(`/dashboards/metrics/${CHILD}$`));
    await expect(page.getByTestId("metric-detail")).toBeVisible();
  });

  test("AC-8: X-Cascade-Truncated header surfaces a 'depth limit reached' hint", async ({
    page,
  }) => {
    // Force the BFF to claim truncation, regardless of the real
    // catalog depth.
    await page.route("**/api/metrics/cascade**", async (r) => {
      const res = await r.fetch();
      const body = (await res.json()) as Record<string, unknown>;
      body.truncated = true;
      await r.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(body),
      });
    });
    await page.goto("/dashboards/metrics");
    await page
      .getByTestId(`board-metric-${BOARD}-toggle`)
      .click({ timeout: 30_000 });
    await expect(page.getByTestId(`cascade-${BOARD}-truncated`)).toContainText(
      "Depth limit reached",
    );
  });

  test("AC-9/10: per-metric detail renders definition, parents/children, and line chart", async ({
    page,
  }) => {
    await page.goto(`/dashboards/metrics/${CHILD}`);
    await expect(page.getByTestId("metric-detail")).toBeVisible({
      timeout: 30_000,
    });
    await expect(page.getByTestId("metric-detail-chart-svg")).toBeVisible();
    await expect(page.getByTestId("metric-detail-parents")).toContainText(
      "Audit readiness",
    );
    await expect(page.getByTestId("metric-detail-children")).toContainText(
      "Walkthrough",
    );
    // A target is set, so the three overlay lines render.
    await expect(
      page.getByTestId("metric-detail-chart-svg-target-line"),
    ).toBeAttached();
    await expect(
      page.getByTestId("metric-detail-chart-svg-warning-line"),
    ).toBeAttached();
    await expect(
      page.getByTestId("metric-detail-chart-svg-critical-line"),
    ).toBeAttached();
  });

  test("AC-11: admin-gated manual-input modal posts and series re-fetches", async ({
    page,
  }) => {
    await page.goto(`/dashboards/metrics/${CHILD}`);
    const rows = page
      .getByTestId("metric-detail-audit-trail")
      .getByTestId(/^audit-row-/);
    await expect(rows.first()).toBeVisible({ timeout: 30_000 });
    const before = await rows.count();
    // A value of this run's own, so a parallel run's row cannot satisfy
    // the assertion. The card formats percent fractions to one decimal.
    const tenths = 700 + Math.floor(Math.random() * 200);
    const value = (tenths / 1000).toFixed(3);
    const shown = `${(tenths / 10).toFixed(1)}%`;

    await page.getByTestId(`manual-input-trigger-${CHILD}`).click();
    await expect(page.getByTestId(`manual-input-modal-${CHILD}`)).toBeVisible();
    await page.getByTestId(`manual-input-value-${CHILD}`).fill(value);
    await page.getByTestId(`manual-input-notes-${CHILD}`).fill("e2e smoke");
    await page.getByTestId(`manual-input-submit-${CHILD}`).click();
    await expect(page.getByTestId(`manual-input-modal-${CHILD}`)).toHaveCount(
      0,
    );
    // After submit the series re-fetches without a reload: the audit
    // trail gains the new row (it shows value + source; notes are not
    // rendered). Rows only accumulate, so parallel runs keep `>`.
    await expect
      .poll(() => rows.count(), { timeout: 15_000 })
      .toBeGreaterThan(before);
    await expect(
      rows.filter({ hasText: shown }).filter({ hasText: "manual:" }),
    ).not.toHaveCount(0);
  });

  test("AC-11 negative: non-admin sees the audit-trail but no submit trigger", async ({
    page,
  }) => {
    // Override the session probe so /api/admin/me returns is_admin=false.
    await page.route("**/api/admin/me", (r) =>
      r.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ is_admin: false }),
      }),
    );
    await page.goto(`/dashboards/metrics/${CHILD}`);
    await expect(page.getByTestId("metric-detail")).toBeVisible({
      timeout: 30_000,
    });
    await expect(page.getByTestId("metric-detail-audit-trail")).toBeVisible();
    await expect(page.getByTestId("manual-input-admin-only")).toBeVisible();
    await expect(page.getByTestId(/^manual-input-trigger-/)).toHaveCount(0);
  });

  test("AC-12: audit-trail panel lists rows where source LIKE manual:%", async ({
    page,
  }) => {
    await page.goto(`/dashboards/metrics/${CHILD}`);
    const trail = page.getByTestId("metric-detail-audit-trail");
    await expect(trail).toBeVisible({ timeout: 30_000 });
    // The fixture's two manual inputs replicate into observations with a
    // `manual:<user>` source.
    const rows = trail.getByTestId(/^audit-row-/);
    await expect(rows.first()).toBeVisible();
    expect(await rows.count()).toBeGreaterThanOrEqual(2);
    for (const source of await rows.locator("code").allTextContents()) {
      expect(source).toMatch(/^manual:/);
    }
  });

  test("auth: 401 from /api/metrics bounces to /login", async ({ page }) => {
    await page.context().clearCookies();
    await page.goto("/dashboards/metrics");
    await expect(page).toHaveURL(/\/login/);
  });
});
