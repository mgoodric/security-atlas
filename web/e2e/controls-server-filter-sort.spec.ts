// #2131 — /controls filters, sorts and pages on the server. Choosing a
// state, clicking a column header, and applying a saved view each change
// the request the page sends to `/api/controls-list?include=state`; the
// page renders the rows that request returns, in the order it returns
// them, and does no filtering or sorting of its own.
//
// HERMETIC: the list, saved-view and scope-cell BFFs are route-mocked
// (slice 594 convention). The fake list BFF applies `result` and `sort`
// the way the platform does, so a page that still filtered or sorted in
// the browser would show the same rows; the assertions on the requests
// are what prove the work moved to the server. The SQL behind the
// parameters is pinned by
// internal/api/controls/list_filter_sort_integration_test.go.

import type { Page, Route } from "@playwright/test";

import { expect, test } from "./fixtures";

// Neutral test strings only (P0-A9 / GitGuardian).
const id = (n: number) =>
  `c2131000-0000-4000-8000-${String(n).padStart(12, "0")}`;
const state = (result: string, observed: string | null) => ({
  result,
  freshness_status: "fresh",
  last_observed_at: observed,
  evaluated_at: "2026-11-15T00:00:00Z",
});
const base = {
  control_family: "Test (2131)",
  lifecycle_state: "active",
  scf_anchor_id: null,
  frameworks: [],
};
const controls = [
  {
    ...base,
    id: id(1),
    title: "Pass recent (2131)",
    scf_id: "TST-01",
    bundle_id: "c2131-1",
    state: state("pass", "2026-11-14T00:00:00Z"),
  },
  {
    ...base,
    id: id(2),
    title: "Fail old (2131)",
    scf_id: "TST-02",
    bundle_id: "c2131-2",
    state: state("fail", "2026-10-01T00:00:00Z"),
  },
  {
    ...base,
    id: id(3),
    title: "Fail recent (2131)",
    scf_id: "TST-03",
    bundle_id: "c2131-3",
    state: state("fail", "2026-11-10T00:00:00Z"),
  },
  {
    ...base,
    id: id(4),
    title: "Pass old (2131)",
    scf_id: "TST-04",
    bundle_id: "c2131-4",
    state: state("pass", "2026-09-01T00:00:00Z"),
  },
];

const rank: Record<string, number> = { fail: 5, pass: 2 };

// What the platform would return for one request.
function respond(q: URLSearchParams) {
  let rows = [...controls];
  const result = q.get("result");
  if (result) rows = rows.filter((c) => c.state.result === result);
  const sort = q.get("sort") ?? "";
  const desc = sort.startsWith("-");
  const key = desc ? sort.slice(1) : sort;
  const observed = (c: (typeof controls)[number]) =>
    c.state.last_observed_at ?? "";
  if (key === "last_observed_at") {
    rows.sort((a, b) => observed(a).localeCompare(observed(b)));
    if (desc) rows.reverse();
  } else {
    // Default: failing first, then the stalest evidence.
    rows.sort(
      (a, b) =>
        rank[b.state.result] - rank[a.state.result] ||
        observed(a).localeCompare(observed(b)),
    );
  }
  const limit = Number(q.get("limit") ?? 100);
  const offset = Number(q.get("offset") ?? 0);
  const page = rows.slice(offset, offset + limit);
  return {
    controls: page,
    count: page.length,
    total: rows.length,
    families: ["Test (2131)"],
  };
}

const VIEW_ID = "5a2131aa-0000-4000-8000-000000000001";

async function mockBFFs(page: Page, seen: URLSearchParams[]) {
  await page.route("**/api/controls**", async (route: Route) => {
    const url = new URL(route.request().url());
    if (url.pathname === "/api/controls-list") {
      if (url.searchParams.get("limit") !== "1") seen.push(url.searchParams);
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(respond(url.searchParams)),
      });
      return;
    }
    if (url.pathname === "/api/controls") {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          anchors: [],
          total: 0,
          catalog_total: 1451,
          with_control_total: 4,
        }),
      });
      return;
    }
    await route.fallback();
  });
  await page.route("**/api/saved-views**", async (route) => {
    if (route.request().method() !== "GET") {
      await route.fallback();
      return;
    }
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        views: [
          {
            id: VIEW_ID,
            name: "Failing, newest evidence (2131)",
            filters: { result: "fail", sort: "-last_observed_at" },
          },
        ],
      }),
    });
  });
  await page.route("**/api/scope-cells**", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ cells: [] }),
    });
  });
}

