// #2108 — the action-plan stepper and edit route.
//
//   1. A plan owner walks a plan draft -> in progress -> blocked ->
//      in progress -> completed through the header's buttons. Each
//      transition confirms inline, then toasts, and sends one PATCH with
//      the next status.
//   2. On a completed plan with no linked controls, Verify shows for an
//      approver who is not the owner, and verifies; it never shows for the
//      owner, nor on a plan with linked controls.
//   3. Editing the owner uses the name picker: the PATCH carries the
//      picked person's id, and only the changed field.
//
// The populated detail page (with its inline confirm open) and the edit
// form pass the axe gate; the route sweep only sees their not-found state.
//
// MOCK STRATEGY: the /api/action-plans BFFs are mocked with one in-memory
// plan that follows the platform's state machine, and /api/me and
// /api/people are mocked so the viewer and the directory are known. The Go
// tier covers the real state machine, the verify rules and the owner check
// against Postgres (internal/actionplan/verify_integration_test.go).
//
// Hard rule (P0-A9 / GitGuardian): every id is a neutral test string.

import type { Page, Request, Route } from "@playwright/test";

import { expectNoSeriousAxe, seriousAxeViolations } from "./axe-gate";
import { expect, test } from "./fixtures";

const PLAN_ID = "d4d4d4d4-0000-4000-8000-000000002108";
const OWNER = "a1a1a1a1-0000-4000-8000-000000002108";
const APPROVER = "b2b2b2b2-0000-4000-8000-000000002108";
const CONTROL = "c3c3c3c3-0000-4000-8000-000000002108";

const ALLOWED: Record<string, string[]> = {
  draft: ["in_progress"],
  in_progress: ["blocked", "completed"],
  blocked: ["in_progress", "completed"],
  completed: ["in_progress"],
  verified: [],
};

type Store = {
  plan: Record<string, unknown>;
  controls: string[];
  patches: Record<string, unknown>[];
  verifies: number;
};

function newStore(status = "draft", controls: string[] = []): Store {
  return {
    plan: {
      id: PLAN_ID,
      title: "Rotate the deploy keys (test-2108)",
      description: "A neutral description.",
      triggering_event: "",
      owner_id: OWNER,
      due_date: "2027-01-15",
      status,
      created_at: "2026-11-16T00:00:00Z",
      updated_at: "2026-11-16T00:00:00Z",
    },
    controls,
    patches: [],
    verifies: 0,
  };
}

function json(body: unknown, status = 200) {
  return {
    status,
    contentType: "application/json",
    body: JSON.stringify(body),
  };
}

async function mock(page: Page, store: Store, viewer: string) {
  await page.route("**/api/me", (route: Route) =>
    route.fulfill(
      json({
        user_id: viewer,
        tenant_id: "e5e5e5e5-0000-4000-8000-000000002108",
        display_name: viewer === OWNER ? "Olive Owner" : "Abel Approver",
        email: "viewer@example.test",
        idp_subject: "",
        tenant_role: "admin",
        time_zone: null,
        theme: null,
        is_admin: true,
        owner_roles: [],
        roles: ["admin"],
      }),
    ),
  );
  await page.route("**/api/people", (route: Route) =>
    route.fulfill(
      json({
        users: [
          { id: OWNER, display_name: "Olive Owner", status: "active" },
          { id: APPROVER, display_name: "Abel Approver", status: "active" },
        ],
        api_keys: [],
      }),
    ),
  );
  await page.route(`**/api/action-plans/${PLAN_ID}/verify`, (route: Route) => {
    store.verifies++;
    if (store.plan.status !== "completed" || store.controls.length > 0) {
      return route.fulfill(json({ error: "refused" }, 409));
    }
    store.plan.status = "verified";
    return route.fulfill(json({ action_plan: store.plan }));
  });
  await page.route(
    `**/api/action-plans/${PLAN_ID}`,
    (route: Route, req: Request) => {
      if (req.method() === "PATCH") {
        const body = (req.postDataJSON() ?? {}) as Record<string, unknown>;
        store.patches.push(body);
        const to = body.status as string | undefined;
        if (to && !ALLOWED[store.plan.status as string].includes(to)) {
          return route.fulfill(
            json({ error: "illegal status transition" }, 422),
          );
        }
        Object.assign(store.plan, body);
        return route.fulfill(json({ action_plan: store.plan }));
      }
      return route.fulfill(
        json({
          action_plan: store.plan,
          linkage: {
            risks: [],
            controls: store.controls.map((c) => ({
              target_id: c,
              linked_at: "2026-11-16T00:00:00Z",
              linked_by: OWNER,
            })),
          },
        }),
      );
    },
  );
}

