// #2093 — Activity is the one audit-log page.
//
//   - rows read as sentences, grouped by day, payload behind an expander;
//   - a null payload (#2379) says "Details not available";
//   - the "Raw ledger" toggle shows the table on the same page;
//   - a filter saved from the old /audit-log page still applies;
//   - /audit-log and Admin › Audit log redirect here;
//   - the export bar stays reachable for a caller the export admits.
//
// HERMETIC: route-mocks `/api/activity` and `/api/people` (slice 594
// convention), so the assertions do not depend on seeded audit rows. The
// harness signs in as a super-admin (global-setup), which the export
// endpoint admits. Every literal is a neutral `test-*` value (slice 069
// P0-A9).

import type { Page, Request } from "@playwright/test";

import { expectNoSeriousAxe, seriousAxeViolations } from "./axe-gate";
import { expect, test } from "./fixtures";

const ACTOR = "00000000-0000-4000-8000-000000002093";
const TENANT = "00000000-0000-4000-8000-000000000001";
const OLD = "2025-01-15T09:30:00Z";

function hoursAgo(h: number): string {
  return new Date(Date.now() - h * 60 * 60 * 1000).toISOString();
}

// The recent rows sit a few minutes back so they stay on the runner's
// local "today" unless the spec runs in the first minutes after midnight.
function entries() {
  return [
    {
      occurred_at: hoursAgo(0.05),
      actor_id: ACTOR,
      actor_name: "Test Person",
      tenant_id: TENANT,
      kind: "exception",
      target_type: "exception",
      target_id: "00000000-0000-4000-8000-0000000020e1",
      action: "approved",
      row_id: "test-row-1",
      payload_json: { note: "test-payload-visible" },
    },
    {
      occurred_at: hoursAgo(0.1),
      actor_id: ACTOR,
      actor_name: "Test Person",
      tenant_id: TENANT,
      kind: "risk",
      target_type: "risk",
      target_id: "00000000-0000-4000-8000-0000000020e2",
      action: "fields_changed",
      row_id: "test-row-2",
      // #2379: the caller cannot read this row's source.
      payload_json: null,
    },
    {
      occurred_at: OLD,
      actor_id: ACTOR,
      actor_name: "Test Person",
      tenant_id: TENANT,
      kind: "framework_scope",
      target_type: "framework_scope",
      target_id: "00000000-0000-4000-8000-0000000020e3",
      action: "submit",
      row_id: "test-row-3",
      payload_json: {},
    },
  ];
}

async function mockActivity(page: Page): Promise<Request[]> {
  const seen: Request[] = [];
  await page.route(/\/api\/activity(\?.*)?$/, async (route) => {
    seen.push(route.request());
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ entries: entries() }),
    });
  });
  await page.route(/\/api\/people(\?.*)?$/, async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        users: [{ id: ACTOR, display_name: "Test Person", status: "active" }],
        api_keys: [],
      }),
    });
  });
  return seen;
}

