// #2113 — Playwright for the exception lifecycle in the app.
//
//   1. A requester opens a control, follows "Request exception" to the
//      form with the control pre-filled, and files the exception. While
//      "choose by dimension" has nothing picked the picker's value is
//      null, and the form cannot be submitted; the request sends the
//      whole control as {"op":"true"}, never null.
//   2. The requester cannot approve their own exception: the platform's
//      refusal shows as a toast and the exception stays requested.
//   3. A second user, in their own browser context, approves it (inline
//      confirm) and activates it (its own route); the exceptions list
//      shows it active.
//   4. The second user revokes it with a reason; it shows revoked.
//
// MOCK STRATEGY: the /api/exceptions BFFs are mocked on each browser
// context with one shared in-memory store, so the two users see the same
// exception. Each context's mock answers as its own person, and approve
// applies the approval rule (approver ≠ requester), which the Go tier
// covers for real against Postgres (internal/exception). The control page
// and the controls list are the real seeded ones.
//
// Hard rule (P0-A9 / GitGuardian): every id is a neutral test string.

import type { BrowserContext, Request, Route } from "@playwright/test";

import { expect, seeded, test } from "./fixtures";

const EXCEPTION_ID = "00000000-0000-0000-0000-000000002113";
const REQUESTER = "00000000-0000-0000-0000-0000000021a1";
const APPROVER = "00000000-0000-0000-0000-0000000021b2";

type Row = Record<string, unknown> & { id: string; status: string };
type Entry = Record<string, unknown>;

type Store = {
  row: Row | null;
  log: Entry[];
  created: unknown[];
};

function json(body: unknown, status = 200) {
  return {
    status,
    contentType: "application/json",
    body: JSON.stringify(body),
  };
}

function record(
  store: Store,
  action: string,
  actor: string,
  to: string,
  reason = "",
) {
  const from = store.log.length ? (store.row?.status as string) : undefined;
  store.log.push({
    id: `00000000-0000-0000-0000-00000000e${store.log.length}00`,
    action,
    actor,
    ...(from ? { from_state: from } : {}),
    to_state: to,
    reason,
    occurred_at: new Date().toISOString(),
    exception_id: EXCEPTION_ID,
  });
}

/** Mocks the exception BFFs on `ctx`, answering as `me`. */
async function mockExceptions(ctx: BrowserContext, store: Store, me: string) {
  await ctx.route(
    /\/api\/exceptions(\/|\?|$)/,
    async (route: Route, req: Request) => {
      const url = new URL(req.url());
      const parts = url.pathname.split("/").filter(Boolean); // api, exceptions, ...
      const method = req.method();
      const now = new Date().toISOString();

      if (parts.length === 2 && method === "GET") {
        return route.fulfill(
          json({
            exceptions: store.row ? [store.row] : [],
            count: store.row ? 1 : 0,
          }),
        );
      }
      if (parts.length === 2 && method === "POST") {
        const body = req.postDataJSON() as Record<string, unknown>;
        store.created.push(body);
        if (body.scope_cell_predicate === null) {
          return route.fulfill(
            json({ error: "scope_cell_predicate must not be null" }, 400),
          );
        }
        store.row = {
          id: EXCEPTION_ID,
          control_id: body.control_id as string,
          scope_cell_predicate: body.scope_cell_predicate,
          justification: body.justification,
          compensating_controls: body.compensating_controls,
          requested_by: me,
          requested_at: now,
          self_approved: false,
          expires_at: body.expires_at,
          status: "requested",
          created_at: now,
          updated_at: now,
        };
        record(store, "requested", me, "requested");
        return route.fulfill(json({ exception: store.row }, 201));
      }
      if (parts[2] !== EXCEPTION_ID || !store.row) {
        return route.fulfill(json({ error: "exception not found" }, 404));
      }
      const row = store.row;
      if (parts.length === 3 && method === "GET") {
        return route.fulfill(json({ exception: row }));
      }
      if (parts[3] === "audit-log" && method === "GET") {
        return route.fulfill(
          json({ entries: store.log, count: store.log.length }),
        );
      }
      if (method === "PATCH") {
        const t = parts[3];
        const body = (req.postDataJSON() ?? {}) as Record<string, string>;
        const from: Record<string, string> = {
          approve: "requested",
          deny: "requested",
          activate: "approved",
          revoke: "active",
        };
        if (row.status !== from[t]) {
          return route.fulfill(
            json(
              { error: "exception not in expected state for this transition" },
              409,
            ),
          );
        }
        if (t === "approve") {
          if (row.requested_by === me) {
            return route.fulfill(
              json(
                {
                  error:
                    "approver must differ from requester (segregation of duties)",
                },
                403,
              ),
            );
          }
          record(store, "approved", me, "approved");
          Object.assign(row, {
            status: "approved",
            approved_by: me,
            approved_at: now,
          });
        } else if (t === "activate") {
          record(store, "activated", me, "active");
          Object.assign(row, {
            status: "active",
            activated_by: me,
            activated_at: now,
            effective_from: body.effective_from ?? now,
          });
        } else if (t === "revoke") {
          record(store, "revoked", me, "revoked", body.reason);
          Object.assign(row, {
            status: "revoked",
            revoked_by: me,
            revoked_at: now,
          });
        }
        row.updated_at = now;
        return route.fulfill(json({ exception: row }));
      }
      return route.fallback();
    },
  );
}

