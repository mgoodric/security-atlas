// #2848 — upload a risk register and map it before staging.
//
// The fixture's headers match no alias, so the platform offers its first
// row as the header with every column unmapped. The user maps the risk ID,
// title and category columns, sees the rest as unmapped and the preview
// follow the mapping, stages with that column map, maps the batch's one
// category value, approves it on the review page, and opens the risk.
//
// HERMETIC (slice 594): every BFF the flow touches is route-mocked to the
// production wire shape (internal/risk/riskimport: Preview, Batch,
// BatchRow, ValueMapping, ApproveResult). The mocks share one in-memory
// batch, so a value mapped on the upload page and an approval on the
// review page are what the next read returns. Header detection, staging,
// approval and RLS are pinned server-side by
// internal/api/risks/riskimports/*_integration_test.go.

import path from "node:path";

import type { Route } from "@playwright/test";

import { expect, test } from "./fixtures";

const BATCH_ID = "b2848000-0000-4000-8000-000000000001";
const ROW_ID = "b2848000-0000-4000-8000-0000000000a1";
const RISK_ID = "b2848000-0000-4000-8000-0000000000f1";
const FIXTURE = path.join(__dirname, "data", "risk-import-2848.csv");
const TITLE = "Phishing campaign against finance";

function json(route: Route, status: number, body: unknown) {
  return route.fulfill({
    status,
    contentType: "application/json",
    body: JSON.stringify(body),
  });
}

const PREVIEW = {
  methodology: "nist_800_30",
  header_row: 1,
  header_detected: false,
  columns: [
    { index: 0, header: "Ref No.", mappable: true },
    { index: 1, header: "Headline", mappable: true },
    { index: 2, header: "Bucket", mappable: true },
    { index: 3, header: "Notes", mappable: true },
  ],
  fields: [
    "accepted_until",
    "accepter",
    "category",
    "description",
    "external_ref",
    "owner",
    "title",
    "treatment",
  ].map((name) => ({
    name,
    required: name === "title" || name === "category",
  })),
  rows: [["X-2848", TITLE, "Operations", "reviewed quarterly"]],
  data_rows: 1,
};

const RISK = {
  id: RISK_ID,
  external_ref: "X-2848",
  title: TITLE,
  description: "",
  category: "operational",
  methodology: "nist_800_30",
  treatment: "untreated",
  treatment_owner: "",
  residual_score: {},
  accepter: "",
  instrument_reference: "",
  linked_control_ids: [],
  created_at: "2026-11-21T00:00:00Z",
  updated_at: "2026-11-21T00:00:00Z",
  themes: [],
  level: "team",
  severity: 0,
};

