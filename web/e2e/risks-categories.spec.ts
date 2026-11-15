// #1817 — Playwright E2E for organisation risk categories in the web UI.
//
// Coverage:
//   - The /risks/new category picker lists the organisation's active
//     categories by name (an added one included, an archived one not) and
//     posts the slug. A category the platform refuses comes back as a 400
//     and renders inline in the form's error Alert.
//   - The register's Category pill filters by category; "Show archived
//     categories" adds archived ones to the pill, alongside the roll-ups
//     toggle.
//   - A risk on an archived category shows that category, marked
//     archived, in the register and on the risk detail.
//
// Mocked per the `route.fulfill` convention: `/api/risks/categories`
// answers like upstream, archived categories only with
// `include_archived=true`. No sleeps: every assertion auto-waits.
//
// Hard rule (P0-A9): neutral test strings only.

import type { Page, Route } from "@playwright/test";

import { expect, test } from "./fixtures";

const ACTIVE_ID = "00000000-0000-4000-8000-000000001817";
const ARCHIVED_ID = "00000000-0000-4000-8000-000000001818";

function category(
  slug: string,
  name: string,
  opts: { archived?: boolean; isDefault?: boolean } = {},
) {
  return {
    id: `00000000-0000-4000-8000-${slug.padEnd(12, "0").slice(0, 12)}`,
    slug,
    name,
    description: "",
    archived: opts.archived ?? false,
    is_default: opts.isDefault ?? false,
    risk_count: 0,
    rule_count: 0,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
  };
}

const CATEGORIES = [
  category("operational", "Operational", { isDefault: true }),
  category("financial", "Financial"),
  // Added through the management API (#1810).
  category("cloud_posture", "Cloud posture"),
  category("legacy_systems", "Legacy systems", { archived: true }),
];

function riskRow(id: string, title: string, slug: string, name: string) {
  return {
    id,
    title,
    description: "",
    category: slug,
    category_name: name,
    methodology: "nist_800_30",
    inherent_score: { likelihood: 3, impact: 3 },
    treatment: "untreated",
    treatment_owner: "",
    residual_score: {},
    accepter: "",
    instrument_reference: "",
    linked_control_ids: [],
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    themes: [],
    level: "team",
    severity: 9,
  };
}

const RISKS = [
  riskRow(ACTIVE_ID, "Ledger reconciliation gap", "financial", "Financial"),
  riskRow(
    ARCHIVED_ID,
    "Mainframe patch backlog",
    "legacy_systems",
    "Legacy systems",
  ),
];

async function json(route: Route, status: number, body: unknown) {
  await route.fulfill({
    status,
    contentType: "application/json",
    body: JSON.stringify(body),
  });
}

async function mockCategories(page: Page) {
  await page.route(/\/api\/risks\/categories(\?.*)?$/, (route, req) => {
    const withArchived =
      new URL(req.url()).searchParams.get("include_archived") === "true";
    const categories = CATEGORIES.filter((c) => withArchived || !c.archived);
    return json(route, 200, { categories, count: categories.length });
  });
}

