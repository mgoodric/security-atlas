// Slice 097 — Playwright E2E for the metrics dashboard + cascade-tree
// view.
//
// Runner status (post-slice-069, verified 2026-05-15 by slice 071
// audit): Playwright IS installed in `web/` (`@playwright/test` in
// devDeps; `web/playwright.config.ts` present; CI runs `Frontend ·
// Playwright e2e`). The job is currently quarantined per slice 079
// because un-shimmed specs reference seed-data preconditions the
// docker-compose bring-up does not yet establish. Slice 082
// (`Playwright e2e seed-data harness`, status `not-ready`) is the fix.
// When 082 lands, the quarantine drops and the un-commented
// assertions below become the gate.
//
// Run locally (against a running platform):
//   cd web
//   npx playwright install chromium     # once per machine
//   npx playwright test e2e/metrics-dashboard.spec.ts
//
// Pre-conditions the seed-data harness (slice 082) must establish
// before the commented assertions are turned on:
//   - PLATFORM_BASE_URL points at a running platform instance
//   - TEST_BEARER carries a credential with the `admin` role so the
//     manual-input modal trigger is visible
//   - The slice-076 catalog seeded with ~40 metrics + at least one
//     manual_input metric ("Per-framework coverage" or similar)
//   - At least one parent → child → grandchild cascade edge (Audit
//     readiness → Per-framework coverage → IAM freshness is the
//     canonical example)
//   - >= 1 observation per metric in the last 90 days so sparklines
//     and the line chart have data
//
// AC mapping:
//   AC-14 narrative: navigate to /dashboards/metrics, expand the
//   audit-readiness cascade, click into per-framework-coverage,
//   submit a manual input, assert the new value appears in the
//   series.

import { test } from "@playwright/test";

