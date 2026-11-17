// #2119 — Playwright for the Approvals page and its sidebar badge.
//
//   1. The sidebar's Approvals badge shows the platform's total, and the
//      page lists the same number of rows, grouped by type, each linking
//      to the record's approval action. The caller's own request (listed
//      because self-approval is on) is marked.
//   2. Approving the exception from its page (inline confirm) removes it:
//      the badge drops by one while still on the exception page, and the
//      Approvals page lists one row fewer.
//
// MOCK STRATEGY: /api/approvals and the /api/exceptions BFFs are mocked on
// the browser context with one in-memory store, so an approval through the
// exception page removes the row from the approvals list as the platform
// would. Which rows the platform lists, and that it matches each approve
// gate, is covered for real against Postgres in
// internal/api/approvals_2119_integration_test.go. The sidebar, the
// exception page and the people directory are the real ones.
//
// Hard rule (P0-A9 / GitGuardian): every id is a neutral test string.

import type { Page, Request, Route } from "@playwright/test";

import { expect, seeded, test } from "./fixtures";

const EXCEPTION_ID = "00000000-0000-0000-0000-000000002119";
const RISK_ID = "00000000-0000-0000-0000-0000000021a9";
const POLICY_ID = "00000000-0000-0000-0000-0000000021b9";
const SCOPE_ID = "00000000-0000-0000-0000-0000000021c9";
const FV_ID = "00000000-0000-0000-0000-0000000021d9";
const REQUESTER = "00000000-0000-0000-0000-0000000021e9";

type Item = {
  type: "exception" | "risk_acceptance" | "policy" | "framework_scope";
  id: string;
  title: string;
  requested_by: string;
  requested_at?: string;
  self_approval: boolean;
  framework_version_id?: string;
};

function json(body: unknown, status = 200) {
  return {
    status,
    contentType: "application/json",
    body: JSON.stringify(body),
  };
}

function approvalsBody(items: Item[]) {
  const counts = {
    exception: 0,
    risk_acceptance: 0,
    policy: 0,
    framework_scope: 0,
  };
  for (const i of items) counts[i.type]++;
  return { items, counts, total: items.length, truncated: false };
}

async function mockApprovals(page: Page, store: { items: Item[] }) {
  const now = new Date().toISOString();
  const exceptionRow = {
    id: EXCEPTION_ID,
    control_id: seeded.controlId,
    scope_cell_predicate: { op: "true" },
    justification: "Vendor cannot support SSO until Q2 (test-2119)",
    compensating_controls: ["Weekly access review"],
    requested_by: REQUESTER,
    requested_at: now,
    self_approved: false,
    expires_at: new Date(Date.now() + 30 * 86_400_000).toISOString(),
    status: "requested",
    created_at: now,
    updated_at: now,
  };
  await page
    .context()
    .route(/\/api\/approvals(\?|$)/, (route: Route) =>
      route.fulfill(json(approvalsBody(store.items))),
    );
  await page
    .context()
    .route(
      /\/api\/exceptions\/[^/]+(\/[^/?]+)?(\?|$)/,
      async (route: Route, req: Request) => {
        const parts = new URL(req.url()).pathname.split("/").filter(Boolean);
        if (parts[2] !== EXCEPTION_ID) return route.fallback();
        if (parts[3] === "audit-log") {
          return route.fulfill(json({ entries: [], count: 0 }));
        }
        if (parts[3] === "approve" && req.method() === "PATCH") {
          Object.assign(exceptionRow, {
            status: "approved",
            approved_by: "approver",
            approved_at: new Date().toISOString(),
          });
          store.items = store.items.filter((i) => i.id !== EXCEPTION_ID);
          return route.fulfill(json({ exception: exceptionRow }));
        }
        if (parts.length === 3 && req.method() === "GET") {
          return route.fulfill(json({ exception: exceptionRow }));
        }
        return route.fallback();
      },
    );
}

