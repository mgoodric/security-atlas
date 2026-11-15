// #2094 — Playwright E2E for the shared <Markdown> component on long-text
// fields.
//
// Coverage:
//   - A risk description with bold text and bullets renders formatted
//     (it rendered as plain text before).
//   - A policy body with a table and a block quote renders a real table
//     and blockquote (pipes and ">" were shown as text before).
//   - Raw HTML and a javascript: link in the same text stay inert.
//
// Mocked per the `route.fulfill` convention (policy-detail,
// risk-detail-hierarchy-links), so the assertions do not depend on seed
// data.

import type { Route } from "@playwright/test";

import { expect, test } from "./fixtures";

const RISK_ID = "00000000-0000-4000-8000-000000002094";
const POLICY_ID = "00000000-0000-0000-0000-000000002094";

async function json(route: Route, body: unknown) {
  await route.fulfill({
    status: 200,
    contentType: "application/json",
    body: JSON.stringify(body),
  });
}

const RISK = {
  id: RISK_ID,
  title: "Backup restore untested",
  description:
    "Restores are **not tested** on a schedule.\n\n- Nightly backups exist\n- No restore drill this year\n\n<script>window.__md_xss = 1</script> [x](javascript:alert(1))",
  category: "operational",
  methodology: "nist_800_30",
  inherent_score: { likelihood: 3, impact: 4 },
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
  severity: 12,
};

const POLICY = {
  id: POLICY_ID,
  title: "Access Control Policy",
  version: "v1.0",
  body_md:
    "# Roles\n\n| Role | Access |\n| --- | --- |\n| Admin | Full |\n| Reader | Read only |\n\n> Review this table **quarterly**.\n",
  owner_role: "security_lead",
  approver_role: "cto",
  linked_control_ids: [],
  acknowledgment_required_roles: ["all_staff"],
  status: "published",
  source_attribution: "in_house",
  created_by: "user-1",
  effective_date: "2026-01-15",
  published_at: "2026-01-15T00:00:00Z",
  created_at: "2026-01-01T00:00:00Z",
  updated_at: "2026-04-22T00:00:00Z",
};

test.describe("long text renders as safe markdown (#2094)", () => {
  test("a risk description with bold and bullets renders formatted", async ({
    authedPage: page,
  }) => {
    await page.route(/\/api\/risks(\?.*)?$/, (route) =>
      json(route, { risks: [RISK] }),
    );
    await page.route(`**/api/risks/${RISK_ID}`, (route) =>
      json(route, { risk: RISK }),
    );
    await page.route(`**/api/risks/${RISK_ID}/action-plans`, (route) =>
      json(route, { action_plans: [] }),
    );
    await page.route(`**/api/risks/${RISK_ID}/parents`, (route) =>
      json(route, { parents: [], count: 0 }),
    );
    await page.route(`**/api/risks/${RISK_ID}/children`, (route) =>
      json(route, { children: [], count: 0 }),
    );

    await page.goto(`/risks/${RISK_ID}`);
    const description = page.getByTestId("risk-detail-description");
    await expect(description).toBeVisible({ timeout: 30_000 });

    await expect(description.locator("strong")).toHaveText("not tested");
    await expect(description.locator("ul > li")).toHaveText([
      "Nightly backups exist",
      "No restore drill this year",
    ]);
    await expect(description).not.toContainText("**");

    // Raw HTML shows as text and never runs; the javascript: link is
    // not a link.
    await expect(description).toContainText("<script>");
    await expect(description.locator("script")).toHaveCount(0);
    expect(
      await page.evaluate(
        () => (window as unknown as { __md_xss?: number }).__md_xss,
      ),
    ).toBeUndefined();
    await expect(description.locator("a")).toHaveCount(0);
  });

  test("a policy body with a table renders it as a table", async ({
    authedPage: page,
  }) => {
    await page.route(`**/api/policies/${POLICY_ID}`, (route) =>
      json(route, {
        policy: POLICY,
        ack_rate: {
          numerator: 1,
          denominator: 2,
          percent: 50,
          window_seconds: 31536000,
        },
      }),
    );

    await page.goto(`/policies/${POLICY_ID}`);
    const body = page.getByTestId("policy-detail-body");
    await expect(body).toBeVisible({ timeout: 30_000 });

    const table = body.locator("table");
    await expect(table).toHaveCount(1);
    await expect(table.locator("thead th")).toHaveText(["Role", "Access"]);
    await expect(table.locator("tbody tr")).toHaveCount(2);
    await expect(table.locator("tbody tr").first().locator("td")).toHaveText([
      "Admin",
      "Full",
    ]);
    await expect(body).not.toContainText("| ---");

    const quote = body.locator("blockquote");
    await expect(quote).toContainText("Review this table quarterly.");
    await expect(quote.locator("strong")).toHaveText("quarterly");
  });
});