test.describe("/activity (#2093)", () => {
  test("rows read as sentences grouped by day, with the payload behind an expander", async ({
    authedPage: page,
  }) => {
    await mockActivity(page);
    await page.goto("/activity");

    const days = page.getByTestId("activity-day");
    await expect(days).toHaveCount(2, { timeout: 30_000 });
    await expect(days.nth(0).getByTestId("activity-day-heading")).toHaveText(
      "Today",
    );
    await expect(days.nth(0).getByTestId("activity-row")).toHaveCount(2);
    await expect(days.nth(1).getByTestId("activity-row")).toHaveCount(1);

    const sentences = page.getByTestId("activity-sentence");
    await expect(sentences.nth(0)).toHaveText(
      /^Test Person approved exception 00000000…$/,
    );
    await expect(sentences.nth(1)).toHaveText(
      /^Test Person edited risk 00000000…$/,
    );
    await expect(sentences.nth(2)).toHaveText(
      /^Test Person submitted framework scope 00000000…$/,
    );
    // Relative time for recent rows.
    await expect(
      days.nth(0).getByTestId("activity-row").nth(0).locator("time"),
    ).toHaveText(/ago|now/);

    // One click flips aria-expanded exactly once (#2397).
    const first = page.getByTestId("activity-row").nth(0);
    const toggle = first.getByTestId("activity-row-toggle");
    await expect(toggle).toHaveAttribute("aria-expanded", "false");
    await toggle.click();
    await expect(toggle).toHaveAttribute("aria-expanded", "true");
    await expect(first.getByTestId("activity-row-payload")).toContainText(
      "test-payload-visible",
    );

    // A withheld payload says so rather than rendering "null".
    const second = page.getByTestId("activity-row").nth(1);
    await second.getByTestId("activity-row-toggle").click();
    await expect(second.getByTestId("activity-payload-unavailable")).toHaveText(
      "Details not available.",
    );

    // Expanded rows are swept too (#2397).
    expectNoSeriousAxe(
      await seriousAxeViolations(page),
      "/activity with rows expanded",
    );
  });

  test("the raw ledger toggle shows the table on the same page", async ({
    authedPage: page,
  }) => {
    await mockActivity(page);
    await page.goto("/activity");
    await expect(page.getByTestId("activity-row")).toHaveCount(3, {
      timeout: 30_000,
    });

    await page.getByTestId("activity-view-ledger").click();
    await expect(page).toHaveURL(/[?&]view=ledger\b/);
    await expect(page.getByTestId("activity-view-ledger")).toHaveAttribute(
      "aria-pressed",
      "true",
    );
    const table = page.getByTestId("activity-table-wrap");
    await expect(table).toBeVisible();
    await expect(page.getByTestId("activity-ledger-row")).toHaveCount(3);
    await expect(table.getByTestId("activity-row-kind").nth(0)).toHaveText(
      "exception",
    );
    await expect(table).toContainText("00000000-0000-4000-8000-0000000020e1");

    const row = page.getByTestId("activity-ledger-row").nth(0);
    await row.getByTestId("activity-row-toggle").click();
    await expect(page.getByTestId("activity-row-payload")).toContainText(
      "test-payload-visible",
    );
    expectNoSeriousAxe(
      await seriousAxeViolations(page),
      "/activity raw ledger with a row expanded",
    );

    await page.getByTestId("activity-view-sentences").click();
    await expect(page).not.toHaveURL(/view=ledger/);
    await expect(page.getByTestId("activity-sentences")).toBeVisible();
  });

  test("a filter saved from /audit-log applies on Activity", async ({
    authedPage: page,
  }) => {
    const seen = await mockActivity(page);
    const from = "2025-01-01T00:00:00.000Z";
    const to = "2025-02-01T00:00:00.000Z";
    const saved = `/audit-log?from=${from}&to=${to}&actor=me&kind=exception,risk`;

    await page.goto(saved);
    await expect(page).toHaveURL(/\/activity\?/);
    const url = new URL(page.url());
    expect(url.searchParams.get("from")).toBe(from);
    expect(url.searchParams.get("kind")).toBe("exception,risk");

    await expect(
      page.getByTestId("activity-kind-chip-exception"),
    ).toHaveAttribute("aria-pressed", "true");
    await expect(page.getByTestId("activity-kind-chip-risk")).toHaveAttribute(
      "aria-pressed",
      "true",
    );
    await expect(page.getByTestId("activity-actor-me-label")).toBeVisible();
    await expect(page.getByTestId("activity-from")).toHaveValue("2025-01-01");

    await expect
      .poll(() => seen.length, { timeout: 30_000 })
      .toBeGreaterThan(0);
    const q = new URL(seen[seen.length - 1].url()).searchParams;
    expect(q.get("kind")).toBe("exception,risk");
    expect(q.get("from")).toBe(from);
    expect(q.get("actor")).toBe("me");
  });

  test("/audit-log redirects to Activity", async ({ authedPage: page }) => {
    await mockActivity(page);
    await page.goto("/audit-log");
    await expect(page).toHaveURL(/\/activity$/);
    await expect(page.getByTestId("activity-page")).toBeVisible();
  });

  test("Admin › Audit log redirects to Activity", async ({
    authedPage: page,
  }) => {
    await mockActivity(page);
    await page.goto("/admin/audit");
    await expect(page).toHaveURL(/\/activity$/);
    await expect(page.getByTestId("activity-page")).toBeVisible();
  });

  test("the export stays reachable for a caller the export endpoint admits", async ({
    authedPage: page,
  }) => {
    await mockActivity(page);
    await page.goto("/activity?kind=exception");
    const bar = page.getByTestId("activity-export-bar");
    await expect(bar).toBeVisible({ timeout: 30_000 });
    for (const format of ["csv", "json", "xlsx"]) {
      const href = await bar
        .getByTestId(`activity-export-${format}`)
        .getAttribute("href");
      expect(href).toMatch(
        new RegExp(`^/api/audit-log/export\\?.*format=${format}`),
      );
      expect(href).toContain("kind=exception");
    }
  });
});
