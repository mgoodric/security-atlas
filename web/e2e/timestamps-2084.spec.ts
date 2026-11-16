// #2084 — one date formatter: relative for recent, absolute otherwise,
// exact on hover.
//
// Controls, Evidence and Activity render every timestamp through
// `<Timestamp>`: no raw ISO string reaches the visible text, a stamp
// within 7 days reads relative ("2 days ago"), an older one reads as an
// absolute date, and the tooltip carries the exact instant in UTC.
//
// HERMETIC: route-mocks each page's list BFF (slice 594 convention). The
// recent stamp is computed from the runner's clock and the old one is
// fixed in 2025, so the result does not depend on the day the spec runs.
// Playwright pins locale en-US and timezone UTC (playwright.config.ts).

import type { Page } from "@playwright/test";

import { expect, test } from "./fixtures";

const OLD = "2025-01-15T09:30:00Z";
const OLD_TEXT = "Jan 15, 2025, 9:30 AM";
const OLD_TITLE = "2025-01-15 09:30:00 UTC";

// Any ISO-8601 date-time: the shape a raw wire timestamp would show.
const RAW_ISO = /\d{4}-\d{2}-\d{2}T\d{2}:\d{2}/;

function twoDaysAgo(): string {
  return new Date(Date.now() - 2 * 24 * 60 * 60 * 1000).toISOString();
}

async function json(page: Page, pattern: string | RegExp, body: unknown) {
  await page.route(pattern, async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify(body),
    });
  });
}

async function expectNoRawISO(page: Page) {
  const text = await page.locator("main").innerText();
  expect(text).not.toMatch(RAW_ISO);
}

test.describe("timestamps render through one formatter (#2084)", () => {
  test("/controls: last observed is relative or absolute, never ISO", async ({
    authedPage: page,
  }) => {
    const anchor = (n: string, observed: string) => ({
      id: `00000000-0000-4000-8000-00000000208${n}`,
      scf_id: `SCF-TST-208${n}`,
      family: "Access Control",
      name: `Test anchor 208${n}`,
      description: "Neutral test anchor",
      state: {
        result: "pass",
        freshness_status: "fresh",
        last_observed_at: observed,
        evaluated_at: observed,
      },
      frameworks: [],
    });
    await json(page, "**/api/controls**", {
      anchors: [anchor("1", twoDaysAgo()), anchor("2", OLD)],
    });
    await json(page, "**/api/scopes/cells**", { cells: [] });

    await page.goto("/controls?view=catalog");
    const wrap = page.locator('[data-testid="list-table-wrap"]');
    await expect(wrap.getByText("2 days ago")).toBeVisible({
      timeout: 30_000,
    });
    const old = wrap.locator("time", { hasText: OLD_TEXT });
    await expect(old).toBeVisible();
    await expect(old).toHaveAttribute("title", OLD_TITLE);
    await expectNoRawISO(page);
  });

  test("/evidence: observed is relative or absolute, never ISO", async ({
    authedPage: page,
  }) => {
    const record = (n: string, observed: string) => ({
      evidence_id: `00000000-0000-4000-8000-00000000208${n}`,
      evidence_kind: "test.kind.v1",
      observed_at: observed,
      source: { actor_type: "connector", actor_id: "test-connector" },
      content_hash: "0".repeat(64),
      scope_cell: null,
      result: "pass",
    });
    await json(page, /\/api\/evidence(\?.*)?$/, {
      control_id: "",
      evidence: [record("1", twoDaysAgo()), record("2", OLD)],
      count: 2,
      total: 2,
      next_cursor: "",
    });

    await page.goto("/evidence");
    const cells = page
      .locator('[data-testid="list-table-wrap"]')
      .getByTestId("evidence-row-observed-at");
    await expect(cells).toHaveText(["2 days ago", OLD_TEXT], {
      timeout: 30_000,
    });
    await expect(cells.nth(1)).toHaveAttribute("title", OLD_TITLE);
    await expectNoRawISO(page);
  });

  test("/activity: occurred is relative or absolute, never ISO", async ({
    authedPage: page,
  }) => {
    const entry = (n: string, occurred: string) => ({
      occurred_at: occurred,
      actor_id: "test-actor",
      actor_name: null,
      tenant_id: "00000000-0000-4000-8000-000000000001",
      kind: "control_change",
      target_type: "control",
      target_id: `test-target-${n}`,
      action: "update",
      row_id: `test-row-208${n}`,
      payload_json: {},
    });
    await json(page, /\/api\/activity(\?.*)?$/, {
      entries: [entry("1", twoDaysAgo()), entry("2", OLD)],
    });

    await page.goto("/activity");
    const rows = page.getByTestId("activity-row");
    await expect(rows).toHaveCount(2, { timeout: 30_000 });
    await expect(rows.nth(0).locator("time")).toHaveText("2 days ago");
    await expect(rows.nth(1).locator("time")).toHaveText(OLD_TEXT);
    await expect(rows.nth(1).locator("time")).toHaveAttribute(
      "title",
      OLD_TITLE,
    );
    await expectNoRawISO(page);
  });
});