async function titles(page: Page): Promise<string[]> {
  const table = page.getByTestId("list-table-wrap");
  return table
    .getByTestId("list-table-row")
    .evaluateAll((rows) =>
      rows.map(
        (r) => r.querySelector("td:nth-child(3)")?.textContent?.trim() ?? "",
      ),
    );
}

const last = (seen: URLSearchParams[]) => seen[seen.length - 1];

test.describe("/controls filters and sorts on the server (#2131)", () => {
  test("the default order is the server's: failing first, then stalest evidence", async ({
    authedPage: page,
  }) => {
    const seen: URLSearchParams[] = [];
    await mockBFFs(page, seen);
    await page.goto("/controls");
    const table = page.getByTestId("list-table-wrap");
    await expect(table.getByTestId("list-table-row")).toHaveCount(4);
    await expect
      .poll(() => titles(page))
      .toEqual([
        "Fail old (2131)",
        "Fail recent (2131)",
        "Pass old (2131)",
        "Pass recent (2131)",
      ]);
    // One page was asked for, with no sort: the platform's default.
    expect(last(seen).get("sort")).toBeNull();
    expect(last(seen).get("limit")).toBe("50");
    expect(last(seen).get("offset")).toBe("0");
  });

  test("filtering by state=failing and sorting by last observed hit the API", async ({
    authedPage: page,
  }) => {
    const seen: URLSearchParams[] = [];
    await mockBFFs(page, seen);
    await page.goto("/controls");
    const table = page.getByTestId("list-table-wrap");
    await expect(table.getByTestId("list-table-row")).toHaveCount(4);

    await page.getByLabel("State", { exact: true }).selectOption("fail");
    await page.waitForURL((u) => u.searchParams.get("result") === "fail");
    await expect.poll(() => last(seen).get("result")).toBe("fail");
    await expect(table.getByTestId("list-table-row")).toHaveCount(2);
    await expect(page.getByTestId("controls-count-label")).toContainText(
      "2 of 4 controls",
    );

    // Last observed: ascending, then descending, each a new request.
    const header = page.getByTestId("controls-sort-last_observed_at");
    await header.click();
    await page.waitForURL(
      (u) => u.searchParams.get("sort") === "last_observed_at",
    );
    await expect.poll(() => last(seen).get("sort")).toBe("last_observed_at");
    await expect(header).toHaveAttribute("data-sort", "ascending");
    await expect
      .poll(() => titles(page))
      .toEqual(["Fail old (2131)", "Fail recent (2131)"]);

    await header.click();
    await page.waitForURL(
      (u) => u.searchParams.get("sort") === "-last_observed_at",
    );
    await expect.poll(() => last(seen).get("sort")).toBe("-last_observed_at");
    await expect(header).toHaveAttribute("data-sort", "descending");
    await expect
      .poll(() => titles(page))
      .toEqual(["Fail recent (2131)", "Fail old (2131)"]);
    // The filter rode along on every sorted request.
    expect(last(seen).get("result")).toBe("fail");
  });

  test("a saved view applies its filter and sort as server parameters", async ({
    authedPage: page,
  }) => {
    const seen: URLSearchParams[] = [];
    await mockBFFs(page, seen);
    await page.goto("/controls");
    const table = page.getByTestId("list-table-wrap");
    await expect(table.getByTestId("list-table-row")).toHaveCount(4);

    await page
      .getByTestId("controls-saved-views-select")
      .selectOption({ label: "Failing, newest evidence (2131)" });
    await page.waitForURL(
      (u) =>
        u.searchParams.get("result") === "fail" &&
        u.searchParams.get("sort") === "-last_observed_at",
    );
    await expect
      .poll(() => {
        const q = last(seen);
        return `${q.get("result")} ${q.get("sort")}`;
      })
      .toBe("fail -last_observed_at");
    await expect
      .poll(() => titles(page))
      .toEqual(["Fail recent (2131)", "Fail old (2131)"]);
    await expect(page.getByLabel("State", { exact: true })).toHaveValue("fail");
  });
});
