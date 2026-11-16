// #2106 — from the risk page, an owner links controls, unlinks one, and
// adds an action plan that comes back linked to the risk.
//
//   1. The risk links no controls. "Link controls…" opens the shared
//      control picker; the owner picks two and links them. Both are
//      listed by name.
//   2. The owner unlinks one (confirmed inline). It leaves the list; the
//      other stays.
//   3. "Add action plan" opens the new-plan form with the risk already
//      picked. The owner names the plan, picks an owner and creates it,
//      and lands back on the risk, where the plan is listed under Linked
//      Action Plans.
//
// MOCK STRATEGY: the /api/risks, /api/controls-list, /api/people and
// /api/action-plans BFFs are mocked with one in-memory store. The Go tier
// covers the real links, the audit rows, the closed-risk and last-control
// refusals and tenant scoping against Postgres
// (internal/api/risks/control_links_integration_test.go).
//
// Hard rule (P0-A9 / GitGuardian): every id is a neutral test string.

import type { Page, Request, Route } from "@playwright/test";

import { expect, test } from "./fixtures";

const RISK_ID = "00000000-0000-4000-8000-000000002106";
const CONTROL_A = "c1c1c1c1-0000-4000-8000-000000002106";
const CONTROL_B = "c2c2c2c2-0000-4000-8000-000000002106";
const USER_OWNER = "a1a1a1a1-0000-4000-8000-000000002106";
const PLAN_ID = "d4d4d4d4-0000-4000-8000-000000002106";
const TITLE = "Test risk for control links (test-2106)";
const PLAN_TITLE = "Test plan from the risk page";

type Store = {
  linked: string[];
  plans: Record<string, unknown>[];
  linkBodies: unknown[];
  unlinked: string[];
  planRiskLinks: string[];
};

function json(body: unknown, status = 200) {
  return {
    status,
    contentType: "application/json",
    body: JSON.stringify(body),
  };
}

function riskRow(store: Store): Record<string, unknown> {
  return {
    id: RISK_ID,
    title: TITLE,
    description: "A neutral description.",
    category: "operational",
    category_name: "Operational",
    methodology: "nist_800_30",
    inherent_score: { likelihood: 2, impact: 3 },
    treatment: "untreated",
    treatment_owner: "owner-a",
    residual_score: {},
    accepter: "",
    instrument_reference: "",
    linked_control_ids: [...store.linked],
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    level: "team",
    themes: [],
    severity: 6,
    status: "open",
  };
}

async function mockAll(page: Page, store: Store) {
  await page.route(/\/api\/risks(\?.*)?$/, (route: Route) =>
    route.fulfill(json({ risks: [riskRow(store)] })),
  );
  await page.route("**/api/risks/categories**", (route: Route) =>
    route.fulfill(
      json({
        categories: [
          {
            id: "c1",
            slug: "operational",
            name: "Operational",
            archived: false,
            is_default: true,
          },
        ],
      }),
    ),
  );
  await page.route(`**/api/risks/${RISK_ID}/action-plans`, (route: Route) =>
    route.fulfill(json({ action_plans: store.plans })),
  );
  await page.route(`**/api/risks/${RISK_ID}/parents`, (route: Route) =>
    route.fulfill(json({ parents: [], count: 0 })),
  );
  await page.route(`**/api/risks/${RISK_ID}/children`, (route: Route) =>
    route.fulfill(json({ children: [], count: 0 })),
  );
  await page.route(`**/api/risks/${RISK_ID}/audit-log`, (route: Route) =>
    route.fulfill(json({ entries: [], count: 0 })),
  );
  await page.route(`**/api/risks/${RISK_ID}`, (route: Route) =>
    route.fulfill(json({ risk: riskRow(store) })),
  );
  await page.route(
    `**/api/risks/${RISK_ID}/controls`,
    (route: Route, req: Request) => {
      const body = req.postDataJSON() as { control_id: string };
      store.linkBodies.push(body);
      if (!store.linked.includes(body.control_id)) {
        store.linked.push(body.control_id);
      }
      return route.fulfill(json({ risk: riskRow(store) }));
    },
  );
  await page.route(
    `**/api/risks/${RISK_ID}/controls/*`,
    (route: Route, req: Request) => {
      const cid = new URL(req.url()).pathname.split("/").pop()!;
      store.unlinked.push(cid);
      store.linked = store.linked.filter((c) => c !== cid);
      return route.fulfill(json({ risk: riskRow(store) }));
    },
  );
  await page.route("**/api/controls-list", (route: Route) =>
    route.fulfill(
      json({
        controls: [
          {
            id: CONTROL_A,
            title: "Multi-factor sign-in",
            control_family: "IAC",
            scf_id: "IAC-06",
            lifecycle_state: "active",
            bundle_id: "test-bundle",
          },
          {
            id: CONTROL_B,
            title: "Backup restore test",
            control_family: "BCD",
            scf_id: "BCD-11",
            lifecycle_state: "active",
            bundle_id: "test-bundle",
          },
        ],
        count: 2,
      }),
    ),
  );
  await page.route("**/api/people", (route: Route) =>
    route.fulfill(
      json({
        users: [
          { id: USER_OWNER, display_name: "Plan Owner", status: "active" },
        ],
        api_keys: [],
      }),
    ),
  );
  await page.route("**/api/action-plans", (route: Route, req: Request) => {
    if (req.method() === "POST") {
      const body = req.postDataJSON() as Record<string, unknown>;
      return route.fulfill(
        json(
          {
            action_plan: { id: PLAN_ID, title: body.title, status: "draft" },
          },
          201,
        ),
      );
    }
    return route.fulfill(json({ action_plans: [], count: 0 }));
  });
  await page.route(
    `**/api/action-plans/${PLAN_ID}/risks/*`,
    (route: Route, req: Request) => {
      const rid = new URL(req.url()).pathname.split("/").pop()!;
      store.planRiskLinks.push(rid);
      store.plans.push({
        id: PLAN_ID,
        title: PLAN_TITLE,
        status: "draft",
        due_date: null,
      });
      return route.fulfill(json({ linked: true }, 201));
    },
  );
}

