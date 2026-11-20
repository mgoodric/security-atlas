// Slice 100 — Playwright E2E for the /risks list view.
//
// Seeded by fixtures/e2e/risks-list.sql: a tenant of the spec's own
// with 55 open risks (30 `mitigate`, 25 `untreated`, none `avoid`)
// across two categories, one of them owned by the tenant's user and in
// its one org unit. 55 rows give the default 50-row page a second page.
// The spec signs in to the tenant with an admin JWT minted by
// /v1/test/issue-jwt. #2755 restored the assertions slice 100 and its
// follow-ons left commented out behind the slice-082 quarantine,
// against what ships now; the tests whose feature was since removed are
// `test.fixme` with the reason beside them.
//
// Run locally:
//   cd web
//   npx playwright install chromium     # once per machine
//   npx playwright test e2e/risks-list.spec.ts
//
// AC-10 coverage targets: list renders, filter narrows results, the
// hierarchy page-header link navigates, and the reciprocal `List view`
// link on /risks/hierarchy navigates back.

import type { Page } from "@playwright/test";

import { expect, test } from "./fixtures";
import { seedFromFixture } from "./seed";
import { signInToTenant } from "./tenant-session";

// Mirror the rows in fixtures/e2e/risks-list.sql.
const TENANT = "00000000-0000-0000-0000-2755d0000004";
const OWNER_USER = "44444444-4444-4444-4444-2755d0000041";
const ORG_UNIT = "cccccccc-cccc-cccc-cccc-2755d0000001";
const TOTAL = 55;
const MITIGATED = 30;

/** Opens /risks (plus `query`) and waits for the register to load. */
async function openRisks(page: Page, query = ""): Promise<void> {
  await page.goto(`/risks${query}`);
  await expect(page.getByTestId("list-page")).toBeVisible({ timeout: 30_000 });
  await expect(page.getByTestId("list-loading-skeleton")).toHaveCount(0, {
    timeout: 30_000,
  });
}

function rows(page: Page) {
  return page.getByTestId("list-table-wrap").getByTestId("list-table-row");
}

function pill(page: Page, id: string) {
  return page.getByTestId(`list-filter-pill-${id}`);
}

