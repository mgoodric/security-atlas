// #1794 — the Statement of Applicability tab on the framework-scope page.
//
// Seeded by fixtures/e2e/framework-scope-soa.sql: an ISO/IEC 27001 version
// (`2022-soa-e2e`) with eight requirements SOA-01..SOA-08. SOA-01, SOA-02
// and SOA-03 have controls behind them (C1 and C2 passing, C3 failing), so
// dashboard coverage starts at 3/8 = 37.5%. The fixture deletes every scope
// revision of the version; beforeAll creates a fresh draft through the API
// and excludes SOA-01, which passing C1 sits behind: the contradiction.
//
// The tests run in order against that one revision (serial mode, so a
// retry re-runs the whole group from a fresh seed):
//   1. bulk-exclude SOA-06..08 with one justification; counts update
//   2. the contradiction filter shows SOA-01 alone
//   3. a CSV import applies its good row and reports the two bad ones
//   4. export downloads the CSV and the xlsx
//   5. submit, approve and activate on the Scopes tab; dashboard coverage
//      moves from 37.5% to 2/4 = 50.0% (SOA-01, 06, 07, 08 excluded), and
//      the tab turns read-only.
//
// #2101: the e2e user submits and approves, so the spec turns on the
// tenant's Allow self-approval for its run (and off in afterAll); the
// approved scope carries the "Self-approved" marker.

import { expect, test } from "./fixtures";
import { DEMO_TENANT_ID, seedFromFixture } from "./seed";

const FV = "e2e17940-0000-0000-0000-0000000f0003";
const SOA_01 = "e2e17940-0000-0000-0000-00000000b001";
const VERSION_LABEL = "2022-soa-e2e";

function atlasBaseURL(): string {
  return process.env.ATLAS_HTTP_URL ?? "http://localhost:8080";
}

async function atlas(path: string, method: string, body: unknown) {
  const res = await fetch(`${atlasBaseURL()}${path}`, {
    method,
    headers: {
      Authorization: `Bearer ${process.env.TEST_BEARER}`,
      "Content-Type": "application/json",
    },
    body: JSON.stringify(body),
  });
  if (!res.ok) {
    throw new Error(`${method} ${path}: ${res.status} ${await res.text()}`);
  }
  return res.json();
}

test.describe.configure({ mode: "serial" });

