// TEMPORARY (#2793 proof, reverted before merge): the pre-fix fixture
// shape (rows 3 and 6 minutes before "now") with the browser clock at
// 00:01 UTC. Marked test.fail(): it passes only if the old shape fails.
import { expect, test } from "./fixtures";

const ACTOR = "00000000-0000-4000-8000-000000002093";
const AT = new Date("2026-03-10T00:01:00Z");
const ago = (min: number) =>
  new Date(AT.getTime() - min * 60 * 1000).toISOString();

test("repro: the pre-#2793 fixture shape fails at 00:01 UTC", async ({
  authedPage: page,
}) => {
  test.fail();
  await page.clock.setFixedTime(AT);
  const row = (id: string, at: string) => ({
    occurred_at: at,
    actor_id: ACTOR,
    actor_name: "Test Person",
    tenant_id: "00000000-0000-4000-8000-000000000001",
    kind: "risk",
    target_type: "risk",
    target_id: "00000000-0000-4000-8000-0000000020e2",
    action: "fields_changed",
    row_id: id,
    payload_json: {},
  });
  await page.route(/\/api\/activity(\?.*)?$/, (r) =>
    r.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        entries: [
          row("r1", ago(3)),
          row("r2", ago(6)),
          row("r3", "2025-01-15T09:30:00Z"),
        ],
      }),
    }),
  );
  await page.route(/\/api\/people(\?.*)?$/, (r) =>
    r.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ users: [], api_keys: [] }),
    }),
  );
  await page.goto("/activity");
  const days = page.getByTestId("activity-day");
  await expect(days).toHaveCount(2, { timeout: 30_000 });
  await expect(days.nth(0).getByTestId("activity-day-heading")).toHaveText(
    "Today",
    { timeout: 5_000 },
  );
});