test.describe("metrics dashboard view", () => {
  test("AC-1: /dashboards/metrics renders the board-level summary panel", async () => {
    // 1. Sign in.
    //    await page.goto("/login");
    //    await page.fill('input[name="token"]', process.env.TEST_BEARER!);
    //    await page.click("button[type=submit]");
    // 2. Visit the dashboard.
    //    await page.goto("/dashboards/metrics");
    // 3. The page header + at least one board-metric card are visible.
    //    await expect(page.getByTestId("metrics-dashboard")).toBeVisible();
    //    await expect(
    //      page.getByTestId(/^board-metric-/).first(),
    //    ).toBeVisible();
  });

  test("AC-2/3: each card shows value, target, sparkline, and threshold badge", async () => {
    //    await page.goto("/dashboards/metrics");
    //    const card = page
    //      .getByTestId("board-metric-audit_readiness");
    //    await expect(card.getByTestId(/value$/)).toBeVisible();
    //    await expect(card.getByTestId(/sparkline$/)).toBeVisible();
    //    const badge = card.getByTestId(/badge$/);
    //    await expect(badge).toBeVisible();
    //    // badge data-threshold-color is one of green/yellow/red/neutral
    //    const color = await badge.getAttribute("data-threshold-color");
    //    expect(["green", "yellow", "red", "neutral"]).toContain(color);
  });

  test("AC-4: empty-state copy when no observations exist", async () => {
    // Override the observations BFF for one card to return an empty
    // page; the card renders its empty-state badge instead of the
    // toggle button.
    //    await page.route("**/api/metrics/audit_readiness/observations**", (r) =>
    //      r.fulfill({
    //        status: 200,
    //        contentType: "application/json",
    //        body: JSON.stringify({ observations: [], count: 0 }),
    //      }),
    //    );
    //    await page.goto("/dashboards/metrics");
    //    await expect(
    //      page.getByTestId("board-metric-audit_readiness-empty"),
    //    ).toContainText("No data yet");
  });

  test("AC-5/6/7: clicking a card expands the cascade tree and navigation works", async () => {
    //    await page.goto("/dashboards/metrics");
    //    await page.getByTestId("board-metric-audit_readiness-toggle").click();
    //    await expect(page.getByTestId("cascade-audit_readiness")).toBeVisible();
    //    // The per-framework-coverage child row is present:
    //    const row = page.getByTestId("cascade-row-per_framework_coverage");
    //    await expect(row).toBeVisible();
    //    // Clicking it navigates to the detail page.
    //    await row.click();
    //    await expect(page).toHaveURL(/\/dashboards\/metrics\/per_framework_coverage$/);
  });

  test("AC-8: X-Cascade-Truncated header surfaces a 'depth limit reached' hint", async () => {
    // Force the BFF to claim truncation, regardless of the real
    // catalog depth.
    //    await page.route("**/api/metrics/cascade**", async (r) => {
    //      const res = await r.fetch();
    //      const body = await res.json();
    //      body.truncated = true;
    //      await r.fulfill({
    //        status: 200,
    //        contentType: "application/json",
    //        body: JSON.stringify(body),
    //      });
    //    });
    //    await page.goto("/dashboards/metrics");
    //    await page.getByTestId("board-metric-audit_readiness-toggle").click();
    //    await expect(
    //      page.getByTestId("cascade-audit_readiness-truncated"),
    //    ).toContainText("Depth limit reached");
  });

  test("AC-9/10: per-metric detail renders definition, parents/children, and line chart", async () => {
    //    await page.goto("/dashboards/metrics/per_framework_coverage");
    //    await expect(page.getByTestId("metric-detail")).toBeVisible();
    //    await expect(page.getByTestId("metric-detail-chart-svg")).toBeVisible();
    //    await expect(page.getByTestId("metric-detail-parents")).toBeVisible();
    //    await expect(page.getByTestId("metric-detail-children")).toBeVisible();
    //    // When a target is set, the three overlay lines render.
    //    await expect(page.getByTestId("metric-detail-chart-svg-target-line")).toBeVisible();
  });

  test("AC-11: admin-gated manual-input modal posts and series re-fetches", async () => {
    // Default fixture is admin, so the trigger is visible.
    //    await page.goto("/dashboards/metrics/per_framework_coverage");
    //    await page.getByTestId(/^manual-input-trigger-/).click();
    //    await expect(page.getByTestId(/^manual-input-modal-/)).toBeVisible();
    //    await page.getByTestId(/^manual-input-value-/).fill("0.87");
    //    await page.getByTestId(/^manual-input-notes-/).fill("e2e smoke");
    //    await page.getByTestId(/^manual-input-submit-/).click();
    //    // After submit, the audit-trail panel includes the new row.
    //    await expect(page.getByTestId("metric-detail-audit-trail")).toContainText("e2e smoke");
  });

  test("AC-11 negative: non-admin sees the audit-trail but no submit trigger", async () => {
    // Override the session probe so /api/admin/me returns is_admin=false.
    //    await page.route("**/api/admin/me", (r) =>
    //      r.fulfill({
    //        status: 200,
    //        contentType: "application/json",
    //        body: JSON.stringify({ is_admin: false }),
    //      }),
    //    );
    //    await page.goto("/dashboards/metrics/per_framework_coverage");
    //    await expect(page.getByTestId("metric-detail")).toBeVisible();
    //    await expect(page.getByTestId(/^manual-input-trigger-/)).toHaveCount(0);
    //    await expect(page.getByTestId("manual-input-admin-only")).toBeVisible();
  });

  test("AC-12: audit-trail panel lists rows where source LIKE manual:%", async () => {
    //    await page.goto("/dashboards/metrics/per_framework_coverage");
    //    await expect(page.getByTestId("metric-detail-audit-trail")).toBeVisible();
    //    // At least one manual:* row if the seed harness wrote one.
    //    const rows = page.getByTestId(/^audit-row-/);
    //    await expect(rows.first()).toBeVisible();
  });

  test("auth: 401 from /api/metrics bounces to /login", async () => {
    //    await page.context().clearCookies();
    //    await page.goto("/dashboards/metrics");
    //    await expect(page).toHaveURL(/\/login/);
  });
});