test.describe("framework scope · Statement of Applicability tab (#1794)", () => {
  test.beforeAll(async () => {
    seedFromFixture("framework-scope-soa");
    await atlas(`/v1/tenants/${DEMO_TENANT_ID}`, "PATCH", {
      allow_self_approval: true,
    });
    const { framework_scope } = (await atlas("/v1/framework-scopes", "POST", {
      framework_version_id: FV,
      name: "ISMS scope (e2e)",
      predicate: { op: "true" },
    })) as { framework_scope: { id: string } };
    await atlas(
      `/v1/framework-scopes/${framework_scope.id}/applicability/${SOA_01}`,
      "PUT",
      {
        decision: "excluded",
        justification: "No shared accounts in scope",
        implementation_ref: "",
      },
    );
  });

  test.afterAll(async () => {
    await atlas(`/v1/tenants/${DEMO_TENANT_ID}`, "PATCH", {
      allow_self_approval: false,
    });
  });

  test("bulk-excludes three requirements with a justification and the counts update", async ({
    authedPage: page,
  }) => {
    await page.goto(`/framework-scopes/${FV}`);
    const tab = page.getByTestId("framework-scope-tab-soa");
    await expect(tab).toHaveText("Statement of Applicability");
    await tab.click();
    await expect(page.getByTestId("soa-title")).toHaveText(
      "Statement of Applicability",
    );

    await expect(page.getByTestId("soa-count-excluded")).toHaveText("1");
    await expect(page.getByTestId("soa-count-undecided")).toHaveText("7");
    await expect(page.getByTestId("soa-count-applicable")).toHaveText("0");
    await expect(page.getByTestId("soa-count-contradictions")).toHaveText("1");

    for (const code of ["SOA-06", "SOA-07", "SOA-08"]) {
      await page
        .locator(`[data-testid="soa-row"][data-code="${code}"]`)
        .getByTestId("soa-row-select")
        .click();
    }
    await expect(page.getByTestId("soa-bulk-count")).toHaveText("3 selected");
    await page.getByTestId("soa-bulk-decision").selectOption("excluded");
    await page
      .getByTestId("soa-bulk-justification")
      .fill("No in-house development or monitored premises");
    await page.getByTestId("soa-bulk-apply").click();

    await expect(page.getByTestId("soa-count-excluded")).toHaveText("4");
    await expect(page.getByTestId("soa-count-undecided")).toHaveText("4");
    await expect(page.getByTestId("soa-count-contradictions")).toHaveText("1");
    await expect(page.getByTestId("soa-bulk-bar")).toHaveCount(0);
    await expect(
      page
        .locator('[data-testid="soa-row"][data-code="SOA-07"]')
        .getByTestId("soa-row-justification"),
    ).toHaveValue("No in-house development or monitored premises");
  });

  test("the contradiction filter shows the flagged requirement", async ({
    authedPage: page,
  }) => {
    await page.goto(`/framework-scopes/${FV}`);
    await page.getByTestId("framework-scope-tab-soa").click();
    await page.getByTestId("soa-filter-contradiction").click();

    const rows = page.getByTestId("soa-row");
    await expect(rows).toHaveCount(1);
    await expect(rows.first()).toHaveAttribute("data-code", "SOA-01");
    await expect(rows.first().getByTestId("soa-contradiction")).toBeVisible();
    // The counts still describe the whole revision.
    await expect(page.getByTestId("soa-count-excluded")).toHaveText("4");
  });

  test("a CSV import applies good rows and reports the bad ones", async ({
    authedPage: page,
  }) => {
    await page.goto(`/framework-scopes/${FV}`);
    await page.getByTestId("framework-scope-tab-soa").click();
    await expect(page.getByTestId("soa-count-applicable")).toHaveText("0");

    const csv = [
      "requirement_code,decision,justification,implementation_ref",
      "SOA-02,applicable,,ISMS-IAM-02",
      "NOPE-99,excluded,Not used,",
      "SOA-04,excluded,,",
    ].join("\n");
    await page.getByTestId("soa-import-file").setInputFiles({
      name: "soa.csv",
      mimeType: "text/csv",
      buffer: Buffer.from(csv),
    });

    await expect(page.getByTestId("soa-import-summary")).toHaveText(
      "Imported 1 row; 2 rejected",
    );
    const rejected = page.getByTestId("soa-import-rejected");
    await expect(rejected).toHaveCount(2);
    await expect(rejected.nth(0)).toContainText("Line 3 (NOPE-99)");
    await expect(rejected.nth(0)).toContainText("unknown requirement code");
    await expect(rejected.nth(1)).toContainText("Line 4 (SOA-04)");
    await expect(rejected.nth(1)).toContainText("justification is required");

    await expect(page.getByTestId("soa-count-applicable")).toHaveText("1");
    await expect(page.getByTestId("soa-count-undecided")).toHaveText("3");
  });

  test("export downloads the Statement of Applicability", async ({
    authedPage: page,
  }) => {
    await page.goto(`/framework-scopes/${FV}`);
    await page.getByTestId("framework-scope-tab-soa").click();

    const csvDownload = page.waitForEvent("download");
    await page.getByTestId("soa-export-csv").click();
    const csv = await csvDownload;
    expect(csv.suggestedFilename()).toBe(
      "statement-of-applicability-iso27001-2022-soa-e2e.csv",
    );
    const chunks: Buffer[] = [];
    for await (const chunk of await csv.createReadStream()) {
      chunks.push(chunk as Buffer);
    }
    const body = Buffer.concat(chunks).toString("utf8");
    expect(body).toContain("SOA-01");
    expect(body).toContain("No shared accounts in scope");

    const xlsxDownload = page.waitForEvent("download");
    await page.getByTestId("soa-export-xlsx").click();
    expect((await xlsxDownload).suggestedFilename()).toBe(
      "statement-of-applicability-iso27001-2022-soa-e2e.xlsx",
    );
  });

  test("submitting, approving and activating the scope changes dashboard coverage", async ({
    authedPage: page,
  }) => {
    const coverage = page
      .getByTestId("framework-tile")
      .filter({ hasText: VERSION_LABEL })
      .getByTestId("framework-tile-coverage");

    await page.goto("/dashboard");
    await expect(coverage).toHaveText("37.5%");

    await page.goto(`/framework-scopes/${FV}`);
    await page
      .getByRole("button", { name: "Submit for review", exact: true })
      .click();
    await page.getByRole("button", { name: "Approve", exact: true }).click();
    await expect(page.getByTestId("framework-scope-self-approved")).toHaveText(
      "Self-approved",
    );
    await page.getByRole("button", { name: "Activate", exact: true }).click();
    await page.getByRole("button", { name: "Confirm", exact: true }).click();
    await expect(page.getByText("Current scope")).toBeVisible();

    // Activated decisions are frozen; the tab says so and offers no edits.
    await page.getByTestId("framework-scope-tab-soa").click();
    await expect(page.getByTestId("soa-revision-state")).toHaveText(
      "activated",
    );
    await expect(page.getByTestId("soa-read-only")).toBeVisible();
    await expect(page.getByTestId("soa-row-select")).toHaveCount(0);

    // Exclusions now leave coverage: 2 covered of the 4 applicable.
    await expect(async () => {
      await page.goto("/dashboard");
      await expect(coverage).toHaveText("50.0%", { timeout: 2_000 });
    }).toPass({ timeout: 15_000 });
  });
});
