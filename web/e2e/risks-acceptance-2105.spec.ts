// #2105 — a risk owner proposes acceptance and a second user approves it
// from the risk page.
//
//   1. The owner opens the risk, follows the header's "Propose acceptance"
//      to its form, gives a justification and a proposed expiry, and
//      returns to the risk with a toast. The Acceptance card shows the
//      proposal awaiting approval; the header now offers "Approve
//      acceptance", and Change treatment is unavailable.
//   2. The owner cannot approve their own proposal: the platform's refusal
//      shows on the approve form and the risk stays proposed.
//   3. A second user, in their own browser context, approves it from the
//      risk page, keeping the proposed expiry. The risk reads accepted, the
//      card shows the approval, and History lists both transitions.
//
// MOCK STRATEGY: the /api/risks/{id} BFFs are mocked on each browser
// context with one shared in-memory store, so the two users see the same
// risk. Each context's mock answers as its own person, and approve applies
// the approval rule (approver differs from proposer), which the Go tier
// covers for real against Postgres
// (internal/api/risks/transitions_integration_test.go).
//
// Hard rule (P0-A9 / GitGuardian): every id is a neutral test string.

import type { BrowserContext, Request, Route } from "@playwright/test";

import { expect, test } from "./fixtures";

const RISK_ID = "00000000-0000-4000-8000-000000002105";
const OWNER = "00000000-0000-0000-0000-0000000021c1";
const APPROVER = "00000000-0000-0000-0000-0000000021d2";

type Store = {
  risk: Record<string, unknown>;
  log: Record<string, unknown>[];
  bodies: Record<string, unknown>[];
};

function newStore(): Store {
  return {
    risk: {
      id: RISK_ID,
      title: "Test risk for acceptance",
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
      linked_control_ids: [],
      created_at: "2026-01-01T00:00:00Z",
      updated_at: "2026-01-01T00:00:00Z",
      level: "team",
      themes: [],
      severity: 6,
    },
    log: [],
    bodies: [],
  };
}

function json(body: unknown, status = 200) {
  return {
    status,
    contentType: "application/json",
    body: JSON.stringify(body),
  };
}

