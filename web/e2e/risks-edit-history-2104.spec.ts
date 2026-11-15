// #2104 — edit a risk from its page and see the change in History.
//
// From the risk page, the shared header's Edit opens /risks/{id}/edit.
// Changing the title and the review date saves only those fields through
// PATCH /api/risks/{id}, returns to the risk page with a toast, and the
// History section lists the change with its before and after values.
//
// HERMETIC: route-mocks the risk BFF routes (slice 594 convention). The
// PATCH mock applies the body to its stored risk and appends the audit
// entry the platform writes, so the page reads back what it saved. The
// platform side (PATCH rules, audit rows, the read route) is covered by
// internal/api/risks/record_integration_test.go against real Postgres.

import type { Route } from "@playwright/test";

import { expect, test } from "./fixtures";

const RISK_ID = "00000000-0000-4000-8000-000000002104";

// Neutral test strings only (P0-A4 / GitGuardian).
function storedRisk() {
  return {
    id: RISK_ID,
    title: "Test risk before edit",
    description: "a neutral description",
    category: "operational",
    category_name: "Operational",
    methodology: "nist_800_30",
    inherent_score: { likelihood: 3, impact: 4 },
    treatment: "untreated",
    treatment_owner: "owner-a",
    residual_score: {},
    review_due_at: "2027-01-15T00:00:00Z",
    accepter: "",
    instrument_reference: "",
    linked_control_ids: [],
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    level: "team",
    themes: [],
    severity: 12,
  };
}

async function json(route: Route, status: number, body: unknown) {
  await route.fulfill({
    status,
    contentType: "application/json",
    body: JSON.stringify(body),
  });
}

test.describe("/risks/[id] edit + history (#2104)", () => {
  test("edits the title and review date; the toast shows; History lists the change", async ({
    authedPage: page,
  }) => {
    const risk: Record<string, unknown> = storedRisk();
    const entries: unknown[] = [];
    let patched: Record<string, unknown> | undefined;

    await page.route("**/api/risks/categories**", (route) =>
      json(route, 200, {
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
    );
    await page.route(`**/api/risks/${RISK_ID}/action-plans`, (route) =>
      json(route, 200, { action_plans: [] }),
    );
    await page.route(`**/api/risks/${RISK_ID}/parents`, (route) =>
      json(route, 200, { parents: [], count: 0 }),
    );
    await page.route(`**/api/risks/${RISK_ID}/children`, (route) =>
      json(route, 200, { children: [], count: 0 }),
    );
    await page.route(`**/api/risks/${RISK_ID}/audit-log`, (route) =>
      json(route, 200, { entries, count: entries.length }),
    );
    await page.route(/\/api\/risks(\?.*)?$/, (route) =>
      json(route, 200, { risks: [risk], count: 1 }),
    );
    await page.route(`**/api/risks/${RISK_ID}`, async (route, req) => {
      if (req.method() === "PATCH") {
        patched = JSON.parse(req.postData() ?? "{}");
        const before: Record<string, unknown> = {};
        for (const k of Object.keys(patched!)) before[k] = risk[k];
        Object.assign(risk, patched);
        entries.unshift({
          id: "e1",
          actor: "user-1",
          action: "fields_changed",
          before,
          after: patched,
          created_at: "2026-11-14T12:00:00Z",
        });
        await json(route, 200, { risk });
        return;
      }
      await json(route, 200, { risk });
    });

    await page.goto(`/risks/${RISK_ID}`);
    await expect(page.getByTestId("risk-detail-title")).toHaveText(
      "Test risk before edit",
      { timeout: 30_000 },
    );
    await expect(page.getByTestId("risk-history-empty")).toBeVisible();

    await page.getByTestId("risk-detail-edit").click();
    await expect(page).toHaveURL(new RegExp(`/risks/${RISK_ID}/edit$`), {
      timeout: 30_000,
    });
    await expect(page.getByTestId("risks-edit-form")).toBeVisible();
    // The form opens on the stored risk; treatment is not on it.
    await expect(page.getByTestId("risks-create-title")).toHaveValue(
      "Test risk before edit",
    );
    await expect(page.getByTestId("risks-create-review-due")).toHaveValue(
      "2027-01-15",
    );
    await expect(page.getByTestId("risks-create-treatment")).toHaveCount(0);

    await page.getByTestId("risks-create-title").fill("Test risk after edit");
    await page.getByTestId("risks-create-review-due").fill("2027-06-30");

    const saved = page.waitForResponse(
      (r) =>
        r.url().endsWith(`/api/risks/${RISK_ID}`) &&
        r.request().method() === "PATCH",
      { timeout: 30_000 },
    );
    await page.getByTestId("risks-edit-submit").click();
    await saved;

    // Only the two changed fields went on the wire.
    expect(patched).toEqual({
      title: "Test risk after edit",
      review_due_at: "2027-06-30T00:00:00Z",
    });

    await expect(page).toHaveURL(new RegExp(`/risks/${RISK_ID}$`), {
      timeout: 30_000,
    });
    await expect(page.getByTestId("toast")).toHaveText(/Risk updated/);
    await expect(page.getByTestId("risk-detail-title")).toHaveText(
      "Test risk after edit",
    );
    await expect(page.getByTestId("risk-detail-review-due")).toHaveText(
      "2027-06-30",
    );

    const entry = page.getByTestId("risk-history-entry");
    await expect(entry).toHaveCount(1);
    await expect(entry).toContainText("Edited");
    const changes = entry.getByTestId("risk-history-change");
    await expect(changes).toHaveText([
      "Title: Test risk before edit → Test risk after edit",
      "Review due: 2027-01-15 → 2027-06-30",
    ]);
  });
});
