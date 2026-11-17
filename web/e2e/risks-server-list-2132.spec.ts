// #2132 — the risk register is filtered, sorted and paged on the server.
//
//   1. At 1316px the default columns (ID, Title, Category, Owner,
//      Residual, Treatment, Review due, Status) show with no sideways
//      scroll, and the first request carries no sort or status: the
//      platform's defaults (residual descending, open risks).
//   2. The column chooser adds a column.
//   3. A header click sends `sort` to the API, and the rows render in the
//      order the API returned them: the browser does not re-sort.
//   4. Each filter is sent to the API as its own parameter.
//   5. Closed risks are hidden by default and shown with the Status filter.
//   6. A row opens its risk.
//
// MOCK STRATEGY: the /api/risks list BFF, the categories BFF and the
// org-units fetch are mocked, and every list request is recorded. The Go
// integration tier covers what the platform does with each parameter
// (internal/api/risks/list_page_integration_test.go).
//
// Hard rule (P0-A9 / GitGuardian): every id is a neutral test string.

import type { Page, Request, Route } from "@playwright/test";

import { expect, test } from "./fixtures";

const OPEN_A = "00000000-0000-4000-8000-00000002132a";
const OPEN_B = "00000000-0000-4000-8000-00000002132b";
const OPEN_C = "00000000-0000-4000-8000-00000002132c";
const CLOSED = "00000000-0000-4000-8000-00000002132d";

function riskRow(
  id: string,
  title: string,
  overrides: Record<string, unknown> = {},
): Record<string, unknown> {
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
    ...overrides,
  };
}

// Long titles and unlinked owners: the widest content the default
// columns hold.
const OPEN_ROWS = [
  riskRow(
    OPEN_A,
    "Test risk alpha: customer data exported to an unmanaged analytics workspace (test-2132)",
    { owner_unlinked: "Security engineering on-call rotation" },
  ),
  riskRow(OPEN_B, "Test risk bravo (test-2132)", {
    residual_score: { score: 4 },
    treatment: "accept",
    acceptance_status: "proposed",
  }),
  riskRow(OPEN_C, "Test risk charlie (test-2132)", {
    residual_score: {},
    review_due_at: "2026-01-02T00:00:00Z",
  }),
];
const CLOSED_ROW = riskRow(CLOSED, "Test risk closed (test-2132)", {
  status: "closed",
  closure: {
    reason: "not_applicable",
    closed_at: "2026-02-01T00:00:00Z",
    closed_by: "owner-a",
  },
});

function json(route: Route, body: unknown) {
  return route.fulfill({
    status: 200,
    contentType: "application/json",
    body: JSON.stringify(body),
  });
}

// mockRegister records the query of every register request. The mock
// filters only on status (so the closed test reads as the platform
// behaves) and reverses the rows for any `sort`, so a render in the
// returned order proves the browser did not sort.
async function mockRegister(page: Page): Promise<URLSearchParams[]> {
  const seen: URLSearchParams[] = [];
  await page.route("**/api/risks-hierarchy/org-units**", (route) =>
    json(route, { org_units: [] }),
  );
  await page.route(/\/api\/risks\/categories(\?.*)?$/, (route) =>
    json(route, {
      categories: [
        {
          id: "cat-operational",
          slug: "operational",
          name: "Operational",
          description: "",
          archived: false,
          is_default: true,
          risk_count: 3,
          rule_count: 0,
        },
        {
          id: "cat-privacy",
          slug: "privacy",
          name: "Privacy",
          description: "",
          archived: false,
          is_default: false,
          risk_count: 0,
          rule_count: 0,
        },
      ],
      count: 2,
    }),
  );
  await page.route(/\/api\/risks(\?.*)?$/, (route: Route, req: Request) => {
    const q = new URL(req.url()).searchParams;
    // The sidebar badge and the "N of M" size read the same BFF with
    // limit=1 and no offset (#2490); only the register page sends offset.
    if (q.has("offset")) seen.push(q);
    const status = q.get("status") ?? "open";
    let rows =
      status === "all"
        ? [...OPEN_ROWS, CLOSED_ROW]
        : status === "closed"
          ? [CLOSED_ROW]
          : [...OPEN_ROWS];
    if (q.get("sort")) rows = [...rows].reverse();
    return json(route, { risks: rows, count: rows.length, total: rows.length });
  });
  return seen;
}

