// #2464 — the Activity page's default window includes now, west of UTC.
//
// Runs only in the `chromium-denver` project (playwright.config.ts). The
// old default ended at today's 00:00 UTC, which the server reads as
// exclusive, so an event from moments ago fell outside the default view.
// Late evening in Denver is already tomorrow in UTC, which is where a
// day-boundary default goes wrong in a second way; the default is now
// an exact instant, the same moment in every timezone.
//
// HERMETIC: route-mocks `/api/activity` and `/api/people` (slice 594
// convention) and checks the window the page asks for, plus that the
// export bar and the actors picker carry the same window.

import { expect, test } from "./fixtures";

const ACTOR = "00000000-0000-4000-8000-000000002464";

test.describe("Activity default window includes now (#2464)", () => {
  test("a row written moments ago shows with the default filters", async ({
    authedPage: page,
  }) => {
    const windows: { from: string; to: string }[] = [];
    await page.route(/\/api\/activity(\?.*)?$/, async (route) => {
      const u = new URL(route.request().url());
      const from = u.searchParams.get("from") ?? "";
      const to = u.searchParams.get("to") ?? "";
      windows.push({ from, to });
      const at = new Date(Date.now() - 5_000).toISOString();
      // Answer as the server would: only rows inside [from, to).
      const t = Date.parse(at);
      const inside = t >= Date.parse(from) && t < Date.parse(to);
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          entries: inside
            ? [
                {
                  occurred_at: at,
                  actor_id: ACTOR,
                  actor_name: "Test Person",
                  tenant_id: "00000000-0000-4000-8000-000000000001",
                  kind: "exception",
                  target_type: "exception",
                  target_id: "00000000-0000-4000-8000-0000000024e1",
                  action: "approved",
                  row_id: "test-row-2464",
                  payload_json: {},
                },
              ]
            : [],
        }),
      });
    });
    const actorWindows: { from: string; to: string }[] = [];
    await page.route(/\/api\/activity\/actors(\?.*)?$/, async (route) => {
      const u = new URL(route.request().url());
      actorWindows.push({
        from: u.searchParams.get("from") ?? "",
        to: u.searchParams.get("to") ?? "",
      });
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ actors: [] }),
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

    const before = Date.now();
    await page.goto("/activity");

    await expect(page.getByTestId("activity-row")).toHaveCount(1, {
      timeout: 30_000,
    });
    expect(windows.length).toBeGreaterThan(0);
    const { from, to } = windows[0];
    // `to` is an exact instant at or after the page opened, not a day.
    expect(Date.parse(to)).toBeGreaterThanOrEqual(before - 1_000);
    expect(to).not.toMatch(/T00:00:00(\.000)?Z$/);

    const exportHref = await page
      .getByTestId("activity-export-csv")
      .getAttribute("href");
    expect(exportHref).not.toBeNull();
    const e = new URL(exportHref ?? "", "http://x");
    expect(e.searchParams.get("from")).toBe(from);
    expect(e.searchParams.get("to")).toBe(to);

    // The actors picker (#2414) asks for the same window.
    await expect.poll(() => actorWindows.length).toBeGreaterThan(0);
    expect(actorWindows[0]).toEqual({ from, to });
  });
});
