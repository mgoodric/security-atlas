// #2185 — Playwright E2E for the board-pack top-risks table with a FAIR
// risk in it.
//
// A FAIR risk's residual_severity is a dollar amount (annualized loss
// exposure), not a 1..25 severity. The table shows it as currency with no
// Low/Medium/High band; a 5x5 row keeps its number and band.
//
// Hermetic mock pattern (as board-pack-vendor-burndown.spec.ts): the pack
// and the /api/admin/me approver probe are route-mocked, so the assertions
// do not depend on the demo seed. `authedPage` supplies the session cookie.

import { expect, test } from "./fixtures";

const PACK_ID = "00000000-0000-0000-0000-0000021850aa";

const pack = {
  id: PACK_ID,
  period_end: "2026-03-31",
  status: "draft",
  narrative_md: "Quarterly board pack — e2e mock.",
  created_at: "2026-03-31T00:00:00Z",
  updated_at: "2026-03-31T00:00:00Z",
  content: {
    period_end: "2026-03-31",
    generated_at: "2026-03-31T00:00:00Z",
    status: "draft",
    sections: {
      top_risks: {
        key: "top_risks",
        title: "Top risks aging",
        templated_text: "Templated narrative for Top risks aging.",
        override_text: "",
        approved: true,
        data: {
          // The generator ranks every 5x5 risk above every FAIR risk.
          top_risks: [
            {
              id: "RISK-001",
              title: "Unpatched perimeter",
              category: "operational",
              treatment: "mitigate",
              methodology: "nist_800_30",
              residual_severity: 12,
              age_days: 30,
            },
            {
              id: "RISK-002",
              title: "Wire fraud",
              category: "financial",
              treatment: "mitigate",
              methodology: "fair",
              residual_severity: 1250000,
              age_days: 40,
            },
          ],
        },
      },
    },
  },
};

test.describe("board-pack top risks with a FAIR risk (#2185)", () => {
  test("a FAIR residual shows as dollars with no band; a 5x5 residual keeps its band", async ({
    authedPage: page,
  }) => {
    await page.route("**/api/admin/me", (route) =>
      route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ is_admin: true }),
      }),
    );
    await page.route(`**/api/board-packs/${PACK_ID}`, (route) =>
      route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(pack),
      }),
    );
    const resp = page.waitForResponse(
      (r) =>
        r.url().includes(`/api/board-packs/${PACK_ID}`) && r.status() === 200,
      { timeout: 30_000 },
    );
    await page.goto(`/board-packs/${PACK_ID}`);
    await resp;

    const table = page.getByTestId("top-risks-table");
    await expect(table).toBeVisible();
    const residuals = table.getByTestId("top-risks-residual");
    await expect(residuals).toHaveCount(2);
    await expect(residuals.nth(0)).toHaveText("12");
    await expect(residuals.nth(1)).toHaveText("$1,250,000");

    // Only the 5x5 row carries a band label.
    const bands = table.getByTestId("top-risks-residual-band");
    await expect(bands).toHaveCount(1);
    await expect(bands.first()).toHaveText("Medium");
  });
});