test.describe("Approvals page and sidebar badge (#2119)", () => {
  test("the badge matches the page; approving an item removes it and decrements the badge", async ({
    authedPage: page,
  }) => {
    const at = new Date(Date.now() - 86_400_000).toISOString();
    const store: { items: Item[] } = {
      items: [
        {
          type: "exception",
          id: EXCEPTION_ID,
          title: "Exception on a seeded control",
          requested_by: REQUESTER,
          requested_at: at,
          self_approval: false,
        },
        {
          type: "risk_acceptance",
          id: RISK_ID,
          title: "Legacy VPN appliance",
          requested_by: REQUESTER,
          requested_at: at,
          self_approval: false,
        },
        {
          type: "policy",
          id: POLICY_ID,
          title: "Access control v1.0",
          requested_by: REQUESTER,
          requested_at: at,
          self_approval: true,
        },
        {
          type: "framework_scope",
          id: SCOPE_ID,
          title: "SOC 2 system",
          requested_by: REQUESTER,
          requested_at: at,
          self_approval: false,
          framework_version_id: FV_ID,
        },
      ],
    };
    await mockApprovals(page, store);

    // 1. The badge and the page agree.
    await page.goto("/approvals");
    const sidebar = page.getByTestId("sidebar-desktop");
    const badge = sidebar.getByTestId("sidebar-approvals-count");
    await expect(badge).toHaveText("4", { timeout: 30_000 });
    await expect(badge).toHaveAttribute(
      "aria-label",
      "4 waiting for your approval",
    );
    const rows = page.getByTestId("approvals-row");
    await expect(rows).toHaveCount(4);
    for (const type of [
      "exception",
      "risk_acceptance",
      "policy",
      "framework_scope",
    ]) {
      await expect(page.getByTestId(`approvals-count-${type}`)).toHaveText("1");
    }
    // Each row links to its approval action; the self-approval is marked.
    await expect(
      page.getByTestId(`approvals-row-link-${RISK_ID}`),
    ).toHaveAttribute("href", `/risks/${RISK_ID}/acceptance/approve`);
    await expect(
      page.getByTestId(`approvals-row-link-${POLICY_ID}`),
    ).toHaveAttribute("href", `/policies/${POLICY_ID}`);
    await expect(
      page.getByTestId(`approvals-row-link-${SCOPE_ID}`),
    ).toHaveAttribute("href", `/framework-scopes/${FV_ID}`);
    await expect(page.getByTestId("approvals-row-self")).toHaveCount(1);
    await expect(
      page
        .getByTestId(`approvals-row-link-${POLICY_ID}`)
        .getByTestId("approvals-row-self"),
    ).toBeVisible();

    // 2. Approve the exception from its page.
    await page.getByTestId(`approvals-row-link-${EXCEPTION_ID}`).click();
    await expect(page).toHaveURL(new RegExp(`/exceptions/${EXCEPTION_ID}$`));
    await expect(page.getByTestId("exception-status")).toHaveText("requested", {
      timeout: 30_000,
    });
    await page.getByTestId("exception-approve").click();
    await page.getByTestId("exception-approve-confirm-ok").click();
    await expect(page.getByTestId("exception-status")).toHaveText("approved");
    // The badge drops by one without leaving the page.
    await expect(badge).toHaveText("3");

    // The Approvals page lists one row fewer, and no exception group.
    await sidebar.getByRole("link", { name: /Approvals/ }).click();
    await expect(page).toHaveURL(/\/approvals$/);
    await expect(rows).toHaveCount(3);
    await expect(page.getByTestId("approvals-group-exception")).toHaveCount(0);
    await expect(badge).toHaveText("3");
  });

  test("nothing waiting: no badge and an empty page", async ({
    authedPage: page,
  }) => {
    await mockApprovals(page, { items: [] });
    await page.goto("/approvals");
    await expect(page.getByTestId("approvals-empty")).toBeVisible({
      timeout: 30_000,
    });
    await expect(
      page
        .getByTestId("sidebar-desktop")
        .getByTestId("sidebar-approvals-count"),
    ).toHaveCount(0);
  });
});