async function mockImport(page: import("@playwright/test").Page) {
  const captured = { preview: "", upload: "", values: [] as unknown[] };
  const row: Record<string, unknown> = {
    id: ROW_ID,
    row_number: 2,
    external_ref: "X-2848",
    title: TITLE,
    description: "",
    category_value: "Operations",
    category: "",
    theme_values: [],
    themes: [],
    treatment: "untreated",
    unmapped: { Notes: "reviewed quarterly" },
    flags: [],
    status: "pending",
  };
  const category = {
    kind: "category",
    value: "Operations",
    row_count: 1,
  } as Record<string, unknown>;
  const batch = () => ({
    id: BATCH_ID,
    source_filename: "risk-import-2848.csv",
    source_format: "csv",
    methodology: "nist_800_30",
    uploaded_by: "u2848",
    // #2881: the uploader never decides their own batch, so the platform
    // reads can_decide false for them. The mock stands for the reviewing
    // approver, who did not upload it.
    can_decide: true,
    status: row.status === "pending" ? "staged" : "completed",
    total_rows: 1,
    staged_count: 1,
    rejected_count: 0,
    unmapped_columns: ["Notes"],
    category_values: [category],
    theme_values: [],
    created_at: "2026-11-21T00:00:00Z",
    staged: [row],
    rejected: [],
  });

  await page.route("**/api/me", (route) =>
    json(route, 200, {
      user_id: "u2848",
      tenant_id: "e5e5e5e5-0000-4000-8000-000000002848",
      display_name: "Abel Approver",
      email: "approver@example.test",
      idp_subject: "",
      tenant_role: "admin",
      time_zone: null,
      theme: null,
      is_admin: true,
      is_tenant_admin: true,
      is_approver: true,
      owner_roles: [],
      roles: ["admin"],
    }),
  );
  await page.route("**/api/risks/categories**", (route) =>
    json(route, 200, {
      categories: [
        {
          id: "c2848",
          slug: "operational",
          name: "Operational",
          description: "",
          archived: false,
          is_default: true,
          risk_count: 0,
          rule_count: 0,
        },
      ],
    }),
  );
  await page.route("**/api/risks-hierarchy/themes", (route) =>
    json(route, 200, { themes: [], count: 0 }),
  );
  await page.route("**/api/risks/imports**", async (route) => {
    const req = route.request();
    const p = new URL(req.url()).pathname;
    if (p === "/api/risks/imports/preview" && req.method() === "POST") {
      captured.preview = req.postDataBuffer()?.toString("utf8") ?? "";
      return json(route, 200, PREVIEW);
    }
    if (p === "/api/risks/imports" && req.method() === "POST") {
      captured.upload = req.postDataBuffer()?.toString("utf8") ?? "";
      return json(route, 201, batch());
    }
    if (
      p === `/api/risks/imports/${BATCH_ID}/values` &&
      req.method() === "PUT"
    ) {
      const body = req.postDataJSON() as { target?: string };
      captured.values.push(body);
      category.target = body.target;
      row.category = body.target;
      return json(route, 200, { ...category, mapped_by: "u2848" });
    }
    if (p === `/api/risks/imports/${BATCH_ID}`) {
      return json(route, 200, batch());
    }
    if (
      p === `/api/risks/imports/${BATCH_ID}/approve` &&
      req.method() === "POST"
    ) {
      Object.assign(row, {
        status: "approved",
        risk_id: RISK_ID,
        decided_by: "u2848",
      });
      const d = {
        batch_id: BATCH_ID,
        row_id: ROW_ID,
        row_number: 2,
        external_ref: "X-2848",
        status: "approved",
        decided_by: "u2848",
        risk_id: RISK_ID,
      };
      return json(route, 200, {
        batch_id: BATCH_ID,
        approved_count: 1,
        approved: [d],
        duplicates: [],
        rejected: [],
        skipped: [],
      });
    }
    return route.fallback();
  });
  await page.route(`**/api/risks/${RISK_ID}`, (route) =>
    json(route, 200, { risk: RISK }),
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
  return captured;
}

test.describe("risk import upload with the column-mapping step (#2848)", () => {
  test("upload, map columns, stage, map values, approve, open the risk", async ({
    authedPage: page,
  }) => {
    const captured = await mockImport(page);

    await page.goto("/risks/imports");
    await page.getByTestId("risk-import-file").setInputFiles(FIXTURE);
    await page
      .getByTestId("risk-import-methodology")
      .selectOption("qualitative_5x5");
    await page.getByTestId("risk-import-read").click();

    // The preview carried the methodology; no header matched an alias.
    await expect(page.getByTestId("risk-import-header-note")).toContainText(
      "row 1 is offered as the header",
      { timeout: 30_000 },
    );
    expect(captured.preview).toContain('name="methodology"');
    expect(captured.preview).toContain("qualitative_5x5");
    expect(captured.preview).not.toContain('name="column_map"');

    const mapping = page.getByTestId("risk-import-mapping");
    const select = (header: string) =>
      mapping
        .locator(
          `[data-testid="risk-import-mapping-row"][data-header="${header}"]`,
        )
        .getByTestId("risk-import-mapping-select");

    // Nothing is mapped, so staging waits for title and category.
    await expect(page.getByTestId("risk-import-mapping-missing")).toContainText(
      "Title and Category",
    );
    await expect(page.getByTestId("risk-import-stage")).toBeDisabled();

    await select("Ref No.").selectOption("external_ref");
    await select("Headline").selectOption("title");
    await select("Bucket").selectOption("category");
    await expect(page.getByTestId("risk-import-mapping-missing")).toHaveCount(
      0,
    );
    await expect(
      page.getByTestId("risk-import-mapping-unmapped"),
    ).toContainText("Notes");
    // The preview reads the row under the mapped fields.
    const preview = page.getByTestId("risk-import-mapping-preview");
    await expect(preview.locator("thead th")).toHaveText([
      "Risk ID",
      "Title",
      "Category",
      "Notes",
    ]);
    await expect(preview.locator("tbody tr").first()).toContainText(TITLE);

    await page.getByTestId("risk-import-stage").click();

    // The upload carried the column map, and the unmapped column ignored.
    await expect(page.getByTestId("risk-import-value-step")).toBeVisible({
      timeout: 30_000,
    });
    for (const m of [
      "external_ref=Ref No.",
      "title=Headline",
      "category=Bucket",
    ]) {
      expect(captured.upload).toContain(m);
    }
    expect(captured.upload).toContain('name="ignore_column"');
    expect(captured.upload).toContain("qualitative_5x5");

    // The batch's one category value maps to an existing category.
    await expect(page.getByTestId("risk-import-value-unmapped")).toHaveText(
      "1 unmapped",
    );
    await page
      .locator('[data-testid="risk-import-value-row"][data-value="Operations"]')
      .getByTestId("risk-import-value-select")
      .selectOption("operational");
    await expect(page.getByTestId("risk-import-value-unmapped")).toHaveText(
      "0 unmapped",
    );
    expect(captured.values).toEqual([
      { kind: "category", value: "Operations", target: "operational" },
    ]);

    await page.getByTestId("risk-import-to-review").click();
    await expect(page).toHaveURL(new RegExp(`/risks/imports/${BATCH_ID}$`));
    await expect(page.getByTestId("risk-import-row")).toHaveCount(1, {
      timeout: 30_000,
    });

    await page.getByTestId("risk-import-approve-all").click();
    await page.getByTestId("risk-import-approve-all-confirm-ok").click();
    const decision = page.getByTestId("risk-import-row-decision");
    await expect(decision).toContainText("Approved.", { timeout: 30_000 });

    await decision.getByRole("link", { name: "Open risk" }).click();
    await expect(page).toHaveURL(new RegExp(`/risks/${RISK_ID}$`));
    await expect(page.getByTestId("risk-detail")).toContainText(TITLE, {
      timeout: 30_000,
    });
  });
});
