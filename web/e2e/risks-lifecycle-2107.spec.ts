// #2107 — a risk owner closes a risk, sees it leave the register, finds it
// under "Show closed", and reopens it.
//
//   1. The register lists three risks.
//   2. The owner opens one, picks "Close risk…" from the header's overflow,
//      is told a reason is required, chooses one with a note, and returns
//      to the risk with a toast. The page marks it Closed and shows the
//      reason and note.
//   3. Back on the register, the risk is gone and the count is two. With
//      the Status filter on "Open and closed" (#2132) it is back, marked
//      Closed, and the count is three.
//   4. From its page the owner reopens it (one click, confirmed inline).
//      The register lists it again without the toggle.
//
// MOCK STRATEGY: the /api/risks BFFs are mocked with one in-memory store.
// The register mock reads ?status= as upstream does (#2132: open by
// default, closed or all on request); the Go tier covers the real filtering, the state machine
// and the audit rows against Postgres
// (internal/api/risks/lifecycle_integration_test.go).
//
// Hard rule (P0-A9 / GitGuardian): every id is a neutral test string.

import type { Page, Request, Route } from "@playwright/test";

import { expect, test } from "./fixtures";

const RISK_ID = "00000000-0000-4000-8000-000000002107";
const OTHER_A = "00000000-0000-4000-8000-0000000021a1";
const OTHER_B = "00000000-0000-4000-8000-0000000021a2";
const TITLE = "Test risk for closing (test-2107)";

function riskRow(id: string, title: string): Record<string, unknown> {
  return {
    id,
    title,
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
    status: "open",
  };
}

type Store = {
  risks: Record<string, unknown>[];
  bodies: Record<string, unknown>[];
};

function json(body: unknown, status = 200) {
  return {
    status,
    contentType: "application/json",
    body: JSON.stringify(body),
  };
}

async function mockRisks(page: Page, store: Store) {
  const target = () => store.risks.find((r) => r.id === RISK_ID)!;
  await page.route(/\/api\/risks(\?.*)?$/, (route: Route, req: Request) => {
    const status = new URL(req.url()).searchParams.get("status") ?? "open";
    const risks =
      status === "all"
        ? store.risks
        : store.risks.filter((r) => r.status === status);
    return route.fulfill(json({ risks, total: risks.length }));
  });
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
    route.fulfill(json({ action_plans: [] })),
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
    route.fulfill(json({ risk: target() })),
  );
  await page.route(
    `**/api/risks/${RISK_ID}/close`,
    (route: Route, req: Request) => {
      const body = (req.postDataJSON() ?? {}) as Record<string, string>;
      store.bodies.push({ action: "close", ...body });
      const r = target();
      if (r.status !== "open") {
        return route.fulfill(
          json({ error: "risk: only an open risk can be closed" }, 409),
        );
      }
      Object.assign(r, {
        status: "closed",
        closure: {
          reason: body.reason,
          note: body.note,
          closed_at: new Date().toISOString(),
          closed_by: "owner-a",
        },
      });
      return route.fulfill(json({ risk: r }));
    },
  );
  await page.route(`**/api/risks/${RISK_ID}/reopen`, (route: Route) => {
    store.bodies.push({ action: "reopen" });
    const r = target();
    if (r.status !== "closed") {
      return route.fulfill(
        json({ error: "risk: only a closed risk can be reopened" }, 409),
      );
    }
    r.status = "open";
    delete r.closure;
    return route.fulfill(json({ risk: r }));
  });
}

test.describe("/risks lifecycle (#2107)", () => {
  test("close a risk, find it under Show closed, reopen it", async ({
    authedPage: page,
  }) => {
    const store: Store = {
      risks: [
        riskRow(RISK_ID, TITLE),
        riskRow(OTHER_A, "Test risk A (test-2107)"),
        riskRow(OTHER_B, "Test risk B (test-2107)"),
      ],
      bodies: [],
    };
    await mockRisks(page, store);

    // 1. The register lists three risks.
    await page.goto("/risks");
    const count = page.getByTestId("risks-count-label");
    await expect(count).toHaveText("3 risks", { timeout: 30_000 });
    // ListTable renders the desktop table and a hidden mobile card stack;
    // count only what is on screen.
    const row = page
      .getByTestId("risks-row-title")
      .filter({ hasText: TITLE, visible: true });
    await expect(row).toHaveCount(1);

    // 2. Close it from its page.
    await page.goto(`/risks/${RISK_ID}`);
    const overflow = page.getByTestId("detail-header-overflow-trigger");
    await expect(overflow).toBeVisible({ timeout: 30_000 });
    await expect(page.getByTestId("risk-detail-closed")).toHaveCount(0);
    await overflow.click();
    await page.getByTestId("risk-detail-close").click();
    await expect(page).toHaveURL(new RegExp(`/risks/${RISK_ID}/close$`), {
      timeout: 30_000,
    });
    await page.getByTestId("risk-close-submit").click();
    await expect(page.getByTestId("risk-close-field-error")).toHaveText(
      "Choose why the risk is closed.",
    );
    await page.getByTestId("risk-close-reason-not_applicable").check();
    await page.getByTestId("risk-close-note").fill("Site decommissioned.");
    await page.getByTestId("risk-close-submit").click();

    await expect(page).toHaveURL(new RegExp(`/risks/${RISK_ID}$`), {
      timeout: 30_000,
    });
    await expect(page.getByTestId("toast-viewport")).toContainText(
      "Risk closed",
    );
    await expect(page.getByTestId("risk-detail-closed")).toBeVisible();
    await expect(page.getByTestId("risk-detail-closure")).toContainText(
      "No longer applicable",
    );
    await expect(page.getByTestId("risk-detail-closure-note")).toHaveText(
      "Site decommissioned.",
    );
    expect(store.bodies[0]).toEqual({
      action: "close",
      reason: "not_applicable",
      note: "Site decommissioned.",
    });

    // 3. It leaves the register, and comes back with the Status filter.
    await page.goto("/risks");
    await expect(count).toHaveText("2 risks", { timeout: 30_000 });
    await expect(row).toHaveCount(0);
    const status = page
      .getByTestId("list-filter-pill-status")
      .locator("select");
    await expect(status).toHaveValue("open");
    await status.selectOption("all");
    await expect(page).toHaveURL(/[?&]status=all/);
    await expect(count).toHaveText("3 risks");
    await expect(row).toHaveCount(1);
    await expect(
      page.getByTestId("risks-row-closed").filter({ visible: true }),
    ).toHaveCount(1);

    // 4. Reopen it from its page.
    await row.click();
    await expect(page).toHaveURL(new RegExp(`/risks/${RISK_ID}$`), {
      timeout: 30_000,
    });
    await page.getByTestId("risk-detail-reopen").click();
    await page.getByTestId("risk-detail-reopen-confirm-ok").click();
    await expect(page.getByTestId("toast-viewport")).toContainText(
      "Risk reopened",
    );
    await expect(page.getByTestId("risk-detail-closed")).toHaveCount(0);
    await expect(page.getByTestId("risk-detail-closure")).toHaveCount(0);
    expect(store.bodies[1]).toEqual({ action: "reopen" });

    await page.goto("/risks");
    await expect(count).toHaveText("3 risks", { timeout: 30_000 });
    await expect(row).toHaveCount(1);
  });
});
