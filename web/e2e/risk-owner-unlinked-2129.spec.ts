// #2129 — a risk whose free-text owner matched no user shows it as
// "Owner (unlinked)" and lets the user pick the person it names.
//
// On the risk page the unlinked text is shown beside a "Pick the user"
// control. Picking a user and saving sends PATCH /api/risks/{id} with
// that user's id as owner_user_id; the page then shows the owner by name
// and the unlinked text is gone. The edit form shows the same prompt.
//
// HERMETIC: route-mocks the risk BFF routes and the people directory
// (slice 594 convention). The PATCH mock applies the body as the platform
// does: a picked owner clears the unlinked text. The platform side
// (validation, the 422, the audit row) is covered by
// internal/api/risks/owner_integration_test.go against real Postgres.

import type { Route } from "@playwright/test";

import { expect, test } from "./fixtures";
import { pickFirstUser } from "./owner-picker";

const RISK_ID = "00000000-0000-4000-8000-000000002129";
// Neutral test strings only (P0-A4 / GitGuardian).
const USER_ID = "a1a1a1a1-0000-4000-8000-000000002129";
const USER_NAME = "Owner Picked";
const UNLINKED = "Someone in Finance";

function storedRisk(): Record<string, unknown> {
  return {
    id: RISK_ID,
    title: "Risk with an unlinked owner",
    description: "",
    category: "operational",
    category_name: "Operational",
    methodology: "nist_800_30",
    inherent_score: { likelihood: 3, impact: 4 },
    treatment: "untreated",
    owner_user_id: null,
    owner_unlinked: UNLINKED,
    treatment_owner: UNLINKED,
    residual_score: {},
    review_due_at: "2027-01-15T00:00:00Z",
    accepter: "",
    instrument_reference: "",
    linked_control_ids: [],
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    level: "team",
    themes: [],
    severity: 12,
    status: "open",
  };
}

async function json(route: Route, status: number, body: unknown) {
  await route.fulfill({
    status,
    contentType: "application/json",
    body: JSON.stringify(body),
  });
}

test.describe("/risks/[id] unlinked owner (#2129)", () => {
  test("shows the unlinked owner and lets me pick the user", async ({
    authedPage: page,
  }) => {
    const risk = storedRisk();
    let patched: Record<string, unknown> | undefined;

    await page.route(/\/api\/people(\?.*)?$/, (route) =>
      json(route, 200, {
        users: [{ id: USER_ID, display_name: USER_NAME, status: "active" }],
        api_keys: [],
      }),
    );
    await page.route("**/api/risks/categories**", (route) =>
      json(route, 200, {
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
    );
    await page.route(`**/api/risks/${RISK_ID}/action-plans`, (route) =>
      json(route, 200, { action_plans: [] }),
    );
    await page.route(`**/api/risks/${RISK_ID}/parents`, (route) =>
      json(route, 200, { parents: [], count: 0 }),
    );
    await page.route(`**/api/risks/${RISK_ID}/children`, (route) =>
      json(route, 200, { children: [], count: 0 }),
    );
    await page.route(`**/api/risks/${RISK_ID}/audit-log`, (route) =>
      json(route, 200, { entries: [], count: 0 }),
    );
    await page.route(/\/api\/risks(\?.*)?$/, (route) =>
      json(route, 200, { risks: [risk], count: 1 }),
    );
    await page.route(`**/api/risks/${RISK_ID}`, async (route, req) => {
      if (req.method() === "PATCH") {
        patched = JSON.parse(req.postData() ?? "{}");
        Object.assign(risk, patched);
        if (risk.owner_user_id) {
          risk.owner_unlinked = "";
          risk.treatment_owner = "";
        }
        await json(route, 200, { risk });
        return;
      }
      await json(route, 200, { risk });
    });

    // The edit form shows the unlinked owner with the prompt to pick.
    await page.goto(`/risks/${RISK_ID}/edit`);
    await expect(page.getByTestId("risks-form-owner-unlinked")).toContainText(
      UNLINKED,
      { timeout: 30_000 },
    );

    // The risk page shows it too, marked unlinked.
    await page.goto(`/risks/${RISK_ID}`);
    const unlinked = page.getByTestId("risk-detail-owner-unlinked");
    await expect(unlinked).toContainText(UNLINKED, { timeout: 30_000 });
    await expect(unlinked).toContainText("unlinked");

    // Pick the user and save.
    await page.getByTestId("risk-detail-owner-pick-open").click();
    const picked = await pickFirstUser(page, "risk-detail-owner-pick");
    expect(picked).toBe(USER_NAME);
    await page.getByTestId("risk-detail-owner-save").click();

    await expect.poll(() => patched).toEqual({ owner_user_id: USER_ID });
    const owner = page.getByTestId("risk-detail-owner");
    await expect(owner).toHaveText(USER_NAME);
    await expect(owner).toHaveAttribute("data-person-kind", "user");
    await expect(page.getByTestId("risk-detail-owner-unlinked")).toHaveCount(0);
  });
});
