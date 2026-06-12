// Slice 681 / ATLAS-039 + ATLAS-036 — Playwright E2E pinning the three
// risk-surface UX fixes:
//
//   AC-1 — clicking the INHERENT SEVERITY / RESIDUAL / REVIEW-DUE headers
//          re-orders the rows (sortable columns).
//   AC-2 — a risk TITLE links to the read-only `/risks/{id}` detail.
//   AC-3 — the sidebar Risks badge reads as HIGH-SEVERITY (not a total).
//
// HERMETIC (slice 594 shared-DB → hermetic-mock convention): every spec
// route-mocks the `/api/risks` list BFF + the `/api/risks/{id}` detail
// BFF + the org-units fetch, so nothing reaches the docker-compose seed.
// Neutral test strings only (P0-A4 / GitGuardian).

import { expect, test } from "./fixtures";

// Three rows with distinct severities + residuals + review-due dates so
// every sort direction produces an unambiguous, assertable order. Shape
// mirrors `web/lib/api/risks.ts` `Risk`.
function mockRisks() {
  return [
    {
      // Mid severity, mid residual, mid review-due.
      id: "00000000-0000-4000-8000-0000000000bb",
      title: "Risk Bravo",
      description: "Neutral risk bravo",
      category: "operational",
      methodology: "nist_800_30",
      inherent_score: { likelihood: 3, impact: 4 },
      treatment: "mitigate",
      treatment_owner: "owner-b",
      residual_score: { likelihood: 3, impact: 3 }, // 9/25 = 0.36
      review_due_at: "2026-09-01T00:00:00Z",
      accepter: "",
      instrument_reference: "",
      linked_control_ids: [],
      created_at: "2026-01-01T00:00:00Z",
      updated_at: "2026-01-01T00:00:00Z",
      themes: [],
      severity: 12,
    },
    {
      // Highest severity, highest residual, latest review-due.
      id: "00000000-0000-4000-8000-0000000000aa",
      title: "Risk Alpha",
      description: "Neutral risk alpha",
      category: "operational",
      methodology: "nist_800_30",
      inherent_score: { likelihood: 5, impact: 5 },
      treatment: "mitigate",
      treatment_owner: "owner-a",
      residual_score: { likelihood: 5, impact: 5 }, // 25/25 = 1.00
      review_due_at: "2026-12-01T00:00:00Z",
      accepter: "",
      instrument_reference: "",
      linked_control_ids: [],
      created_at: "2026-01-01T00:00:00Z",
      updated_at: "2026-01-01T00:00:00Z",
      themes: [],
      severity: 25, // high — also drives the sidebar badge
    },
    {
      // Lowest severity, lowest residual, soonest review-due.
      id: "00000000-0000-4000-8000-0000000000cc",
      title: "Risk Charlie",
      description: "Neutral risk charlie",
      category: "operational",
      methodology: "nist_800_30",
      inherent_score: { likelihood: 1, impact: 2 },
      treatment: "mitigate",
      treatment_owner: "owner-c",
      residual_score: { likelihood: 1, impact: 1 }, // 1/25 = 0.04
      review_due_at: "2026-06-01T00:00:00Z",
      accepter: "",
      instrument_reference: "",
      linked_control_ids: [],
      created_at: "2026-01-01T00:00:00Z",
      updated_at: "2026-01-01T00:00:00Z",
      themes: [],
      severity: 4,
    },
  ];
}

async function mockListRoutes(page: import("@playwright/test").Page) {
  // Org-units fetch (Org unit pill) — registered FIRST so the broader
  // /api/risks pattern below does not swallow it.
  await page.route("**/api/risks-hierarchy/org-units**", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ org_units: [] }),
    });
  });
  // The list BFF. The `/api/risks/{id}` detail route is a longer path, so
  // anchor this pattern to the list shape (`/api/risks` optionally with a
  // query string, but NOT a trailing `/id`).
  await page.route(/\/api\/risks(\?.*)?$/, async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ risks: mockRisks() }),
    });
  });
}

// Read the visible title-cell text, in DOM order, from the desktop table.
async function titleOrder(
  page: import("@playwright/test").Page,
): Promise<string[]> {
  const desktop = page.locator('[data-testid="list-table-wrap"]');
  return desktop.getByTestId("risks-row-title").allInnerTexts();
}