test.describe("/risks list view", () => {
  test.beforeAll(() => {
    seedFromFixture("risks-list");
  });

  test.beforeEach(async ({ page }) => {
    await signInToTenant(page, {
      tenantId: TENANT,
      roles: ["admin"],
      superAdmin: true,
    });
  });

  test("AC-1: /risks renders the risk table for any signed-in user", async ({
    page,
  }) => {
    await openRisks(page);
    await expect(
      page.getByRole("heading", { name: "Risk register", exact: true }),
    ).toBeVisible();
    await expect(page.getByTestId("list-table-wrap")).toBeVisible();
    await expect(rows(page)).toHaveCount(50);
    await expect(page.getByTestId("risks-count-label")).toContainText(
      `${TOTAL} risks`,
    );
  });

  test("AC-3: horizontal pill filter row narrows the result set", async ({
    page,
  }) => {
    await openRisks(page);
    // The filter row is horizontal (P0-A2) — no left sidebar.
    await expect(page.getByTestId("list-filter-pills")).toBeVisible();
    await expect(rows(page)).toHaveCount(50);
    await pill(page, "treatment")
      .getByRole("combobox")
      .selectOption({ value: "mitigate" });
    await expect(page).toHaveURL(/[?&]treatment=mitigate/);
    await expect(rows(page)).toHaveCount(MITIGATED);
    await expect(page.getByTestId("risks-count-label")).toContainText(
      `${MITIGATED} of ${TOTAL} risks`,
    );
    for (const t of await page.getByTestId("risks-row-treatment").all()) {
      await expect(t).toContainText(/mitigate/i);
    }
  });

  test("AC-4: empty state surfaces when filters return zero rows", async ({
    page,
  }) => {
    // No seeded risk is `avoid`. The owner pill takes a user id since
    // #2129; this one owns nothing.
    await openRisks(
      page,
      "?treatment=avoid&owner=44444444-4444-4444-4444-2755d00000ff",
    );
    await expect(page.getByTestId("list-empty-state")).toContainText(
      "No risks match these filters",
    );
    // The CTA reads "Clear filters" on a filter-induced empty state.
    const cta = page.getByTestId("list-empty-state-cta");
    await expect(cta).toHaveText("Clear filters");
    await cta.click();
    await expect(page).not.toHaveURL(/treatment=|owner=/);
    await expect(rows(page)).toHaveCount(50);
  });

  test("AC-6: page-header `Hierarchy view ->` link navigates to /risks/hierarchy", async ({
    page,
  }) => {
    await openRisks(page);
    await page.getByTestId("risks-hierarchy-link").click();
    await expect(page).toHaveURL(/\/risks\/hierarchy/);
    // Reciprocal AC-8 wiring: the hierarchy page exposes a `List view ->`
    // link back to /risks.
    const back = page.getByTestId("risk-hierarchy-list-view-link");
    await expect(back).toBeVisible({ timeout: 30_000 });
    await back.click();
    await expect(page).toHaveURL(/\/risks(\?.*)?$/);
    await expect(page.getByTestId("list-page")).toBeVisible();
  });

  // The sidebar lists /risks/hierarchy again: lib/nav.ts NAV_GROUPS puts
  // it in the "Risks" group beside /risks, reversing slice 100's audit
  // F-3 removal. Not a product bug on its face; the intent needs a call: #2774.
  test.fixme(
    "AC-8: sidebar no longer exposes /risks/hierarchy as a top-level entry",
    async ({ page }) => {
      await page.goto("/dashboard");
      await expect(page.getByRole("link", { name: "Risks" })).toBeVisible();
      await expect(
        page.getByRole("link", { name: "Risk hierarchy" }),
      ).toHaveCount(0);
    },
  );

  // Slice 185 (AC-4) made rows non-clickable, with a per-row "View in
  // hierarchy" link and a future-slice banner, because no per-risk detail
  // page existed. /risks/[id] has since shipped (#2132): a row click opens
  // it, and the link and banner are gone. #2755 replaced slice 185's three
  // tests, which can never pass again, with this one for the shipped shape.
  test("#2132 (supersedes AC-185-1..3): a row click opens /risks/[id]; no hierarchy-link or future-slice banner", async ({
    page,
  }) => {
    await openRisks(page);
    await expect(page.getByTestId("risks-row-hierarchy-link")).toHaveCount(0);
    await expect(
      page.getByTestId("risks-detail-future-slice-banner"),
    ).toHaveCount(0);
    await rows(page).first().getByTestId("list-cell-title").click();
    await expect(page).toHaveURL(/\/risks\/[0-9a-f-]{36}$/);
  });

  // Slice 247 — the header "New risk" is a `<Link>` styled as a button,
  // replacing the disabled `<Button>`.
  test("AC-247-1: header `New risk` link is enabled and points at /risks/new", async ({
    page,
  }) => {
    await openRisks(page);
    const newLink = page.getByTestId("risks-new-link");
    await expect(newLink).toBeVisible();
    await expect(newLink).toHaveText("New risk");
    await expect(newLink).toHaveAttribute("href", "/risks/new");
    // The link is the role exposed to assistive tech, not a disabled
    // button.
    await expect(newLink).toHaveRole("link");
    await expect(newLink).not.toHaveAttribute("disabled");
    await expect(
      page.locator("button[disabled]", { hasText: /^New risk$/ }),
    ).toHaveCount(0);
  });

  test("AC-247-2: clicking header `New risk` navigates to /risks/new", async ({
    page,
  }) => {
    await openRisks(page);
    await page.getByTestId("risks-new-link").click();
    await expect(page).toHaveURL(/\/risks\/new$/);
  });

  // Slice 244 — extended filter pill row (Category + Methodology + Org
  // unit beside Treatment + Owner).
  test("AC-244-1: Category pill is visible with the default `All categories` label", async ({
    page,
  }) => {
    await openRisks(page);
    const p = pill(page, "category");
    await expect(p).toBeVisible();
    await expect(p).toContainText("Category");
    await expect(p.getByRole("combobox")).toHaveValue("all");
    await expect(
      p.getByRole("option", { name: "All categories" }),
    ).toBeAttached();
    // The tenant's own categories are offered by name.
    await expect(p.getByRole("option", { name: "Operational" })).toBeAttached();
  });

  test("AC-244-2: Methodology pill is visible with the default `All methodologies` label", async ({
    page,
  }) => {
    await openRisks(page);
    const p = pill(page, "methodology");
    await expect(p).toBeVisible();
    await expect(p).toContainText("Methodology");
    await expect(p.getByRole("combobox")).toHaveValue("all");
    await expect(
      p.getByRole("option", { name: "All methodologies" }),
    ).toBeAttached();
  });

  test("AC-244-3: Org unit pill is visible with the default `All units` label", async ({
    page,
  }) => {
    await openRisks(page);
    const p = pill(page, "org_unit");
    await expect(p).toBeVisible();
    await expect(p).toContainText("Org unit");
    await expect(p.getByRole("combobox")).toHaveValue("all");
    await expect(p.getByRole("option", { name: "All units" })).toBeAttached();
    await expect(p.getByRole("option", { name: "Platform" })).toBeAttached();
  });

  // Slice 246 — /risks pagination footer (AC-7). The fixture's 55 risks
  // make the default 50-row page a 2-page result.
  test("AC-246-1: pagination footer renders with truth-telling summary", async ({
    page,
  }) => {
    await openRisks(page);
    await expect(page.getByTestId("risks-pagination")).toBeVisible();
    await expect(page.getByTestId("risks-pagination-summary")).toHaveText(
      `Showing 1–50 of ${TOTAL}`,
    );
  });

  test("AC-246-2: Previous is disabled on page 1, Next is enabled", async ({
    page,
  }) => {
    await openRisks(page);
    await expect(page.getByTestId("risks-pagination-prev")).toBeDisabled();
    await expect(page.getByTestId("risks-pagination-next")).toBeEnabled();
  });

  test("AC-246-3: clicking Next advances to ?page=2 and updates the summary", async ({
    page,
  }) => {
    await openRisks(page);
    await page.getByTestId("risks-pagination-next").click();
    await expect(page).toHaveURL(/\/risks\?(.*&)?page=2/);
    await expect(page.getByTestId("risks-pagination-summary")).toHaveText(
      `Showing 51–${TOTAL} of ${TOTAL}`,
    );
    await expect(rows(page)).toHaveCount(TOTAL - 50);
    // Previous is now enabled; Next is disabled (only 2 pages).
    await expect(page.getByTestId("risks-pagination-prev")).toBeEnabled();
    await expect(page.getByTestId("risks-pagination-next")).toBeDisabled();
  });

  test("AC-246-4: Previous from page 2 returns to page 1 with the page param dropped", async ({
    page,
  }) => {
    await openRisks(page, "?page=2");
    await expect(page.getByTestId("risks-pagination-summary")).toHaveText(
      `Showing 51–${TOTAL} of ${TOTAL}`,
    );
    await page.getByTestId("risks-pagination-prev").click();
    // Canonical page-1 URL drops the `page` param.
    await expect(page).not.toHaveURL(/[?&]page=/);
    await expect(page).toHaveURL(/\/risks(\?.*)?$/);
    await expect(page.getByTestId("risks-pagination-prev")).toBeDisabled();
    await expect(page.getByTestId("risks-pagination-summary")).toHaveText(
      `Showing 1–50 of ${TOTAL}`,
    );
  });

  test("AC-246-5: filter mutation while on page 2 resets to page 1", async ({
    page,
  }) => {
    await openRisks(page, "?page=2");
    await expect(page.getByTestId("risks-pagination-summary")).toHaveText(
      `Showing 51–${TOTAL} of ${TOTAL}`,
    );
    await pill(page, "treatment")
      .getByRole("combobox")
      .selectOption({ value: "mitigate" });
    // The page param is dropped on the URL replace.
    await expect(page).toHaveURL(/[?&]treatment=mitigate/);
    await expect(page).not.toHaveURL(/[?&]page=/);
    await expect(page.getByTestId("risks-pagination-prev")).toBeDisabled();
    await expect(page.getByTestId("risks-pagination-summary")).toHaveText(
      `Showing 1–${MITIGATED} of ${MITIGATED}`,
    );
  });

  test("AC-246-6: refresh on ?page=2 preserves the page state", async ({
    page,
  }) => {
    await openRisks(page, "?page=2");
    await page.reload();
    await expect(page).toHaveURL(/[?&]page=2/);
    await expect(page.getByTestId("risks-pagination-summary")).toHaveText(
      `Showing 51–${TOTAL} of ${TOTAL}`,
      { timeout: 30_000 },
    );
  });

  test("AC-244-4: URL query round-trips the filter keys", async ({ page }) => {
    // The slice 244 set was six keys; the severity pill has since left
    // the page and Status + Review due joined it. Every key the page
    // ships reads back into its pill, and the combination lands on the
    // fixture's one owned, org-unit risk.
    await openRisks(
      page,
      `?status=open&category=operational&methodology=nist_800_30&org_unit=${ORG_UNIT}&treatment=mitigate&owner=${OWNER_USER}`,
    );
    const expected: Record<string, string> = {
      status: "open",
      category: "operational",
      methodology: "nist_800_30",
      org_unit: ORG_UNIT,
      treatment: "mitigate",
      owner: OWNER_USER,
    };
    for (const [id, value] of Object.entries(expected)) {
      await expect(pill(page, id).getByRole("combobox")).toHaveValue(value);
    }
    // The id-valued pills show the person and the unit by name.
    await expect(pill(page, "owner").locator("option:checked")).toHaveText(
      "Risks List Owner",
    );
    await expect(pill(page, "org_unit").locator("option:checked")).toHaveText(
      "Platform",
    );
    await expect(rows(page)).toHaveCount(1);
    await expect(rows(page).first()).toContainText("Register risk 01");
  });
});