/** Mocks the risk page's BFFs on `ctx`, answering as `me`. */
async function mockRisk(ctx: BrowserContext, store: Store, me: string) {
  await ctx.route("**/api/risks/categories**", (route: Route) =>
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
  await ctx.route(`**/api/risks/${RISK_ID}/action-plans`, (route: Route) =>
    route.fulfill(json({ action_plans: [] })),
  );
  await ctx.route(`**/api/risks/${RISK_ID}/parents`, (route: Route) =>
    route.fulfill(json({ parents: [], count: 0 })),
  );
  await ctx.route(`**/api/risks/${RISK_ID}/children`, (route: Route) =>
    route.fulfill(json({ children: [], count: 0 })),
  );
  await ctx.route(`**/api/risks/${RISK_ID}/audit-log`, (route: Route) =>
    route.fulfill(json({ entries: store.log, count: store.log.length })),
  );
  await ctx.route(`**/api/risks/${RISK_ID}`, (route: Route) =>
    route.fulfill(json({ risk: store.risk })),
  );
  await ctx.route(
    `**/api/risks/${RISK_ID}/acceptance/*`,
    async (route: Route, req: Request) => {
      const action = new URL(req.url()).pathname.split("/").pop();
      const body = (req.postDataJSON() ?? {}) as Record<string, string>;
      store.bodies.push({ action, ...body });
      const r = store.risk;
      const now = new Date().toISOString();
      const audit = (
        a: string,
        before: Record<string, unknown>,
        after: Record<string, unknown>,
      ) =>
        store.log.unshift({
          id: `00000000-0000-0000-0000-00000000f${store.log.length}00`,
          actor: me,
          action: a,
          before,
          after,
          created_at: now,
        });
      if (action === "propose" && r.treatment !== "accept") {
        Object.assign(r, {
          treatment: "accept",
          acceptance_status: "proposed",
          acceptance: {
            justification: body.justification,
            proposed_until: body.proposed_until,
            proposed_by: me,
            proposed_at: now,
            self_approved: false,
          },
        });
        audit(
          "acceptance_proposed",
          { treatment: "untreated" },
          {
            treatment: "accept",
            acceptance_status: "proposed",
            justification: body.justification,
            proposed_until: body.proposed_until,
          },
        );
        return route.fulfill(json({ risk: r }));
      }
      if (action === "approve" && r.acceptance_status === "proposed") {
        const acc = r.acceptance as Record<string, unknown>;
        if (acc.proposed_by === me) {
          return route.fulfill(
            json(
              {
                error: "risk: the proposer cannot approve their own acceptance",
              },
              403,
            ),
          );
        }
        Object.assign(r, {
          acceptance_status: "approved",
          accepted_until: body.accepted_until,
          accepter: me,
        });
        Object.assign(acc, { approved_at: now });
        audit(
          "acceptance_approved",
          { acceptance_status: "proposed" },
          {
            acceptance_status: "approved",
            accepted_until: body.accepted_until,
            accepter: me,
            self_approved: false,
          },
        );
        return route.fulfill(json({ risk: r }));
      }
      return route.fulfill(
        json({ error: "risk: transition not allowed" }, 409),
      );
    },
  );
}

test.describe("/risks/[id] acceptance (#2105)", () => {
  test("the owner proposes acceptance; a second user approves it from the risk page", async ({
    authedPage: page,
    browser,
    baseURL,
  }) => {
    const store = newStore();
    await mockRisk(page.context(), store, OWNER);

    // 1. The owner proposes.
    await page.goto(`/risks/${RISK_ID}`);
    const propose = page.getByTestId("risk-detail-propose-acceptance");
    await expect(propose).toBeVisible({ timeout: 30_000 });
    await expect(page.getByTestId("risk-detail-acceptance-card")).toHaveCount(
      0,
    );
    await propose.click();
    await expect(page).toHaveURL(
      new RegExp(`/risks/${RISK_ID}/acceptance/propose$`),
      { timeout: 30_000 },
    );
    // A justification is required.
    await page.getByTestId("risk-acceptance-submit").click();
    await expect(
      page.getByText("Say why the risk should be accepted."),
    ).toBeVisible();
    await page
      .getByTestId("risk-acceptance-justification")
      .fill("Compensated by the edge filter (test-2105)");
    await page.getByTestId("risk-acceptance-until").fill("2099-03-31");
    await page.getByTestId("risk-acceptance-submit").click();

    await expect(page).toHaveURL(new RegExp(`/risks/${RISK_ID}$`), {
      timeout: 30_000,
    });
    await expect(page.getByTestId("toast-viewport")).toContainText(
      "Acceptance proposed",
    );
    await expect(page.getByTestId("risk-detail-acceptance-status")).toHaveText(
      "awaiting approval",
    );
    await expect(
      page.getByTestId("risk-detail-acceptance-proposed-until"),
    ).toHaveText("Mar 31, 2099");
    expect(store.bodies[0]).toEqual({
      action: "propose",
      justification: "Compensated by the edge filter (test-2105)",
      proposed_until: "2099-03-31",
    });

    // 2. The owner cannot approve their own proposal.
    await page.getByTestId("risk-detail-approve-acceptance").click();
    await expect(page).toHaveURL(
      new RegExp(`/risks/${RISK_ID}/acceptance/approve$`),
    );
    await expect(page.getByTestId("risk-acceptance-until")).toHaveValue(
      "2099-03-31",
    );
    await page.getByTestId("risk-acceptance-submit").click();
    await expect(
      page.getByTestId("risk-acceptance-submit-error"),
    ).toContainText("cannot approve their own acceptance");
    expect(store.risk.acceptance_status).toBe("proposed");

    // 3. A second user approves from the risk page.
    const approverCtx = await browser.newContext({
      baseURL,
      storageState: await page.context().storageState(),
    });
    try {
      await mockRisk(approverCtx, store, APPROVER);
      const approver = await approverCtx.newPage();
      await approver.goto(`/risks/${RISK_ID}`);
      const approve = approver.getByTestId("risk-detail-approve-acceptance");
      await expect(approve).toBeVisible({ timeout: 30_000 });
      await approve.click();
      await expect(approver).toHaveURL(
        new RegExp(`/risks/${RISK_ID}/acceptance/approve$`),
      );
      await approver.getByTestId("risk-acceptance-submit").click();

      await expect(approver).toHaveURL(new RegExp(`/risks/${RISK_ID}$`), {
        timeout: 30_000,
      });
      await expect(approver.getByTestId("toast-viewport")).toContainText(
        "Acceptance approved",
      );
      await expect(approver.getByTestId("risk-detail-treatment")).toContainText(
        /approved/i,
      );
      await expect(
        approver.getByTestId("risk-detail-acceptance-accepted-until"),
      ).toHaveText("Mar 31, 2099");
      await expect(
        approver.getByTestId("risk-detail-acceptance-approved"),
      ).toContainText(APPROVER);
      await expect(
        approver.getByTestId("risk-detail-self-approved"),
      ).toHaveCount(0);
      // The header now leads with Revoke.
      await expect(
        approver.getByTestId("risk-detail-revoke-acceptance"),
      ).toBeVisible();
      expect(store.bodies.at(-1)).toEqual({
        action: "approve",
        accepted_until: "2099-03-31",
      });
      expect(store.log.map((e) => e.action)).toEqual([
        "acceptance_approved",
        "acceptance_proposed",
      ]);
    } finally {
      await approverCtx.close();
    }
  });
});
