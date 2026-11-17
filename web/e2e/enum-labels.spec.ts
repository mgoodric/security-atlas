// #2083 — pages show display labels, never raw enum values.
//
// The BFF responses are mocked with the raw values the audit found on
// screen (qualitative_5x5, nist_800_30, intersects_with, manual_attested,
// lowercase treatments), so each test proves the page turns them into
// labels rather than relying on whatever the seed happens to hold. The
// label table itself is checked against the backend's enums by
// lib/labels/enum-labels.test.ts.

import { expect, seeded, test } from "./fixtures";
import type { Page } from "@playwright/test";

import { seedFromFixture } from "./seed";

const RAW_VALUES = [
  "qualitative_5x5",
  "nist_800_30",
  "intersects_with",
  "manual_attested",
];

// Raw treatment values, matched whole-word and lowercase so "Mitigate" and
// "Accept (proposed)" pass but a bare "mitigate" does not.
const RAW_TREATMENT = /\b(untreated|mitigate|transfer|accept|avoid)\b/;

async function expectNoRawValues(page: Page) {
  const text = await page.locator("body").innerText();
  for (const raw of RAW_VALUES) {
    expect(text, `page shows the raw value ${raw}`).not.toContain(raw);
  }
}

async function expectLabelledTreatments(page: Page, testid: string) {
  const cells = page.getByTestId(testid);
  await expect(cells.first()).toBeVisible();
  for (const text of await cells.allInnerTexts()) {
    expect(text, `treatment cell shows ${text}`).not.toMatch(RAW_TREATMENT);
  }
}

function json(body: unknown) {
  return {
    status: 200,
    contentType: "application/json",
    body: JSON.stringify(body),
  };
}

const RISK_ID = "00000000-0000-4000-8000-000000002083";

function mockRisk(overrides: Record<string, unknown> = {}) {
  return {
    id: RISK_ID,
    title: "Unlabelled enum risk",
    description: "A risk whose enum values must render as labels",
    category: "operational",
    methodology: "qualitative_5x5",
    inherent_score: { likelihood: 3, impact: 4 },
    treatment: "mitigate",
    treatment_owner: "owner-a",
    residual_score: { likelihood: 2, impact: 3 },
    review_due_at: "2026-12-01T00:00:00Z",
    accepter: "",
    instrument_reference: "",
    linked_control_ids: [],
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    themes: [],
    severity: 12,
    ...overrides,
  };
}

const MOCK_RISKS = [
  mockRisk(),
  mockRisk({
    id: "00000000-0000-4000-8000-000000002084",
    title: "Accepted risk",
    methodology: "nist_800_30",
    treatment: "accept",
    acceptance_status: "proposed",
  }),
  mockRisk({
    id: "00000000-0000-4000-8000-000000002085",
    title: "Undecided risk",
    treatment: "untreated",
  }),
];

