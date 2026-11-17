// #2133 — saved views for the risk register.
//
// Save a view holding a filter, a sort and a chosen column; load the
// register afresh; apply the view; and the register is back to the same
// filters, sort and columns, sent to the API as the same parameters.
//
// MOCK STRATEGY: the /api/risks list BFF, the categories BFF and the
// org-units fetch are mocked, and `/api/saved-views` is an in-memory store
// that holds what the page POSTs and lists it back. The Go integration
// tier covers the platform side: the `risks` surface, its allow-list and
// per-user isolation
// (internal/api/controls/saved_views_risks_integration_test.go).
//
// Hard rule (P0-A9 / GitGuardian): every id is a neutral test string.

import type { Page, Request, Route } from "@playwright/test";

import { expect, test } from "./fixtures";

const VIEW_ID = "00000000-0000-4000-8000-000000002133";

function riskRow(id: string, title: string): Record<string, unknown> {
  return {
    id,
    title,
    description: "",
    category: "operational",
    category_name: "Operational",
    methodology: "nist_800_30",
    inherent_score: { likelihood: 4, impact: 5 },
    treatment: "mitigate",
    treatment_owner: "",
    owner_user_id: null,
    owner_unlinked: "",
    residual_score: { score: 12 },
    review_due_at: "2026-12-01T00:00:00Z",
    accepter: "",
    instrument_reference: "",
    linked_control_ids: [],
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    level: "team",
    themes: [],
    severity: 20,
    status: "open",
  };
}

const ROWS = [
  riskRow(
    "00000000-0000-4000-8000-00000002133a",
    "Test risk alpha (test-2133)",
  ),
  riskRow(
    "00000000-0000-4000-8000-00000002133b",
    "Test risk bravo (test-2133)",
  ),
];

type StoredView = {
  id: string;
  name: string;
  surface: string;
  filters: Record<string, string>;
};

function json(route: Route, body: unknown, status = 200) {
  return route.fulfill({
    status,
    contentType: "application/json",
    body: JSON.stringify(body),
  });
}

async function mockRegister(page: Page, views: StoredView[]) {
  const seen: URLSearchParams[] = [];
  await page.route("**/api/risks-hierarchy/org-units**", (route) =>
    json(route, { org_units: [] }),
  );
  await page.route(/\/api\/risks\/categories(\?.*)?$/, (route) =>
    json(route, { categories: [], count: 0 }),
  );
  await page.route(/\/api\/risks(\?.*)?$/, (route: Route, req: Request) => {
    const q = new URL(req.url()).searchParams;
    if (q.has("offset")) seen.push(q);
    return json(route, { risks: ROWS, count: ROWS.length, total: ROWS.length });
  });
  await page.route("**/api/saved-views**", (route: Route, req: Request) => {
    if (req.method() === "POST") {
      const body = req.postDataJSON() as Omit<StoredView, "id">;
      const view = { ...body, id: VIEW_ID };
      views.push(view);
      return json(route, view, 201);
    }
    if (req.method() === "GET") {
      const surface =
        new URL(req.url()).searchParams.get("surface") ?? "controls";
      return json(route, {
        views: views.filter((v) => v.surface === surface),
      });
    }
    return route.fallback();
  });
  return seen;
}

const last = (seen: URLSearchParams[]) =>
  seen[seen.length - 1] ?? new URLSearchParams();

test.describe("/risks — saved views (#2133)", () => {
  test("a saved view restores the filters, sort and columns after a reload", async ({
    authedPage: page,
  }) => {
    await page.setViewportSize({ width: 1316, height: 900 });
    const views: StoredView[] = [];
    const seen = await mockRegister(page, views);
    await page.goto("/risks");
    const table = page.getByTestId("list-table-wrap");
    await expect(table.getByTestId("risks-row-title")).toHaveCount(2, {
      timeout: 30_000,
    });

    // Nothing to save on the default register.
    await expect(page.getByTestId("risks-save-view-open")).toBeDisabled();

    // A filter, a sort and a column.
    await page
      .getByTestId("list-filter-pill-treatment")
      .locator("select")
      .selectOption("mitigate");
    await expect(page).toHaveURL(/[?&]treatment=mitigate/);
    await table.getByTestId("risks-sort-title").click();
    await expect(page).toHaveURL(/[?&]sort=-title/);
    await page.getByTestId("risks-column-chooser").locator("summary").click();
    await page.getByTestId("risks-column-toggle-methodology").click();
    await expect(page).toHaveURL(/[?&]cols=methodology/);

    // Save it.
    await page.getByTestId("risks-save-view-open").click();
    await page.getByTestId("risks-save-view-name").fill("Mitigating (2133)");
    await page.getByTestId("risks-save-view-confirm").click();
    await expect(page.getByTestId("risks-saved-views-select")).toHaveValue(
      VIEW_ID,
    );
    expect(views).toHaveLength(1);
    expect(views[0]!.surface).toBe("risks");
    expect(views[0]!.filters).toEqual({
      treatment: "mitigate",
      sort: "-title",
      cols: "methodology",
    });

    // A fresh load of the register: defaults again.
    await page.goto("/risks");
    await expect(table.getByTestId("risks-row-title")).toHaveCount(2, {
      timeout: 30_000,
    });
    await expect(table.getByTestId("risks-sort-methodology")).toHaveCount(0);
    await expect(
      page.getByTestId("list-filter-pill-treatment").locator("select"),
    ).toHaveValue("all");

    // Apply the view.
    await page
      .getByTestId("risks-saved-views-select")
      .selectOption({ label: "Mitigating (2133)" });
    await page.waitForURL(
      (u) =>
        u.searchParams.get("treatment") === "mitigate" &&
        u.searchParams.get("sort") === "-title" &&
        u.searchParams.get("cols") === "methodology",
    );
    await expect(
      page.getByTestId("list-filter-pill-treatment").locator("select"),
    ).toHaveValue("mitigate");
    await expect(table.getByTestId("risks-sort-title")).toHaveAttribute(
      "data-dir",
      "desc",
    );
    await expect(table.getByTestId("risks-sort-methodology")).toBeVisible();
    await expect
      .poll(() => {
        const q = last(seen);
        return `${q.get("treatment")} ${q.get("sort")}`;
      })
      .toBe("mitigate -title");
  });
});
