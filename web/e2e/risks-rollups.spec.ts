// #2060 — Playwright E2E for the register's "Show roll-ups" toggle.
//
// Coverage:
//   - By default the register shows 3 risks: the roll-up parent of two of
//     them is left out of the rows and the count.
//   - The toggle asks the BFF for roll-ups, the count becomes 4, and the
//     roll-up row carries a "Roll-up" marker linking to its children.
//   - Turning the toggle off returns to 3.
//
// Mocked per the `route.fulfill` convention: the BFF answers with the
// parent only when `include_rollups=true` is on the request, as upstream
// does. No sleeps: every assertion auto-waits.
//
// Hard rule (P0-A9): neutral test strings only.

import type { Route } from "@playwright/test";

import { expect, test } from "./fixtures";

const PARENT_ID = "00000000-0000-4000-8000-0000000020a1";
const CHILD_A_ID = "00000000-0000-4000-8000-0000000020b1";
const CHILD_B_ID = "00000000-0000-4000-8000-0000000020b2";
const OTHER_ID = "00000000-0000-4000-8000-0000000020b3";

function riskRow(
  id: string,
  title: string,
  level: "team" | "org" | "company",
  likelihood: number,
  impact: number,
  childCount?: number,
) {
  return {
    id,
    title,
    description: "",
    category: "operational",
    methodology: "nist_800_30",
    inherent_score: { likelihood, impact },
    treatment: "untreated",
    treatment_owner: "",
    residual_score: {},
    accepter: "",
    instrument_reference: "",
    linked_control_ids: [],
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    themes: [],
    level,
    severity: likelihood * impact,
    ...(childCount ? { child_count: childCount } : {}),
  };
}

const RISKS = [
  riskRow(CHILD_A_ID, "Team A export gap", "team", 3, 4),
  riskRow(CHILD_B_ID, "Team B backup gap", "team", 4, 4),
  riskRow(OTHER_ID, "Team C access gap", "team", 1, 2),
];
const ROLL_UP = riskRow(PARENT_ID, "Org data handling", "org", 5, 5, 2);

async function json(route: Route, body: unknown) {
  await route.fulfill({
    status: 200,
    contentType: "application/json",
    body: JSON.stringify(body),
  });
}

test.describe("/risks roll-ups toggle (#2060)", () => {
  test("leaves roll-ups out by default and marks them when shown", async ({
    authedPage: page,
  }) => {
    await page.route(/\/api\/risks(\?.*)?$/, (route, req) => {
      const withRollUps = new URL(req.url()).searchParams.get(
        "include_rollups",
      );
      return json(route, {
        risks: withRollUps === "true" ? [ROLL_UP, ...RISKS] : RISKS,
      });
    });

    await page.goto("/risks");
    const count = page.getByTestId("risks-count-label");
    await expect(count).toHaveText("3 risks", { timeout: 30_000 });
    await expect(page.getByTestId("risks-row-rollup")).toHaveCount(0);

    const toggle = page.getByTestId("risks-show-rollups");
    await expect(toggle).not.toBeChecked();
    // The checkbox is URL-driven: its state flips once router.replace
    // lands, not synchronously, so click and assert rather than check().
    await toggle.click();

    await expect(page).toHaveURL(/[?&]rollups=1/);
    await expect(toggle).toBeChecked();
    await expect(count).toHaveText("4 risks");
    const marker = page.getByTestId("risks-row-rollup");
    await expect(marker).toHaveCount(1);
    await expect(marker).toHaveText("Roll-up · 2 children");
    await expect(marker).toHaveAttribute(
      "href",
      `/risks/${PARENT_ID}#children`,
    );

    await toggle.click();
    await expect(page).not.toHaveURL(/rollups=1/);
    await expect(toggle).not.toBeChecked();
    await expect(count).toHaveText("3 risks");
    await expect(page.getByTestId("risks-row-rollup")).toHaveCount(0);
  });
});