test.describe("/risks organisation categories (#1817)", () => {
  test("the picker lists active categories by name and posts the slug", async ({
    authedPage: page,
  }) => {
    await mockCategories(page);
    let posted: { category?: string } | undefined;
    await page.route(/\/api\/risks(\?.*)?$/, async (route, req) => {
      if (req.method() !== "POST") return json(route, 200, { risks: [] });
      posted = req.postDataJSON() as { category?: string };
      // Custom categories are refused until #1745's contract step.
      return json(route, 400, {
        error:
          'category "cloud_posture" cannot hold risks until the risk_category enum is retired (#1745)',
      });
    });

    await page.goto("/risks/new");
    const picker = page.getByTestId("risks-create-category");
    await expect(picker).toBeEnabled({ timeout: 30_000 });

    const options = picker.locator("option");
    await expect(options).toHaveText([
      "Operational",
      "Financial",
      "Cloud posture",
    ]);
    await expect(picker.locator("option", { hasText: "Legacy" })).toHaveCount(
      0,
    );
    // Opens on the organisation default.
    await expect(picker).toHaveValue("operational");

    await page.getByTestId("risks-create-title").fill("Neutral test risk");
    await page.getByTestId("risks-create-treatment-owner").fill("e2e-owner");
    await picker.selectOption({ label: "Cloud posture" });
    await page.getByTestId("risks-create-submit").click();

    await expect(page.getByTestId("risks-create-error")).toContainText(
      "cannot hold risks",
    );
    expect(posted?.category).toBe("cloud_posture");
    // The form keeps the user's input after the refusal.
    await expect(picker).toHaveValue("cloud_posture");
  });

  test("filters by category; show archived reveals archived categories", async ({
    authedPage: page,
  }) => {
    await mockCategories(page);
    await page.route(/\/api\/risks(\?.*)?$/, (route) =>
      json(route, 200, { risks: RISKS }),
    );

    await page.goto("/risks");
    const pill = page
      .getByTestId("list-filter-pill-category")
      .locator("select");
    await expect(pill.locator("option")).toHaveText(
      ["All categories", "Cloud posture", "Financial", "Operational"],
      { timeout: 30_000 },
    );

    // The register renders each row twice (desktop table + phone cards);
    // read the desktop table.
    const table = page.getByTestId("list-table-wrap");
    await pill.selectOption({ label: "Financial" });
    await expect(page).toHaveURL(/category=financial/);
    await expect(table.getByTestId("risks-row-title")).toHaveText([
      "Ledger reconciliation gap",
    ]);

    await page.getByTestId("risks-show-archived").check();
    await expect(page).toHaveURL(/archived=1/);
    await expect(
      pill.locator("option", { hasText: "Legacy systems (archived)" }),
    ).toHaveCount(1);
    await pill.selectOption({ label: "Legacy systems (archived)" });
    await expect(table.getByTestId("risks-row-title")).toHaveText([
      "Mainframe patch backlog",
    ]);

    // Additive with the roll-ups toggle: turning it on keeps the archived
    // filter.
    await page.getByTestId("risks-show-rollups").check();
    await expect(page).toHaveURL(/rollups=1/);
    await expect(page).toHaveURL(/archived=1/);
    await expect(page.getByTestId("risks-show-archived")).toBeChecked();
  });

  test("a risk on an archived category shows it, marked archived", async ({
    authedPage: page,
  }) => {
    await mockCategories(page);
    await page.route(/\/api\/risks(\?.*)?$/, (route) =>
      json(route, 200, { risks: RISKS }),
    );
    await page.route(`**/api/risks/${ARCHIVED_ID}`, (route) =>
      json(route, 200, { risk: RISKS[1] }),
    );
    await page.route(`**/api/risks/${ARCHIVED_ID}/action-plans`, (route) =>
      json(route, 200, { action_plans: [] }),
    );
    await page.route(`**/api/risks/${ARCHIVED_ID}/parents`, (route) =>
      json(route, 200, { parents: [], count: 0 }),
    );
    await page.route(`**/api/risks/${ARCHIVED_ID}/children`, (route) =>
      json(route, 200, { children: [], count: 0 }),
    );

    await page.goto("/risks");
    // Desktop table only: the phone cards repeat each row.
    const rows = page
      .getByTestId("list-table-wrap")
      .getByTestId("risks-row-category");
    await expect(rows.filter({ hasText: "Legacy systems" })).toContainText(
      "archived",
      { timeout: 30_000 },
    );
    await expect(rows.filter({ hasText: "Financial" })).not.toContainText(
      "archived",
    );

    await page.goto(`/risks/${ARCHIVED_ID}`);
    await expect(page.getByTestId("risk-detail-category")).toHaveText(
      "Legacy systems",
      { timeout: 30_000 },
    );
    await expect(
      page.getByTestId("risk-detail-category-archived"),
    ).toBeVisible();
  });
});
