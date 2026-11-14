// #1748 — Playwright E2E for the Parents / Children sections on the risk
// detail page (`/risks/[id]`).
//
// Coverage:
//   - Parents and Children render with links to each risk.
//   - The attach picker offers only lower-level risks that are not
//     already children.
//   - Attaching a child shows it in Children and shows the roll-up beside
//     the assessed severity, with the higher-than-assessed flag.
//   - Detaching removes the child and the roll-up.
//   - A platform refusal (409 cycle) is shown as the platform's message.
//
// Mocked per the `route.fulfill` convention (risks-create-control-link):
// the BFF routes are stateful in-test so a POST / DELETE changes what the
// next GET returns. No sleeps: every assertion auto-waits.
//
// Hard rule (P0-A9): neutral test strings only.

import type { Page, Route } from "@playwright/test";

import { expect, test } from "./fixtures";

const ORG_ID = "00000000-0000-4000-8000-00000000a001";
const COMPANY_ID = "00000000-0000-4000-8000-00000000c001";
const TEAM_A_ID = "00000000-0000-4000-8000-00000000e001";
const TEAM_B_ID = "00000000-0000-4000-8000-00000000e002";
const OTHER_ORG_ID = "00000000-0000-4000-8000-00000000a002";

function riskRow(
  id: string,
  title: string,
  level: "team" | "org" | "company",
  likelihood: number,
  impact: number,
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
  };
}

const REGISTER = [
  riskRow(ORG_ID, "Org data handling", "org", 2, 2), // assessed 4
  riskRow(COMPANY_ID, "Enterprise data loss", "company", 3, 3),
  riskRow(TEAM_A_ID, "Team A export gap", "team", 3, 4), // 12
  riskRow(TEAM_B_ID, "Team B backup gap", "team", 4, 4), // 16
  riskRow(OTHER_ORG_ID, "Another org risk", "org", 2, 3),
];

function linked(id: string) {
  const r = REGISTER.find((x) => x.id === id)!;
  return {
    id: r.id,
    title: r.title,
    level: r.level,
    severity: r.severity,
    link_source: "manual",
    linked_at: "2026-01-02T00:00:00Z",
  };
}

async function json(route: Route, status: number, body: unknown) {
  await route.fulfill({
    status,
    contentType: "application/json",
    body: body === null ? "" : JSON.stringify(body),
  });
}

// mockHierarchy serves the org risk's detail, its parents (the company
// risk) and a mutable children set. rejectAttach makes POST answer 409.
async function mockHierarchy(
  page: Page,
  opts: { rejectAttach?: boolean } = {},
) {
  const children = new Set<string>();

  const rollup = () => {
    if (children.size === 0) return undefined;
    const sev = Math.max(
      ...[...children].map((id) => REGISTER.find((r) => r.id === id)!.severity),
    );
    return {
      severity: sev,
      severity_function: "max",
      child_count: children.size,
      assessed_severity: 4,
      exceeds_assessed: sev > 4,
    };
  };

  await page.route(/\/api\/risks(\?.*)?$/, (route) =>
    json(route, 200, { risks: REGISTER }),
  );
  await page.route(`**/api/risks/${ORG_ID}`, (route) =>
    json(route, 200, {
      risk: { ...REGISTER[0], rollup: rollup() },
    }),
  );
  await page.route(`**/api/risks/${ORG_ID}/action-plans`, (route) =>
    json(route, 200, { action_plans: [] }),
  );
  await page.route(`**/api/risks/${ORG_ID}/parents`, (route) =>
    json(route, 200, { parents: [linked(COMPANY_ID)], count: 1 }),
  );
  await page.route(`**/api/risks/${ORG_ID}/children`, async (route, req) => {
    if (req.method() === "POST") {
      if (opts.rejectAttach) {
        return json(route, 409, {
          error: `risk: link would create a cycle in the risk hierarchy: risk ${TEAM_B_ID}`,
        });
      }
      const body = req.postDataJSON() as { child_risk_ids: string[] };
      body.child_risk_ids.forEach((id) => children.add(id));
    }
    return json(route, 200, {
      children: [...children].map(linked),
      count: children.size,
      ...(rollup() ? { rollup: rollup() } : {}),
    });
  });
  await page.route(
    new RegExp(`/api/risks/${ORG_ID}/children/[0-9a-f-]+$`),
    async (route, req) => {
      const childID = req.url().split("/").pop()!;
      if (!children.delete(childID)) {
        return json(route, 404, {
          error: "child risk is not linked to this parent",
        });
      }
      return route.fulfill({ status: 204, body: "" });
    },
  );
}

test.describe("/risks/[id] parents and children (#1748)", () => {
  test("shows parents, attaches and detaches a child, and shows the roll-up", async ({
    authedPage: page,
  }) => {
    await mockHierarchy(page);
    await page.goto(`/risks/${ORG_ID}`);
    await expect(page.getByTestId("risk-detail")).toBeVisible({
      timeout: 30_000,
    });

    // Parents: the company risk, linked.
    const parent = page.getByTestId("risk-detail-parent");
    await expect(parent).toHaveCount(1);
    await expect(parent.getByRole("link")).toHaveAttribute(
      "href",
      `/risks/${COMPANY_ID}`,
    );
    await expect(page.getByTestId("risk-detail-children-empty")).toBeVisible();
    await expect(page.getByTestId("risk-detail-rollup")).toHaveCount(0);

    // The picker offers only the two team risks: not the company risk,
    // not the other org risk, not the page's own risk.
    const picker = page.getByTestId("risk-detail-attach-select");
    await expect(picker.locator("option")).toHaveText([
      "Choose a risk",
      "Team A export gap (team)",
      "Team B backup gap (team)",
    ]);

    // Attach team B: it appears as a linked child and the roll-up (16)
    // shows beside the assessed severity (4) with the flag.
    await picker.selectOption(TEAM_B_ID);
    await page.getByTestId("risk-detail-attach-submit").click();
    const child = page.getByTestId("risk-detail-child");
    await expect(child).toHaveCount(1);
    await expect(child.getByRole("link")).toHaveAttribute(
      "href",
      `/risks/${TEAM_B_ID}`,
    );
    await expect(page.getByTestId("risk-detail-severity")).toHaveText("4");
    await expect(page.getByTestId("risk-detail-rollup-severity")).toHaveText(
      "16",
    );
    await expect(page.getByTestId("risk-detail-rollup-exceeds")).toBeVisible();
    // An attached child leaves the picker.
    await expect(picker.locator("option")).toHaveText([
      "Choose a risk",
      "Team A export gap (team)",
    ]);

    // Detach: the child and the roll-up go.
    await page.getByTestId(`risk-detail-child-detach-${TEAM_B_ID}`).click();
    await expect(page.getByTestId("risk-detail-children-empty")).toBeVisible();
    await expect(page.getByTestId("risk-detail-rollup")).toHaveCount(0);
  });

  test("shows the platform's refusal when an attach is rejected", async ({
    authedPage: page,
  }) => {
    await mockHierarchy(page, { rejectAttach: true });
    await page.goto(`/risks/${ORG_ID}`);
    await expect(page.getByTestId("risk-detail")).toBeVisible({
      timeout: 30_000,
    });

    await page.getByTestId("risk-detail-attach-select").selectOption(TEAM_B_ID);
    await page.getByTestId("risk-detail-attach-submit").click();

    await expect(page.getByTestId("risk-detail-hierarchy-error")).toContainText(
      "cycle",
    );
    await expect(page.getByTestId("risk-detail-children-empty")).toBeVisible();
  });
});