test.describe("enum display labels (#2083)", () => {
  test("Dashboard: posture tiles show name plus version, top risks show labels", async ({
    authedPage: page,
    baseURL,
  }) => {
    // The dashboard layout prefetches its panels server-side, where a
    // browser-side page.route mock cannot reach. The test-only
    // e2e_no_prefetch cookie (honoured only under ATLAS_TEST_MODE=1)
    // makes the page fetch client-side, as dashboard.spec.ts does.
    await page.context().addCookies([
      {
        name: "e2e_no_prefetch",
        value: "1",
        url: baseURL ?? "http://localhost:3000",
      },
    ]);
    await page.route("**/api/dashboard/framework-posture", (route) =>
      route.fulfill(
        json({
          count: 3,
          frameworks: [
            {
              framework_id: "00000000-0000-4000-8000-0000000020a1",
              framework_name: "ISO/IEC 27001",
              framework_version: "2022",
              framework_version_id: "00000000-0000-4000-8000-0000000020b1",
              scope_set: true,
              coverage_pct: 81.5,
              covered_by_exception_pct: 0,
              freshness_composite: 90,
              trend_delta_90d: 2,
            },
            {
              // The name already carries its version: never doubled.
              framework_id: "00000000-0000-4000-8000-0000000020a2",
              framework_name: "SCF 2025.2",
              framework_version: "2025.2",
              framework_version_id: "00000000-0000-4000-8000-0000000020b2",
              scope_set: false,
              coverage_pct: null,
              covered_by_exception_pct: null,
              freshness_composite: null,
              trend_delta_90d: null,
            },
            {
              framework_id: "00000000-0000-4000-8000-0000000020a3",
              framework_name: "NIST CSF",
              framework_version: "2.0",
              framework_version_id: "00000000-0000-4000-8000-0000000020b3",
              scope_set: true,
              coverage_pct: 60,
              covered_by_exception_pct: 0,
              freshness_composite: 75,
              trend_delta_90d: 0,
            },
          ],
        }),
      ),
    );
    await page.route("**/api/dashboard/risks", (route) =>
      route.fulfill(json({ risks: MOCK_RISKS })),
    );

    await page.goto("/dashboard");
    const tiles = page.getByTestId("framework-tile");
    await expect(tiles).toHaveCount(3);
    await expect(tiles.nth(0)).toContainText("ISO/IEC 27001:2022");
    await expect(tiles.nth(1)).toContainText("SCF 2025.2");
    await expect(tiles.nth(1)).not.toContainText("SCF 2025.2 2025.2");
    await expect(tiles.nth(2)).toContainText("NIST CSF 2.0");

    // #2135: the top-risks card shows band, owner and treatment, not the
    // methodology; the raw methodology code must still never render.
    await expect(page.getByTestId("top-risk-row").first()).not.toContainText(
      "qualitative_5x5",
    );
    // No hash prefix under the title.
    await expect(page.getByTestId("top-risk-row").first()).not.toContainText(
      RISK_ID.slice(0, 8),
    );
    await expectLabelledTreatments(page, "top-risk-treatment");
    await expectNoRawValues(page);
  });

  test("Dashboard: real posture tiles never show a bare version", async ({
    authedPage: page,
  }) => {
    await page.goto("/dashboard");
    const tiles = page.getByTestId("framework-tile");
    const empty = page.getByTestId("framework-posture-empty");
    await expect(tiles.or(empty).first()).toBeVisible();
    for (const text of await tiles.allInnerTexts()) {
      const heading = text.split("\n")[0].trim();
      expect(heading, "tile heading is only a version").not.toMatch(
        /^v?[\d.:]+$/,
      );
    }
  });

  test("Risk register", async ({ authedPage: page }) => {
    await page.route("**/api/risks-hierarchy/org-units**", (route) =>
      route.fulfill(json({ org_units: [] })),
    );
    await page.route(/\/api\/risks(\?.*)?$/, (route) =>
      route.fulfill(json({ risks: MOCK_RISKS })),
    );
    await page.goto("/risks");
    await expect(page.getByTestId("risks-row-title").first()).toBeVisible();
    await expectLabelledTreatments(page, "risks-row-treatment");
    await expect(page.getByTestId("risks-row-treatment").nth(1)).toHaveText(
      "Accept (proposed)",
    );
    // No hash prefix under or beside a title (#2083). The register's ID
    // is its own column since #2132, never part of the title cell.
    const table = page.getByTestId("list-table-wrap");
    for (const text of await table
      .getByTestId("list-cell-title")
      .allInnerTexts()) {
      expect(text, `title cell shows ${text}`).not.toMatch(/#|[0-9a-f]{8}/);
    }
    await expectNoRawValues(page);
  });

  test("Risk detail", async ({ authedPage: page }) => {
    await page.route(`**/api/risks/${RISK_ID}`, (route) =>
      route.fulfill(json({ risk: mockRisk() })),
    );
    await page.goto(`/risks/${RISK_ID}`);
    await expect(page.getByTestId("risk-detail-title")).toBeVisible();
    await expect(page.getByTestId("risk-detail-methodology")).toHaveText(
      "Qualitative 5×5",
    );
    await expect(page.getByTestId("risk-detail-treatment")).toHaveText(
      "Mitigate",
    );
    await expectNoRawValues(page);
  });

  test("Add risk: methodology and treatment options are labelled", async ({
    authedPage: page,
  }) => {
    await page.goto("/risks/new");
    const methodology = page.getByTestId("risks-create-methodology");
    await expect(methodology).toBeVisible();
    const methodologyOptions = await methodology
      .locator("option")
      .allInnerTexts();
    expect(methodologyOptions).toContain("NIST SP 800-30");
    expect(methodologyOptions).toContain("Qualitative 5×5");
    for (const o of await page
      .getByTestId("risks-create-treatment")
      .locator("option")
      .allInnerTexts()) {
      expect(o).not.toMatch(RAW_TREATMENT);
    }
    await expectNoRawValues(page);
  });

  test("Controls", async ({ authedPage: page }) => {
    await page.goto("/controls");
    await expect(page.getByTestId("list-table-wrap")).toBeVisible();
    await expectNoRawValues(page);
  });

  test.describe("Control detail", () => {
    test.beforeAll(() => {
      seedFromFixture("control-detail");
    });

    test("implementation type and STRM relationship are labelled", async ({
      authedPage: page,
    }) => {
      await page.route(
        `**/api/controls/${seeded.controlId}/coverage`,
        (route) =>
          route.fulfill(
            json({
              control: {
                id: seeded.controlId,
                tenant_id: seeded.tenantId,
                bundle_id: "CTRL-2083",
                version: 1,
                title: "Access review",
                control_family: "Workforce IAM",
                owner_role: "Security Engineering",
                implementation_type: "manual_attested",
                lifecycle_state: "active",
                freshness_class: "quarterly",
              },
              anchor: {
                id: "11111111-1111-1111-1111-111111112083",
                scf_id: "SCF:IAC-17",
                family: "IAC",
                name: "Periodic review of account privileges",
                description: "Access review anchor",
              },
              requirements: [
                {
                  edge_id: "00000000-0000-0000-0000-0000000020e1",
                  requirement_id: "A.5.18",
                  code: "A.5.18",
                  title: "Access rights",
                  framework_slug: "iso27001",
                  framework_name: "ISO/IEC 27001",
                  framework_version: "2022",
                  framework_version_id: seeded.frameworkVersionId,
                  framework_version_status: "active",
                  relationship_type: "intersects_with",
                  strength: 0.6,
                  coverage: 0.5,
                  source_attribution: "scf",
                },
              ],
            }),
          ),
      );
      const coverage = page.waitForResponse(
        (r) =>
          r.url().includes(`/api/controls/${seeded.controlId}/coverage`) &&
          r.status() === 200,
        { timeout: 30_000 },
      );
      await page.goto(`/controls/${seeded.controlId}`);
      await coverage;
      await expect(page.getByTestId("control-implementation-type")).toHaveText(
        "Manually attested",
      );
      await expect(page.locator("span[data-strm=intersects_with]")).toHaveText(
        "Intersects with",
      );
      await expect(page.getByText("ISO/IEC 27001:2022").first()).toBeVisible();
      await expectNoRawValues(page);
    });
  });
});