test.describe("/risks sortable columns (slice 681 AC-1)", () => {
  test("default order is inherent-severity descending (worst first)", async ({
    authedPage,
  }) => {
    await mockListRoutes(authedPage);
    await authedPage.goto("/risks");
    // Default sort = severity desc: Alpha (25), Bravo (12), Charlie (4).
    await expect
      .poll(() => titleOrder(authedPage))
      .toEqual(["Risk Alpha", "Risk Bravo", "Risk Charlie"]);
  });

  test("clicking the inherent-severity header toggles to ascending order", async ({
    authedPage,
  }) => {
    await mockListRoutes(authedPage);
    await authedPage.goto("/risks");

    const desktop = authedPage.locator('[data-testid="list-table-wrap"]');
    // The active default column is severity; one click flips desc -> asc.
    await desktop.getByTestId("risks-sort-severity").click();

    await expect
      .poll(() => titleOrder(authedPage))
      .toEqual(["Risk Charlie", "Risk Bravo", "Risk Alpha"]);
    // The URL carries the non-default sort so it is bookmarkable.
    await expect(authedPage).toHaveURL(/sort=severity%3Aasc/);
  });

  test("clicking the review-due header orders by date ascending (soonest first)", async ({
    authedPage,
  }) => {
    await mockListRoutes(authedPage);
    await authedPage.goto("/risks");

    const desktop = authedPage.locator('[data-testid="list-table-wrap"]');
    // A new column click sorts descending first (latest first).
    await desktop.getByTestId("risks-sort-review_due").click();
    await expect
      .poll(() => titleOrder(authedPage))
      .toEqual(["Risk Alpha", "Risk Bravo", "Risk Charlie"]); // Dec, Sep, Jun

    // Second click flips to ascending (soonest first).
    await desktop.getByTestId("risks-sort-review_due").click();
    await expect
      .poll(() => titleOrder(authedPage))
      .toEqual(["Risk Charlie", "Risk Bravo", "Risk Alpha"]); // Jun, Sep, Dec
  });

  test("clicking the residual header orders by magnitude descending", async ({
    authedPage,
  }) => {
    await mockListRoutes(authedPage);
    await authedPage.goto("/risks");

    const desktop = authedPage.locator('[data-testid="list-table-wrap"]');
    await desktop.getByTestId("risks-sort-residual").click();
    // Residual desc: Alpha (1.00), Bravo (0.36), Charlie (0.04).
    await expect
      .poll(() => titleOrder(authedPage))
      .toEqual(["Risk Alpha", "Risk Bravo", "Risk Charlie"]);
  });
});

test.describe("/risks per-risk detail (slice 681 AC-2)", () => {
  test("a risk title links to its read-only detail page", async ({
    authedPage,
  }) => {
    await mockListRoutes(authedPage);
    // Mock the detail BFF for Risk Alpha.
    const alpha = mockRisks().find((r) => r.title === "Risk Alpha")!;
    await authedPage.route(/\/api\/risks\/[0-9a-fA-F-]+$/, async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ risk: alpha }),
      });
    });

    await authedPage.goto("/risks");

    const desktop = authedPage.locator('[data-testid="list-table-wrap"]');
    // The title is a link (not plain text) and navigates to /risks/{id}.
    const alphaTitle = desktop
      .getByTestId("risks-row-title")
      .filter({ hasText: "Risk Alpha" });
    await expect(alphaTitle).toHaveAttribute("href", `/risks/${alpha.id}`);
    await alphaTitle.click();

    await expect(authedPage).toHaveURL(new RegExp(`/risks/${alpha.id}$`));
    await expect(authedPage.getByTestId("risk-detail")).toBeVisible();
    await expect(authedPage.getByTestId("risk-detail-title")).toContainText(
      "Risk Alpha",
    );
    // Read-only: the detail shows the scoring axes and a hierarchy link,
    // and a back-link to the register.
    await expect(authedPage.getByTestId("risk-detail-severity")).toContainText(
      "25",
    );
    await expect(authedPage.getByTestId("risk-detail-back")).toBeVisible();
  });

  test("the misleading 'future slice' banner is gone from the list", async ({
    authedPage,
  }) => {
    await mockListRoutes(authedPage);
    await authedPage.goto("/risks");
    await expect(
      authedPage.getByTestId("risks-detail-future-slice-banner"),
    ).toHaveCount(0);
  });
});

test.describe("sidebar Risks badge high-severity clarity (slice 681 AC-3)", () => {
  test("the badge label reads as high-severity, not a total", async ({
    authedPage,
  }) => {
    await mockListRoutes(authedPage);
    await authedPage.goto("/risks");

    // The badge only renders when at least one high-severity (>=15) risk
    // exists — Risk Alpha (severity 25) guarantees it.
    const badge = authedPage.getByTestId("sidebar-risks-count");
    await expect(badge).toBeVisible();
    // Disambiguation: the accessible label + hover title both say
    // "high-severity" (count = 1 here: only Alpha crosses the threshold).
    await expect(badge).toHaveAttribute("aria-label", "1 high-severity risk");
    await expect(badge).toHaveAttribute("title", "1 high-severity risk");
  });
});