// step clicks a transition (the header button, or the overflow item),
// confirms it inline, and waits for its toast and the new status.
async function step(
  page: Page,
  id: string,
  inOverflow: boolean,
  toast: string,
  status: string,
) {
  if (inOverflow) {
    await page.getByTestId("detail-header-overflow-trigger").click();
  }
  await page.getByTestId(`action-plan-transition-${id}`).click();
  const confirm = page.getByTestId("action-plan-transition-confirm");
  await expect(confirm).toBeVisible();
  await page.getByTestId("action-plan-transition-confirm-ok").click();
  await expect(confirm).toHaveCount(0);
  await expect(page.getByTestId("toast-viewport")).toContainText(toast);
  await expect(page.getByTestId("action-plan-detail-status")).toHaveText(
    status,
  );
}

test.describe("/action-plans/[id] stepper and edit (#2108)", () => {
  test("draft -> in progress -> blocked -> in progress -> completed", async ({
    authedPage: page,
  }) => {
    const store = newStore();
    await mock(page, store, OWNER);
    await page.goto(`/action-plans/${PLAN_ID}`);
    await expect(page.getByTestId("action-plan-detail-status")).toHaveText(
      "Draft",
      { timeout: 30_000 },
    );

    // A cancelled confirm sends nothing.
    await page.getByTestId("action-plan-transition-start").click();
    await page.getByTestId("action-plan-transition-confirm-cancel").click();
    await expect(
      page.getByTestId("action-plan-transition-confirm"),
    ).toHaveCount(0);
    expect(store.patches).toEqual([]);

    // The populated header, stepper and inline confirm pass the axe gate.
    await page.getByTestId("action-plan-transition-start").click();
    expectNoSeriousAxe(await seriousAxeViolations(page), "action-plan detail");
    await page.getByTestId("action-plan-transition-confirm-cancel").click();

    await step(page, "start", false, "Plan started", "In progress");
    await step(page, "block", true, "Plan blocked", "Blocked");
    await step(page, "unblock", false, "Plan unblocked", "In progress");
    await step(page, "complete", false, "Plan marked complete", "Completed");

    expect(store.patches).toEqual([
      { status: "in_progress" },
      { status: "blocked" },
      { status: "in_progress" },
      { status: "completed" },
    ]);
    // The owner is never offered Verify; Reopen stays in the overflow.
    await expect(page.getByTestId("action-plan-transition-verify")).toHaveCount(
      0,
    );
    await page.getByTestId("detail-header-overflow-trigger").click();
    await expect(
      page.getByTestId("action-plan-transition-reopen"),
    ).toBeVisible();
  });

  test("an approver who is not the owner verifies a no-control plan", async ({
    authedPage: page,
  }) => {
    const store = newStore("completed");
    await mock(page, store, APPROVER);
    await page.goto(`/action-plans/${PLAN_ID}`);
    await expect(page.getByTestId("action-plan-detail-status")).toHaveText(
      "Completed",
      { timeout: 30_000 },
    );
    await step(page, "verify", false, "Plan verified", "Verified");
    expect(store.verifies).toBe(1);
    await expect(page.getByTestId("action-plan-transition-verify")).toHaveCount(
      0,
    );
  });

  test("Verify is not offered on a plan with linked controls", async ({
    authedPage: page,
  }) => {
    const store = newStore("completed", [CONTROL]);
    await mock(page, store, APPROVER);
    await page.goto(`/action-plans/${PLAN_ID}`);
    await expect(
      page.getByTestId("action-plan-detail-verify-by-controls"),
    ).toBeVisible({ timeout: 30_000 });
    await expect(page.getByTestId("action-plan-transition-verify")).toHaveCount(
      0,
    );
  });

  test("editing the owner uses the name picker", async ({
    authedPage: page,
  }) => {
    const store = newStore();
    await mock(page, store, OWNER);
    await page.goto(`/action-plans/${PLAN_ID}`);
    await page.getByTestId("action-plan-detail-edit").click();
    await expect(page).toHaveURL(new RegExp(`/action-plans/${PLAN_ID}/edit$`), {
      timeout: 30_000,
    });

    const owner = page.getByTestId("action-plan-edit-owner");
    await expect(owner).toHaveValue("Olive Owner", { timeout: 30_000 });
    expectNoSeriousAxe(await seriousAxeViolations(page), "action-plan edit");
    await owner.fill("abel");
    await page
      .getByTestId("action-plan-edit-owner-option")
      .filter({ has: page.getByText("Abel Approver", { exact: true }) })
      .click();
    await expect(owner).toHaveValue("Abel Approver");
    await page.getByTestId("action-plan-edit-submit").click();

    await expect(page).toHaveURL(new RegExp(`/action-plans/${PLAN_ID}$`), {
      timeout: 30_000,
    });
    await expect(page.getByTestId("toast-viewport")).toContainText(
      "Action plan updated",
    );
    expect(store.patches).toEqual([{ owner_id: APPROVER }]);
  });
});
