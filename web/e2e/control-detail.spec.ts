// Slice 041 — Playwright E2E for the control detail view.
// Slice 112 — soak gate satisfied without counting the 2026-06-29 to
// 2026-07-24 CI-disabled gap: slice 111 merged, and post-gap real
// `Frontend · Playwright e2e` jobs had >=5 clean successes. This slice
// extends `fixtures/e2e/control-detail.sql` to FULL coverage and enables
// the deferred control-detail assertions against the seed harness.
//
// Run locally:
//   cd web
//   npx playwright install chromium     # once per machine
//   npx playwright test e2e/control-detail.spec.ts

import type { Page } from "@playwright/test";

import { expect, seeded, test } from "./fixtures";
import { seedFromFixture } from "./seed";

async function gotoControlDetail(page: Page) {
  const coverageResp = page.waitForResponse(
    (r) =>
      r.url().includes(`/api/controls/${seeded.controlId}/coverage`) &&
      r.status() === 200,
    { timeout: 30_000 },
  );
  await page.goto(`/controls/${seeded.controlId}`);
  await coverageResp;
}

test.describe("control detail view", () => {
  test.beforeAll(() => {
    seedFromFixture("control-detail");
  });

  test("AC-1: /controls/:id renders the full detail layout", async ({
    authedPage: page,
  }) => {
    await gotoControlDetail(page);
    await expect(page.getByTestId("control-detail")).toBeVisible();
    await expect(page.getByTestId("control-title")).toBeVisible();
    await expect(page.getByTestId("scf-anchor-pill")).toBeVisible();
    await expect(page.getByTestId("lifecycle-badge")).toBeVisible();
    await expect(page.getByTestId("kpi-strip")).toBeVisible();
    await expect(page.getByTestId("coverage-section")).toBeVisible();
    await expect(page.getByTestId("ucf-viz-section")).toBeVisible();
    await expect(page.getByTestId("evidence-stream-section")).toBeVisible();
    await expect(page.getByTestId("freshness-section")).toBeVisible();
    await expect(page.getByTestId("effective-scope-section")).toBeVisible();
    await expect(page.getByTestId("policies-section")).toBeVisible();
    await expect(page.getByTestId("risks-section")).toBeVisible();
    await expect(page.getByTestId("audit-log-section")).toBeVisible();
  });

  test("AC-2: coverage table shows STRM types + strengths per row", async ({
    authedPage: page,
  }) => {
    await gotoControlDetail(page);
    await expect(page.getByTestId("coverage-table")).toBeVisible();
    const rows = page.getByTestId("coverage-row");
    await expect(rows.first()).toBeVisible();
    await expect(rows.first().locator("[data-strm]")).toBeVisible();
    await expect(rows.first().getByTestId("strength-bar")).toBeVisible();
  });

  test("AC-3: UCF mini-viz renders control -> anchor -> requirements", async ({
    authedPage: page,
  }) => {
    await gotoControlDetail(page);
    await expect(page.getByTestId("ucf-mini-viz")).toBeVisible();
    await expect(page.getByTestId("ucf-viz-control")).toBeVisible();
    await expect(page.getByTestId("ucf-viz-anchor")).toBeVisible();
    await expect(page.getByTestId("ucf-viz-requirement").first()).toBeVisible();
  });

  test("AC-4: evidence stream is bound and endpoint-pending copy is gone", async ({
    authedPage: page,
  }) => {
    await gotoControlDetail(page);
    await expect(page.getByTestId("evidence-stream-section")).toBeVisible();
    await expect(page.getByTestId("evidence-stream-placeholder")).toHaveCount(
      0,
    );
    const list = page.getByTestId("evidence-stream-list");
    const empty = page.getByTestId("evidence-stream-empty");
    await expect(list.or(empty)).toBeVisible();
    await expect(page.getByTestId("evidence-stream-section")).not.toContainText(
      "not yet wired",
    );
    await expect(page.getByTestId("evidence-stream-section")).not.toContainText(
      "not on main yet",
    );
    if (await empty.isVisible()) {
      await expect(empty).toContainText("No evidence records");
      await expect(empty).toContainText("in the last 30 days");
    }
  });

  test("AC-4-b: right-rail Policies / Risks / Audit-log render real backings", async ({
    authedPage: page,
  }) => {
    await gotoControlDetail(page);
    for (const id of [
      "policies-section",
      "risks-section",
      "audit-log-section",
    ]) {
      await expect(page.getByTestId(id)).toBeVisible();
      await expect(page.getByTestId(id)).not.toContainText("not on main yet");
      await expect(page.getByTestId(id)).not.toContainText("endpoint pending");
    }
    await expect(page.getByTestId("kpi-strip")).not.toContainText(
      "evidence-list endpoint pending",
    );
  });

  test("AC-5: freshness clock binds to control state", async ({
    authedPage: page,
  }) => {
    await gotoControlDetail(page);
    await expect(page.getByTestId("freshness-clock")).toBeVisible();
    await expect(page.getByTestId("freshness-since")).toBeVisible();
    await expect(page.getByTestId("freshness-status")).toBeVisible();
  });

  test("AC-6: effective-scope panel calls /effective-scope per framework", async ({
    authedPage: page,
  }) => {
    const requests: string[] = [];
    page.on("request", (r) => requests.push(r.url()));
    await gotoControlDetail(page);
    await expect(page.getByTestId("effective-scope-row").first()).toBeVisible();
    expect(
      requests.filter((u) => u.includes("/effective-scope?framework_version="))
        .length,
    ).toBeGreaterThan(0);
  });

  test("slice 256: coverage column renders both numeric and n/a rows", async ({
    authedPage: page,
  }) => {
    await gotoControlDetail(page);
    await expect(page.getByTestId("coverage-table")).toBeVisible();
    const numeric = page.locator(
      '[data-testid="coverage-cell"][data-coverage-state="numeric"]',
    );
    await expect(numeric.first()).toBeVisible();
    await expect(numeric.first()).toHaveText(/^\d\.\d{2}$/);
    const oosCell = page.locator(
      '[data-testid="coverage-cell"][data-coverage-state="out-of-scope"]',
    );
    await expect(oosCell.first()).toBeVisible();
    await expect(oosCell.first()).toHaveText("n/a");
    await expect(page.getByTestId("coverage-footer")).toContainText(
      "strength × 30-day effectiveness",
    );
    await expect(
      page.getByTestId("coverage-row-chevron").first(),
    ).toBeVisible();
  });

  test("slice 482: each coverage row shows a confidence band badge", async ({
    authedPage: page,
  }) => {
    await gotoControlDetail(page);
    await expect(page.getByTestId("coverage-table")).toBeVisible();
    const bands = page.getByTestId("confidence-band");
    await expect(bands.first()).toBeVisible();
    await expect(bands.first()).toHaveAttribute(
      "data-band",
      /^(strong|partial|weak|uncovered)$/,
    );
    const numericRow = page.locator('[data-testid="coverage-row"]').filter({
      has: page.locator(
        '[data-testid="coverage-cell"][data-coverage-state="numeric"]',
      ),
    });
    await expect(
      numericRow.first().getByTestId("confidence-band"),
    ).toHaveAttribute("data-band", /^(strong|partial|weak)$/);
    const oosRow = page.locator(
      '[data-testid="coverage-row"][data-out-of-scope="true"]',
    );
    await expect(oosRow.first().getByTestId("confidence-band")).toHaveAttribute(
      "data-band",
      "uncovered",
    );
    await expect(page.getByTestId("coverage-footer")).toContainText(
      "confidence band",
    );
  });

  test("slice 255: header action buttons + last-evaluated timestamp", async ({
    authedPage: page,
  }) => {
    await gotoControlDetail(page);
    await expect(page.getByTestId("control-header-actions")).toBeVisible();

    const runQuery = page.getByTestId("control-action-run-query");
    const editYaml = page.getByTestId("control-action-edit-yaml");
    const requestException = page.getByTestId(
      "control-action-request-exception",
    );
    await expect(runQuery).toBeVisible();
    await expect(editYaml).toBeVisible();
    await expect(requestException).toBeVisible();
    await expect(runQuery).toBeDisabled();
    await expect(editYaml).toBeDisabled();
    await expect(runQuery).toHaveAttribute(
      "aria-label",
      /Rule-based control evaluation is not available yet/,
    );
    await expect(editYaml).toHaveAttribute(
      "aria-label",
      /In-app control-text editing is not available yet/,
    );
    await expect(
      page.getByTestId("control-header-actions").locator('a[href="#"]'),
    ).toHaveCount(0);
    await expect(requestException).toHaveAttribute(
      "href",
      new RegExp(`^/exceptions\\?control_id=${seeded.controlId}`),
    );
    await expect(page.getByTestId("control-last-evaluated")).toBeVisible();
    await expect(
      page.getByTestId("control-last-evaluated-value"),
    ).toBeVisible();
    await runQuery.focus();
    await expect(runQuery).toBeFocused();
    await page.keyboard.press("Tab");
    await expect(editYaml).toBeFocused();
    await page.keyboard.press("Tab");
    await expect(requestException).toBeFocused();
  });

  test("AC-7: out-of-scope framework rows render dashed/greyed", async ({
    authedPage: page,
  }) => {
    await gotoControlDetail(page);
    const oosRow = page.locator(
      '[data-testid="coverage-row"][data-out-of-scope="true"]',
    );
    await expect(oosRow.first()).toBeVisible();
    const oosEdge = page.locator(
      '[data-testid="ucf-viz-requirement"] [data-out-of-scope="true"]',
    );
    await expect(oosEdge.first()).toBeVisible();
  });

  test("responsive: layout collapses to a single column at 375px", async ({
    authedPage: page,
  }) => {
    await page.setViewportSize({ width: 375, height: 812 });
    await gotoControlDetail(page);
    await expect(page.getByTestId("coverage-section")).toBeVisible();
    await expect(page.getByTestId("freshness-section")).toBeVisible();
  });

  test("auth: a 401 from a bound endpoint bounces to /login", async ({
    page,
  }) => {
    await page.goto(`/controls/${seeded.controlId}`);
    await expect(page).toHaveURL(/\/login/);
  });
});