test.describe("exception lifecycle in the app (#2113)", () => {
  test("request from a control; a second user approves and activates; the list shows it active", async ({
    authedPage: page,
    browser,
    baseURL,
  }) => {
    const store: Store = { row: null, log: [], created: [] };
    await mockExceptions(page.context(), store, REQUESTER);

    // 1. Request from the control.
    await page.goto(`/controls/${seeded.controlId}`);
    const requestLink = page.getByTestId("control-action-request-exception");
    await expect(requestLink).toBeVisible({ timeout: 30_000 });
    await requestLink.click();
    await expect(page).toHaveURL(
      new RegExp(`/exceptions/new\\?control_id=${seeded.controlId}`),
    );
    await expect(page.getByTestId("exception-request-control")).toHaveValue(
      seeded.controlId,
    );

    // Choose by dimension with nothing picked: the picker reports null
    // and the form refuses to submit.
    const submit = page.getByTestId("exception-request-submit");
    await page.getByTestId("scope-picker-dimensions").check();
    await expect(submit).toBeDisabled();
    // Back to Everything, the whole control.
    await page.getByTestId("scope-picker-everything").check();
    await expect(submit).toBeEnabled();

    await page
      .getByTestId("exception-request-justification")
      .fill("Vendor cannot support SSO until Q2 (test-2113)");
    await page
      .getByTestId("exception-request-compensating")
      .fill("Weekly access review");
    await submit.click();

    await expect(page).toHaveURL(new RegExp(`/exceptions/${EXCEPTION_ID}$`));
    await expect(page.getByTestId("toast-viewport")).toContainText(
      "Exception requested",
    );
    await expect(page.getByTestId("exception-status")).toHaveText("requested");
    // #2600: the always-true predicate reads as the whole control.
    await expect(
      page.getByTestId("exception-scope-whole-control"),
    ).toContainText("Whole control");
    expect(store.created).toHaveLength(1);
    expect(store.created[0]).toMatchObject({
      control_id: seeded.controlId,
      scope_cell_predicate: { op: "true" },
      compensating_controls: ["Weekly access review"],
    });

    // 2. The requester cannot approve their own exception.
    await page.getByTestId("exception-approve").click();
    await page.getByTestId("exception-approve-confirm-ok").click();
    await expect(page.getByTestId("toast-viewport")).toContainText(
      "segregation of duties",
    );
    await expect(page.getByTestId("exception-status")).toHaveText("requested");

    // 3. A second user approves and activates.
    const approverCtx = await browser.newContext({
      baseURL,
      storageState: await page.context().storageState(),
    });
    try {
      await mockExceptions(approverCtx, store, APPROVER);
      const approver = await approverCtx.newPage();
      await approver.goto(`/exceptions/${EXCEPTION_ID}`);
      await expect(approver.getByTestId("exception-status")).toHaveText(
        "requested",
        { timeout: 30_000 },
      );

      await approver.getByTestId("exception-approve").click();
      await approver.getByTestId("exception-approve-confirm-ok").click();
      await expect(approver.getByTestId("exception-status")).toHaveText(
        "approved",
      );

      await approver.getByTestId("exception-activate").click();
      await expect(approver).toHaveURL(
        new RegExp(`/exceptions/${EXCEPTION_ID}/activate$`),
      );
      await approver.getByTestId("exception-action-submit").click();
      await expect(approver).toHaveURL(
        new RegExp(`/exceptions/${EXCEPTION_ID}$`),
      );
      await expect(approver.getByTestId("exception-status")).toHaveText(
        "active",
      );

      // The list shows it active.
      await approver.goto("/exceptions");
      await expect(approver.getByTestId("exceptions-row-status")).toHaveText(
        "active",
        { timeout: 30_000 },
      );

      // 4. Revoke, with a reason.
      await approver.getByTestId("exceptions-row-status").click();
      await expect(approver).toHaveURL(
        new RegExp(`/exceptions/${EXCEPTION_ID}$`),
      );
      await approver.getByTestId("exception-revoke").click();
      await approver.getByTestId("exception-action-submit").click();
      await expect(approver.getByText("Give a reason.")).toBeVisible();
      await approver
        .getByTestId("exception-action-reason")
        .fill("MFA rolled out early");
      await approver.getByTestId("exception-action-submit").click();
      await expect(approver.getByTestId("exception-status")).toHaveText(
        "revoked",
      );
      await expect(
        approver.getByTestId("exception-audit-entry").last(),
      ).toContainText("MFA rolled out early");
    } finally {
      await approverCtx.close();
    }
  });
});
