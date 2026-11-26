// #3053 (N9 from the #1742 review) — Playwright for the "Amended by import"
// notice. A frozen period that a historical import amended shows, on the
// audit-period page, the badge, the freeze revision and the admin's reason;
// a sample drawn in that period's control workspace shows the same
// revision and reason in the sample panel.
//
// MOCK STRATEGY: the BFF wire surface is mocked on the browser CONTEXT, as
// in audit-period-detail.spec.ts. Amending a period needs a frozen period
// and an admin import, which would change the shared seeded data for every
// other spec; the amendment itself is covered by the Go tier
// (internal/evidence/historicalimport).
//
// Hard rule (P0-A9 / GitGuardian): every id and hash is a neutral test
// string.

import type { BrowserContext } from "@playwright/test";

import { expect, test } from "./fixtures";

const PERIOD_ID = "00000000-0000-0000-0000-000000003053";
const CONTROL_ID = "00000000-0000-0000-0000-0000000030a1";
const POPULATION_ID = "00000000-0000-0000-0000-0000000030b1";
const SAMPLE_ID = "00000000-0000-0000-0000-0000000030c1";
const RECORD_ID = "00000000-0000-0000-0000-0000000030e1";
const FROZEN_AT = "2026-04-01T00:00:00Z";
const FROZEN_HASH =
  "3053305330533053305330533053305330533053305330533053305330533053";
const REVISION_HASH =
  "30533053305330533053305330533053305330533053305330533053305330ab";
const REASON = "late FY26 Q1 results (test-3053)";

const period = {
  id: PERIOD_ID,
  name: "SOC 2 2026 Q1 (test-3053)",
  framework_version_id: "00000000-0000-0000-0000-0000000000ff",
  period_start: "2026-01-01T00:00:00Z",
  period_end: "2026-03-31T00:00:00Z",
  status: "frozen",
  frozen_at: FROZEN_AT,
  frozen_by: "test-operator",
  frozen_hash: FROZEN_HASH,
  freeze_revision: 3,
  amended_by_import: true,
  amend_reason: REASON,
  revision_hash: REVISION_HASH,
  created_by: "test-operator",
  created_at: "2026-01-01T00:00:00Z",
  updated_at: FROZEN_AT,
};

function json(body: unknown, status = 200) {
  return {
    status,
    contentType: "application/json",
    body: JSON.stringify(body),
  };
}

async function mockAmendedPeriod(context: BrowserContext) {
  await context.route(
    new RegExp(`/api/audits/${PERIOD_ID}$`),
    async (route, req) => {
      if (req.method() !== "GET") return route.fallback();
      await route.fulfill(json({ audit_period: period }));
    },
  );
  await context.route(/\/api\/controls-list$/, async (route) => {
    await route.fulfill(
      json({
        controls: [
          {
            id: CONTROL_ID,
            title: "Access reviews (test-3053)",
            control_family: "IAC",
            scf_id: "IAC-17",
            lifecycle_state: "active",
            bundle_id: "test-bundle",
          },
        ],
        count: 1,
      }),
    );
  });
  await context.route(
    new RegExp(`/api/audits/${PERIOD_ID}/control-state\\?`),
    async (route) => {
      await route.fulfill(
        json({
          audit_period_id: PERIOD_ID,
          control_id: CONTROL_ID,
          observations: [
            {
              evidence_record_id: RECORD_ID,
              observed_at: "2026-02-01T00:00:00Z",
              result: "pass",
              hash: "aa",
            },
          ],
          count: 1,
        }),
      );
    },
  );
  await context.route(/\/api\/audit\/populations$/, async (route, req) => {
    if (req.method() !== "POST") return route.fallback();
    await route.fulfill(
      json({
        population: {
          id: POPULATION_ID,
          control_id: CONTROL_ID,
          scope_predicate: null,
          time_window_start: "2026-01-01T00:00:00Z",
          time_window_end: "2026-03-31T00:00:00Z",
          frozen_at: FROZEN_AT,
          row_count: 1,
          created_by: "test-operator",
          created_at: FROZEN_AT,
        },
      }),
    );
  });
  await context.route(/\/api\/audit\/samples$/, async (route, req) => {
    if (req.method() !== "POST") return route.fallback();
    await route.fulfill(
      json({
        sample: {
          id: SAMPLE_ID,
          population_id: POPULATION_ID,
          n: 1,
          seed: "test-3053",
          created_by: "test-operator",
          created_at: FROZEN_AT,
          evidence_record_ids: [RECORD_ID],
          freeze_revision: 3,
          amended_by_import: true,
          amend_reason: REASON,
        },
      }),
    );
  });
  await context.route(
    new RegExp(`/api/audit/samples/${SAMPLE_ID}/annotations$`),
    async (route, req) => {
      if (req.method() !== "GET") return route.fallback();
      await route.fulfill(json({ annotations: [] }));
    },
  );
}

test.describe("amended-by-import notice (#3053)", () => {
  test("the period page and the sample panel show the freeze revision and the reason", async ({
    authedPage: page,
  }) => {
    await mockAmendedPeriod(page.context());
    await page.goto(`/audits/${PERIOD_ID}`);

    // Audit-period page: the badge, the revision and the reason; the
    // original freeze hash is still the one shown as the freeze hash.
    await expect(page.getByTestId("audit-period-amended-by-import")).toHaveText(
      "Amended by import",
      { timeout: 30_000 },
    );
    await expect(
      page.getByTestId("audit-period-freeze-revision"),
    ).toContainText("3: a historical import added evidence");
    await expect(page.getByTestId("audit-period-amend-reason")).toHaveText(
      REASON,
    );
    await expect(page.getByTestId("audit-period-revision-hash")).toHaveText(
      REVISION_HASH,
    );
    await expect(page.getByTestId("audit-period-frozen-hash")).toHaveText(
      FROZEN_HASH,
    );

    // Sample panel: open the control, build a population, draw a sample.
    await page
      .locator(
        `[data-testid="audit-period-control-row"][data-control-id="${CONTROL_ID}"]`,
      )
      .getByTestId("audit-period-control-open")
      .click();
    const workspace = page.getByTestId("audit-period-control-workspace");
    await expect(workspace).toBeVisible();
    await workspace.getByTestId("population-start").fill("2026-01-01");
    await workspace.getByTestId("population-end").fill("2026-03-31");
    await workspace.getByTestId("population-create").click();
    await workspace.getByTestId("sample-n-input").fill("1");
    await workspace.getByTestId("sample-seed-input").fill("test-3053");
    await workspace.getByTestId("sample-pull-submit").click();

    const card = workspace.locator(
      `[data-testid="sample-card"][data-sample-id="${SAMPLE_ID}"]`,
    );
    await expect(card.getByTestId("sample-freeze-revision")).toHaveText(
      "freeze revision 3",
    );
    const notice = card.getByTestId("sample-amended-by-import");
    await expect(notice).toContainText(
      "Amended by import: drawn from freeze revision 3",
    );
    await expect(notice).toContainText(`Reason: ${REASON}`);
  });
});