function titles(page: Page) {
  return page
    .getByTestId("list-table-wrap")
    .getByTestId("risks-row-title")
    .allInnerTexts();
}

function lastQuery(seen: URLSearchParams[]): URLSearchParams {
  return seen[seen.length - 1] ?? new URLSearchParams();
}

const DEFAULT_HEADERS = [
  "id",
  "title",
  "category",
  "owner",
  "residual",
  "treatment",
  "review_due",
  "status",
];

test.describe("/risks — server-side register (#2132)", () => {
  test("at 1316px the default columns show without sideways scroll", async ({
    authedPage: page,
  }) => {
    await page.setViewportSize({ width: 1316, height: 900 });
    const seen = await mockRegister(page);
    await page.goto("/risks");
    const table = page.getByTestId("list-table-wrap");
    await expect(table.getByTestId("risks-row-title")).toHaveCount(3, {
      timeout: 30_000,
    });

    for (const key of DEFAULT_HEADERS) {
      await expect(table.getByTestId(`risks-sort-${key}`)).toBeVisible();
    }
    await expect(table.getByTestId("risks-sort-methodology")).toHaveCount(0);

    // The platform's defaults are not sent.
    const first = seen[0]!;
    expect(first.get("sort")).toBeNull();
    expect(first.get("status")).toBeNull();
    expect(first.get("limit")).toBe("50");
    expect(first.get("offset")).toBe("0");

    const overflow = await page.evaluate(() => {
      const wrap = document.querySelector('[data-testid="list-table-wrap"]')!;
      const tbl = wrap.querySelector("table")!;
      const doc = document.scrollingElement!;
      return {
        page: doc.scrollWidth - doc.clientWidth,
        table: tbl.scrollWidth - wrap.clientWidth,
      };
    });
    expect(overflow.page).toBeLessThanOrEqual(0);
    expect(overflow.table).toBeLessThanOrEqual(0);

    // The residual shows its band, with the inherent severity on hover.
    const residual = table.getByTestId("risks-row-residual").first();
    await expect(
      table.getByTestId("risks-row-residual-band").first(),
    ).toHaveText("Medium");
    await expect(residual.locator("xpath=..")).toHaveAttribute(
      "title",
      "Inherent: 20 (High)",
    );
  });

  test("the column chooser adds a column", async ({ authedPage: page }) => {
    await page.setViewportSize({ width: 1316, height: 900 });
    await mockRegister(page);
    await page.goto("/risks");
    const table = page.getByTestId("list-table-wrap");
    await expect(table.getByTestId("risks-row-title")).toHaveCount(3, {
      timeout: 30_000,
    });

    const chooser = page.getByTestId("risks-column-chooser");
    await chooser.locator("summary").click();
    // URL-driven checkbox: click and assert, as the roll-ups toggle does.
    const toggle = page.getByTestId("risks-column-toggle-methodology");
    await toggle.click();
    await expect(page).toHaveURL(/[?&]cols=methodology/);
    await expect(toggle).toBeChecked();
    await expect(table.getByTestId("risks-sort-methodology")).toBeVisible();
    await expect(table.getByTestId("risks-row-methodology").first()).toHaveText(
      "NIST SP 800-30",
    );
  });

  test("sorting is sent to the API, and the rows keep its order", async ({
    authedPage: page,
  }) => {
    const seen = await mockRegister(page);
    await page.goto("/risks");
    const table = page.getByTestId("list-table-wrap");
    await expect(table.getByTestId("risks-row-title")).toHaveCount(3, {
      timeout: 30_000,
    });
    const served = await titles(page);

    // A new column sorts descending first.
    const title = table.getByTestId("risks-sort-title");
    await title.click();
    await expect(title).toHaveAttribute("data-dir", "desc");
    await expect.poll(() => lastQuery(seen).get("sort")).toBe("-title");
    await expect(page).toHaveURL(/[?&]sort=-title/);
    // The mock reversed the rows; the page shows them as served.
    await expect.poll(() => titles(page)).toEqual([...served].reverse());

    await title.click();
    await expect(title).toHaveAttribute("data-dir", "asc");
    await expect.poll(() => lastQuery(seen).get("sort")).toBe("title");

    await table.getByTestId("risks-sort-review_due").click();
    await expect.poll(() => lastQuery(seen).get("sort")).toBe("-review_due");
    await expect(table.getByTestId("risks-sort-review_due")).toHaveAttribute(
      "aria-label",
      /sorted descending/,
    );
  });

  test("each filter is sent to the API", async ({ authedPage: page }) => {
    const seen = await mockRegister(page);
    await page.goto("/risks");
    await expect(
      page.getByTestId("list-table-wrap").getByTestId("risks-row-title"),
    ).toHaveCount(3, { timeout: 30_000 });

    const pill = (id: string) =>
      page.getByTestId(`list-filter-pill-${id}`).locator("select");

    await pill("owner").selectOption("none");
    await expect.poll(() => lastQuery(seen).get("owner")).toBe("none");

    await pill("treatment").selectOption("mitigate");
    await expect.poll(() => lastQuery(seen).get("treatment")).toBe("mitigate");

    await pill("category").selectOption("privacy");
    await expect.poll(() => lastQuery(seen).get("category")).toBe("privacy");

    await pill("review_due").selectOption("overdue");
    await expect.poll(() => lastQuery(seen).get("review_due")).toBe("overdue");

    await page.getByTestId("risks-review-due-from").fill("2026-11-01");
    await expect
      .poll(() => lastQuery(seen).get("review_due_from"))
      .toBe("2026-11-01");
    await page.getByTestId("risks-review-due-to").fill("2026-12-31");
    await expect
      .poll(() => lastQuery(seen).get("review_due_to"))
      .toBe("2026-12-31");

    // Every filter is in the one request, and a filter change goes back
    // to the first page.
    const q = lastQuery(seen);
    expect(q.get("owner")).toBe("none");
    expect(q.get("treatment")).toBe("mitigate");
    expect(q.get("category")).toBe("privacy");
    expect(q.get("review_due")).toBe("overdue");
    expect(q.get("offset")).toBe("0");
  });

  test("closed risks are hidden by default and shown with the status filter", async ({
    authedPage: page,
  }) => {
    const seen = await mockRegister(page);
    await page.goto("/risks");
    const table = page.getByTestId("list-table-wrap");
    await expect(table.getByTestId("risks-row-title")).toHaveCount(3, {
      timeout: 30_000,
    });
    await expect(table.getByTestId("risks-row-closed")).toHaveCount(0);
    await expect(
      table.getByTestId("risks-row-title").filter({ hasText: "closed" }),
    ).toHaveCount(0);

    const status = page
      .getByTestId("list-filter-pill-status")
      .locator("select");
    await expect(status).toHaveValue("open");
    await status.selectOption("closed");
    await expect.poll(() => lastQuery(seen).get("status")).toBe("closed");
    await expect(table.getByTestId("risks-row-title")).toHaveText([
      "Test risk closed (test-2132)",
    ]);
    await expect(table.getByTestId("risks-row-closed")).toHaveCount(1);

    await status.selectOption("all");
    await expect.poll(() => lastQuery(seen).get("status")).toBe("all");
    await expect(table.getByTestId("risks-row-title")).toHaveCount(4);
  });

  test("a row opens its risk", async ({ authedPage: page }) => {
    await mockRegister(page);
    await page.goto("/risks");
    const table = page.getByTestId("list-table-wrap");
    await expect(table.getByTestId("risks-row-title")).toHaveCount(3, {
      timeout: 30_000,
    });
    // Click a cell that is not a link: the row itself navigates.
    await table.getByTestId("risks-row-treatment").first().click();
    await expect(page).toHaveURL(
      /\/risks\/00000000-0000-4000-8000-00000002132/,
      {
        timeout: 30_000,
      },
    );
  });
});