test("links two controls, unlinks one, and adds an action plan from the risk page", async ({
  authedPage: page,
}) => {
  const store: Store = {
    linked: [],
    plans: [],
    linkBodies: [],
    unlinked: [],
    planRiskLinks: [],
  };
  await mockAll(page, store);

  await page.goto(`/risks/${RISK_ID}`);
  const section = page.getByTestId("risk-controls");
  await expect(section).toBeVisible({ timeout: 30_000 });
  await expect(page.getByTestId("risk-controls-empty")).toBeVisible();

  // 1. Link two controls with the shared picker.
  await page.getByTestId("risk-controls-link-open").click();
  await page.getByTestId(`risk-controls-picker-checkbox-${CONTROL_A}`).check();
  await page.getByTestId(`risk-controls-picker-checkbox-${CONTROL_B}`).check();
  await expect(page.getByTestId("risk-controls-link-submit")).toHaveText(
    "Link 2 controls",
  );
  await page.getByTestId("risk-controls-link-submit").click();

  await expect(
    page.getByTestId(`risk-controls-row-${CONTROL_A}`),
  ).toContainText("Multi-factor sign-in");
  await expect(
    page.getByTestId(`risk-controls-row-${CONTROL_B}`),
  ).toContainText("Backup restore test");
  await expect(page.getByTestId("risk-controls-picker-panel")).toHaveCount(0);
  expect(store.linkBodies).toEqual([
    { control_id: CONTROL_A },
    { control_id: CONTROL_B },
  ]);

  // 2. Unlink one, confirmed inline.
  await page.getByTestId(`risk-controls-unlink-${CONTROL_A}`).click();
  await page
    .getByTestId(`risk-controls-unlink-confirm-${CONTROL_A}-ok`)
    .click();
  await expect(page.getByTestId(`risk-controls-row-${CONTROL_A}`)).toHaveCount(
    0,
  );
  await expect(
    page.getByTestId(`risk-controls-row-${CONTROL_B}`),
  ).toBeVisible();
  expect(store.unlinked).toEqual([CONTROL_A]);

  // 3. Add an action plan: the form opens with this risk already linked.
  await expect(page.getByTestId("linked-action-plans-empty")).toBeVisible();
  await page.getByTestId("linked-action-plans-add").click();
  await expect(page).toHaveURL(
    new RegExp(`/action-plans/new\\?risk_id=${RISK_ID}$`),
  );
  await expect(
    page.getByTestId(`action-plan-risks-checkbox-${RISK_ID}`),
  ).toBeChecked({ timeout: 30_000 });
  await page.getByTestId("action-plan-title").fill(PLAN_TITLE);
  await page.getByTestId("action-plan-owner").fill("plan");
  await page
    .getByTestId("action-plan-owner-option")
    .filter({ has: page.getByText("Plan Owner", { exact: true }) })
    .click();
  await page.getByTestId("action-plan-submit").click();

  // Back on the risk, the plan is listed as linked.
  await expect(page).toHaveURL(new RegExp(`/risks/${RISK_ID}$`));
  await expect(page.getByTestId("linked-action-plans-list")).toContainText(
    PLAN_TITLE,
    { timeout: 30_000 },
  );
  expect(store.planRiskLinks).toEqual([RISK_ID]);
});
