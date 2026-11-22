// #2836 — upload a control sheet whose headers match no alias, map its
// columns in the shared mapping step, and stage it.
//
// The fixture has a title row above its header, and none of its headers is
// an alias, so the platform offers its fullest early row as the header with
// every column unmapped. The user maps the control ID, title, description
// and tested-under columns, sees the rest as unmapped and the preview follow
// the mapping, stages with that column map, and lands on the batch's review
// page with the row staged.
//
// HERMETIC (slice 594): every BFF the flow touches is route-mocked to the
// production wire shape (internal/controlimport: Preview, Batch,
// BatchControl). Header detection, the column map, ignore_column and RLS are
// pinned server-side by internal/controlimport/preview_test.go and
// internal/api/controlimports/preview_integration_test.go.

import path from "node:path";

import type { Page, Route } from "@playwright/test";

import { expect, test } from "./fixtures";

const BATCH_ID = "b2836000-0000-4000-8000-000000000001";
const FIXTURE = path.join(__dirname, "data", "control-import-2836.csv");
const TITLE = "Nightly backups (2836)";

function json(route: Route, status: number, body: unknown) {
  return route.fulfill({
    status,
    contentType: "application/json",
    body: JSON.stringify(body),
  });
}

const PREVIEW = {
  header_row: 2,
  header_detected: false,
  columns: ["Key", "Headline", "Body", "Criteria Ref", "Kind"].map(
    (header, index) => ({ index, header, mappable: true }),
  ),
  fields: [
    "id",
    "title",
    "description",
    "tested_under",
    "implementation_type",
    "applicability",
  ].map((name) => ({
    name,
    required: ["id", "title", "description"].includes(name),
  })),
  rows: [
    [
      "X-2836",
      TITLE,
      "Production databases are backed up nightly.",
      "CC7.2",
      "manual",
    ],
  ],
  data_rows: 1,
};

async function mockImport(page: Page) {
  const captured = { preview: "", upload: "" };
  const row = {
    id: "r2836-1",
    row_number: 3,
    external_id: "X-2836",
    title: TITLE,
    description: "Production databases are backed up nightly.",
    tested_under: [{ code: "CC7.2", requirement_id: "q2836-cc72" }],
    status: "pending",
    candidates: [],
    no_candidate_reaches: true,
    llm_rerank: "off",
  };
  const batch = {
    id: BATCH_ID,
    framework: { slug: "soc2", version_id: "fv2836", version: "2017" },
    source_filename: "control-import-2836.csv",
    source_format: "csv",
    uploaded_by: "u2836",
    status: "staged",
    total_rows: 1,
    staged_count: 1,
    rejected_count: 0,
    unmapped_columns: ["Kind"],
    scf_release: { version_id: "scf2836", version: "2026.3" },
    suggestions_status: "ready",
    created_at: "2026-11-22T00:00:00Z",
    staged: [row],
    rejected: [],
  };

  await page.route("**/api/frameworks", (route) =>
    json(route, 200, {
      frameworks: [
        { id: "f2836", name: "SOC 2", slug: "soc2", issuer: "AICPA" },
      ],
    }),
  );
  await page.route("**/api/controls/imports**", async (route) => {
    const req = route.request();
    const url = new URL(req.url());
    const p = url.pathname;
    if (p === "/api/controls/imports/preview" && req.method() === "POST") {
      captured.preview = req.postDataBuffer()?.toString("utf8") ?? "";
      return json(route, 200, PREVIEW);
    }
    if (p === "/api/controls/imports" && req.method() === "POST") {
      captured.upload = req.postDataBuffer()?.toString("utf8") ?? "";
      return json(route, 201, batch);
    }
    if (p === `/api/controls/imports/${BATCH_ID}`) {
      return json(route, 200, batch);
    }
    if (p === `/api/controls/imports/${BATCH_ID}/rows`) {
      return json(route, 200, {
        batch_id: BATCH_ID,
        scf_release: batch.scf_release,
        suggestions_status: "ready",
        rows: [row],
      });
    }
    return route.fallback();
  });
  await page.route("**/api/anchors", (route) =>
    json(route, 200, { anchors: [] }),
  );
  await page.route("**/api/scope-cells**", (route) =>
    json(route, 200, { cells: [] }),
  );
  await page.route("**/api/scope-dimensions**", (route) =>
    json(route, 200, { dimensions: [] }),
  );
  return captured;
}

test.describe("control import upload with the column-mapping step (#2836)", () => {
  test("upload a sheet with no alias headers, map its columns, stage it", async ({
    authedPage: page,
  }) => {
    const captured = await mockImport(page);

    await page.goto("/controls/imports");
    await page
      .getByTestId("control-import-framework")
      .selectOption({ label: "SOC 2" });
    await page.getByTestId("control-import-file").setInputFiles(FIXTURE);
    await page.getByTestId("control-import-read").click();

    // No header matched an alias, so row 2 is offered.
    await expect(page.getByTestId("control-import-header-note")).toContainText(
      "row 2 is offered as the header",
      { timeout: 30_000 },
    );
    expect(captured.preview).toContain('filename="control-import-2836.csv"');
    expect(captured.preview).not.toContain('name="column_map"');

    const mapping = page.getByTestId("control-import-mapping");
    const select = (header: string) =>
      mapping
        .locator(
          `[data-testid="control-import-mapping-row"][data-header="${header}"]`,
        )
        .getByTestId("control-import-mapping-select");

    // The step lists the controls fields, and nothing is mapped yet.
    await expect(select("Key").locator("option")).toHaveText([
      "Not imported",
      "Control ID (required)",
      "Title (required)",
      "Description (required)",
      "Tested under",
      "Implementation type",
      "Applicability",
    ]);
    await expect(
      page.getByTestId("control-import-mapping-missing"),
    ).toContainText("Control ID and Title and Description");
    await expect(page.getByTestId("control-import-submit")).toBeDisabled();

    await select("Key").selectOption("id");
    await select("Headline").selectOption("title");
    await select("Body").selectOption("description");
    await select("Criteria Ref").selectOption("tested_under");
    await expect(
      page.getByTestId("control-import-mapping-missing"),
    ).toHaveCount(0);
    await expect(
      page.getByTestId("control-import-mapping-unmapped"),
    ).toContainText("Kind");
    const preview = page.getByTestId("control-import-mapping-preview");
    await expect(preview.locator("thead th")).toHaveText([
      "Control ID",
      "Title",
      "Description",
      "Tested under",
      "Kind",
    ]);
    await expect(preview.locator("tbody tr").first()).toContainText(TITLE);

    await page.getByTestId("control-import-submit").click();
    await expect(page).toHaveURL(new RegExp(`/controls/imports/${BATCH_ID}$`), {
      timeout: 30_000,
    });

    // The upload carried the framework, the column map, and the unmapped
    // column ignored.
    for (const m of [
      "id=Key",
      "title=Headline",
      "description=Body",
      "tested_under=Criteria Ref",
    ]) {
      expect(captured.upload).toContain(m);
    }
    expect(captured.upload).toContain('name="ignore_column"');
    expect(captured.upload).toContain("Kind");
    expect(captured.upload).toContain('name="framework"');
    expect(captured.upload).toContain("soc2");

    await expect(page.getByTestId("control-import-row")).toHaveCount(1, {
      timeout: 30_000,
    });
  });
});
