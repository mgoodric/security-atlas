// #2658 — a hand-picked "To" day includes that day, west of UTC.
//
// Runs only in the `chromium-denver` project (playwright.config.ts). The
// server reads `to` as exclusive, and the page used to send the picked
// day's 00:00 UTC, so picking today showed nothing from today. The date
// inputs also showed the UTC slice of the bounds, which on a Denver
// evening reads as tomorrow. The page now sends the start of the next
// local day for "To" and renders both inputs in the viewer's local date.
//
// HERMETIC: route-mocks `/api/activity`, `/api/activity/actors` and
// `/api/people` (slice 594 convention). The activity mock answers as the
// server would: only rows inside [from, to).

import { expect, test } from "./fixtures";

const ACTOR = "00000000-0000-4000-8000-000000002658";

test.describe("Activity hand-picked To day is inclusive (#2658)", () => {
  test("picking today's date in To shows an event written just now", async ({
    authedPage: page,
  }) => {
    const at = new Date(Date.now() - 5_000).toISOString();
    const windows: { from: string; to: string }[] = [];
    await page.route(/\/api\/activity(\?.*)?$/, async (route) => {
      const u = new URL(route.request().url());
      const from = u.searchParams.get("from") ?? "";
      const to = u.searchParams.get("to") ?? "";
      windows.push({ from, to });
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
                  target_id: "00000000-0000-4000-8000-0000000026e1",
                  action: "approved",
                  row_id: "test-row-2658",
                  payload_json: {},
                },
              ]
            : [],
        }),
      });
    });
    await page.route(/\/api\/activity\/actors(\?.*)?$/, async (route) => {
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

    await page.goto("/activity");
    await expect(page.getByTestId("activity-row")).toHaveCount(1, {
      timeout: 30_000,
    });

    // The browser's local calendar: today, yesterday, and the start of
    // tomorrow (the exclusive bound a picked "today" must send).
    const local = await page.evaluate(() => {
      const day = (d: Date) =>
        `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(
          2,
          "0",
        )}-${String(d.getDate()).padStart(2, "0")}`;
      const now = new Date();
      const y = now.getFullYear();
      const m = now.getMonth();
      const d = now.getDate();
      return {
        tz: Intl.DateTimeFormat().resolvedOptions().timeZone,
        today: day(now),
        yesterday: day(new Date(y, m, d - 1)),
        tomorrowStart: new Date(y, m, d + 1).toISOString(),
      };
    });
    expect(local.tz).toBe("America/Denver");

    // The default `to` is now; the input shows the viewer's today.
    await expect(page.getByTestId("activity-to")).toHaveValue(local.today);

    // Move "To" off today first, so picking today is a real change.
    await page.getByTestId("activity-to").fill(local.yesterday);
    await expect(page.getByTestId("activity-row")).toHaveCount(0, {
      timeout: 30_000,
    });

    await page.getByTestId("activity-to").fill(local.today);
    await expect(page.getByTestId("activity-row")).toHaveCount(1, {
      timeout: 30_000,
    });
    await expect(page.getByTestId("activity-to")).toHaveValue(local.today);
    expect(new URL(page.url()).searchParams.get("to")).toBe(
      local.tomorrowStart,
    );
    expect(windows[windows.length - 1].to).toBe(local.tomorrowStart);
  });
});
