// #1785 — the control import review page.
//
// Upload the fixture sheet, see each row's candidates with their source
// and "reaches N of M", approve one row by candidate and one by a
// searched anchor, reject one with a reason, run approve-clear, filter to
// the "no candidate reaches" row, and find the approved controls on the
// controls page.
//
// HERMETIC (slice 594): every BFF the flow touches is route-mocked to the
// production wire shape (internal/controlimport: Batch, BatchControl,
// Candidate, RowDecision, ApproveClearResult). The mocks share one
// in-memory batch, so a decision made on the review page is what the
// controls list then returns: the spec pins the page's requests and what
// it renders from the answers. Staging, approval and RLS are pinned
// server-side by internal/api/controlimports/*_integration_test.go.

import path from "node:path";

import type { Page, Route } from "@playwright/test";

import { expect, test } from "./fixtures";

const BATCH_ID = "b1785000-0000-4000-8000-000000000001";
const FIXTURE = path.join(__dirname, "data", "control-import-1785.csv");

type Req = { code: string; requirement_id: string };
type Cand = {
  rank: number;
  scf_anchor_id: string;
  scf_id: string;
  title: string;
  source: string;
  reaches: Req[];
  tested_count: number;
  ai_assisted: boolean;
};
type Row = {
  id: string;
  row_number: number;
  external_id: string;
  title: string;
  description: string;
  tested_under: Req[];
  implementation_type?: string;
  status: string;
  candidates: Cand[];
  no_candidate_reaches: boolean;
  llm_rerank: string;
  decided_by?: string;
  control_id?: string;
  approved_scf_id?: string;
  rejection_reason?: string;
};

const CC61 = { code: "CC6.1", requirement_id: "q1785-cc61" };
const CC62 = { code: "CC6.2", requirement_id: "q1785-cc62" };
const CC71 = { code: "CC7.1", requirement_id: "q1785-cc71" };

function cand(
  rank: number,
  scfID: string,
  title: string,
  source: string,
  reaches: Req[],
  tested: number,
): Cand {
  return {
    rank,
    scf_anchor_id: `a1785-${scfID}`,
    scf_id: scfID,
    title,
    source,
    reaches,
    tested_count: tested,
    ai_assisted: false,
  };
}

function freshRows(): Row[] {
  return [
    {
      id: "r1785-1",
      row_number: 2,
      external_id: "ORG-AC-01",
      title: "MFA for workforce (1785)",
      description:
        "All workforce accounts require multi-factor authentication.",
      tested_under: [CC61, CC62],
      implementation_type: "automated",
      status: "pending",
      candidates: [
        cand(
          1,
          "IAC-06",
          "Multi-factor authentication",
          "crosswalk",
          [CC61, CC62],
          2,
        ),
        cand(2, "IAC-10", "Authenticator management", "keyword", [], 2),
      ],
      no_candidate_reaches: false,
      llm_rerank: "off",
    },
    {
      id: "r1785-2",
      row_number: 3,
      external_id: "ORG-AC-02",
      title: "Quarterly access review (1785)",
      description: "Privileged access is reviewed every quarter.",
      tested_under: [CC62],
      implementation_type: "manual_periodic",
      status: "pending",
      candidates: [
        cand(1, "IAC-16", "Privileged account management", "keyword", [], 1),
        cand(
          2,
          "IAC-17",
          "Periodic review of account privileges",
          "crosswalk",
          [CC62],
          1,
        ),
      ],
      no_candidate_reaches: false,
      llm_rerank: "off",
    },
    {
      id: "r1785-3",
      row_number: 4,
      external_id: "ORG-OP-01",
      title: "Incident monitoring (1785)",
      description: "Security events are monitored and triaged daily.",
      tested_under: [CC71],
      status: "pending",
      candidates: [cand(1, "IRO-02", "Incident handling", "keyword", [], 1)],
      no_candidate_reaches: true,
      llm_rerank: "off",
    },
    {
      id: "r1785-4",
      row_number: 5,
      external_id: "ORG-HR-01",
      title: "Background checks (1785)",
      description: "New hires pass a background check before their start date.",
      tested_under: [],
      implementation_type: "manual_attested",
      status: "pending",
      candidates: [cand(1, "HRS-04", "Personnel screening", "keyword", [], 0)],
      no_candidate_reaches: false,
      llm_rerank: "off",
    },
  ];
}

const ANCHORS = [
  {
    id: "a1785-MON-01",
    scf_id: "MON-01",
    family: "Continuous Monitoring",
    name: "Continuous monitoring",
    description: "",
  },
  {
    id: "a1785-IAC-06",
    scf_id: "IAC-06",
    family: "Identification & Authentication",
    name: "Multi-factor authentication",
    description: "",
  },
];

type Captured = {
  upload?: string;
  approvals: { row: string; body: Record<string, unknown> }[];
  rejections: { row: string; body: Record<string, unknown> }[];
  clears: number;
};

function json(route: Route, status: number, body: unknown) {
  return route.fulfill({
    status,
    contentType: "application/json",
    body: JSON.stringify(body),
  });
}

async function mockImport(
  page: Page,
  opts: { clearStatus?: number } = {},
): Promise<Captured> {
  const rows = freshRows();
  const captured: Captured = { approvals: [], rejections: [], clears: 0 };

  function batch() {
    return {
      id: BATCH_ID,
      framework: { slug: "soc2", version_id: "fv1785", version: "2017" },
      source_filename: "control-import-1785.csv",
      source_format: "csv",
      uploaded_by: "u1785",
      status: "staged",
      total_rows: 5,
      staged_count: rows.length,
      rejected_count: 1,
      unmapped_columns: [],
      scf_release: { version_id: "scf1785", version: "2026.3" },
      suggestions_status: "ready",
      created_at: "2026-11-15T00:00:00Z",
      staged: rows,
      rejected: [
        {
          row_number: 6,
          external_id: "ORG-AC-03",
          reason: "missing_title",
          detail: "the row has no title",
        },
      ],
    };
  }

  function approve(row: Row, scfID: string) {
    row.status = "approved";
    row.approved_scf_id = scfID;
    row.control_id = `c1785-${row.id}`;
    row.decided_by = "u1785";
  }

  await page.route("**/api/controls/imports**", async (route) => {
    const req = route.request();
    const url = new URL(req.url());
    const p = url.pathname;
    if (p === "/api/controls/imports" && req.method() === "POST") {
      captured.upload = req.postDataBuffer()?.toString("utf8") ?? "";
      return json(route, 201, batch());
    }
    if (p === `/api/controls/imports/${BATCH_ID}`) {
      return json(route, 200, batch());
    }
    if (p === `/api/controls/imports/${BATCH_ID}/rows`) {
      const status = url.searchParams.get("status");
      const flagged = url.searchParams.get("no_candidate_reaches") === "true";
      return json(route, 200, {
        batch_id: BATCH_ID,
        scf_release: batch().scf_release,
        suggestions_status: "ready",
        rows: rows.filter(
          (r) =>
            (!status || r.status === status) &&
            (!flagged || r.no_candidate_reaches),
        ),
      });
    }
    if (p === `/api/controls/imports/${BATCH_ID}/approve-clear`) {
      captured.clears++;
      if (opts.clearStatus) {
        return json(route, opts.clearStatus, {
          error: "an approve-clear of this batch is already running",
        });
      }
      const clear = rows.filter((r) => {
        const top = r.candidates[0];
        return (
          r.status === "pending" &&
          top?.source === "crosswalk" &&
          r.tested_under.length > 0 &&
          top.reaches.length === r.tested_under.length
        );
      });
      clear.forEach((r) => approve(r, r.candidates[0].scf_id));
      return json(route, 200, {
        batch_id: BATCH_ID,
        qualifying: clear.length,
        approved_count: clear.length,
        approved: [],
        duplicates: [],
        skipped: [],
      });
    }
    const m = p.match(/\/rows\/([^/]+)\/(approve|reject)$/);
    if (m && req.method() === "POST") {
      const row = rows.find((r) => r.id === m[1]);
      const body = req.postDataJSON() as Record<string, unknown>;
      if (!row) return json(route, 404, { error: "not found" });
      if (m[2] === "approve") {
        captured.approvals.push({ row: row.id, body });
        const scfID =
          typeof body.scf_id === "string"
            ? body.scf_id
            : row.candidates.find((c) => c.rank === body.candidate_rank)!
                .scf_id;
        approve(row, scfID);
        return json(route, 200, {
          batch_id: BATCH_ID,
          row_id: row.id,
          status: "approved",
          scf_id: scfID,
          control_id: row.control_id,
        });
      }
      captured.rejections.push({ row: row.id, body });
      row.status = "rejected";
      row.rejection_reason = String(body.reason);
      return json(route, 200, { row_id: row.id, status: "rejected" });
    }
    return route.fallback();
  });

  await page.route("**/api/frameworks", (route) =>
    json(route, 200, {
      frameworks: [
        { id: "f1785", name: "SOC 2", slug: "soc2", issuer: "AICPA" },
      ],
    }),
  );
  await page.route("**/api/anchors", (route) =>
    json(route, 200, { anchors: ANCHORS }),
  );

  // The controls list returns a control for every approved row.
  await page.route("**/api/controls-list**", (route) => {
    const controls = rows
      .filter((r) => r.status === "approved")
      .map((r) => ({
        id: r.control_id,
        title: r.title,
        control_family: "Imported",
        scf_id: r.approved_scf_id,
        lifecycle_state: "active",
        bundle_id: `import-${r.external_id.toLowerCase()}`,
        scf_anchor_id: null,
        state: null,
        frameworks: [],
      }));
    return json(route, 200, {
      controls,
      count: controls.length,
      total: controls.length,
    });
  });
  await page.route("**/api/scope-cells**", (route) =>
    json(route, 200, { cells: [] }),
  );

  return captured;
}

function rowCard(page: Page, externalID: string) {
  return page.locator(
    `[data-testid="control-import-row"][data-external-id="${externalID}"]`,
  );
}

async function uploadFixture(page: Page) {
  await page.goto("/controls");
  await page.getByTestId("controls-import").click();
  await expect(page).toHaveURL(/\/controls\/imports$/);
  await page
    .getByTestId("control-import-framework")
    .selectOption({ label: "SOC 2" });
  await page.getByTestId("control-import-file").setInputFiles(FIXTURE);
  await page.getByTestId("control-import-submit").click();
  await expect(page).toHaveURL(new RegExp(`/controls/imports/${BATCH_ID}$`));
  await expect(page.getByTestId("control-import-row")).toHaveCount(4, {
    timeout: 30_000,
  });
}

test.describe("control import review (#1785)", () => {
  test("upload, review candidates, approve by candidate and by search, reject, approve-clear", async ({
    authedPage: page,
  }) => {
    const captured = await mockImport(page);
    await uploadFixture(page);

    // The upload carried the sheet and the framework slug. (Playwright's
    // intercepted multipart body omits the file's bytes, so its content
    // is not asserted here.)
    expect(captured.upload).toContain('name="framework"');
    expect(captured.upload).toContain("soc2");
    expect(captured.upload).toContain('filename="control-import-1785.csv"');

    // Progress and the row the upload refused.
    await expect(page.getByTestId("control-import-progress-staged")).toHaveText(
      "4",
    );
    await expect(
      page.getByTestId("control-import-progress-pending"),
    ).toHaveText("4");
    await expect(page.getByTestId("control-import-not-staged")).toContainText(
      "1 not staged at upload",
    );

    // Candidates show their source and reaches-N-of-M.
    const mfa = rowCard(page, "ORG-AC-01");
    const top = mfa.getByTestId("control-import-candidate").first();
    await expect(top).toHaveAttribute("data-scf-id", "IAC-06");
    await expect(top.getByTestId("control-import-candidate-source")).toHaveText(
      "Crosswalk",
    );
    await expect(top.getByTestId("control-import-candidate-fit")).toHaveText(
      "reaches 2 of 2: CC6.1, CC6.2",
    );
    await expect(
      mfa
        .getByTestId("control-import-candidate")
        .nth(1)
        .getByTestId("control-import-candidate-fit"),
    ).toHaveText("reaches 0 of 2");

    // Approve ORG-AC-02 with its second candidate.
    const review = rowCard(page, "ORG-AC-02");
    await review
      .locator('[data-testid="control-import-candidate"][data-scf-id="IAC-17"]')
      .getByTestId("control-import-approve-candidate")
      .click();
    await expect(review).toHaveAttribute("data-status", "approved");
    await expect(
      review.getByTestId("control-import-row-decision"),
    ).toContainText("IAC-17");

    // Approve ORG-OP-01 with a searched anchor, overriding the
    // implementation type.
    const monitoring = rowCard(page, "ORG-OP-01");
    await monitoring
      .getByTestId("control-import-impl-type")
      .selectOption("automated");
    await monitoring.getByTestId("control-import-search-anchor").click();
    await monitoring.getByTestId("scf-anchor-search").fill("MON");
    await monitoring
      .locator('[data-testid="scf-anchor-option"][data-scf-id="MON-01"]')
      .click();
    await expect(monitoring).toHaveAttribute("data-status", "approved");

    // Reject ORG-HR-01: the confirm stays disabled until a reason is given.
    const hr = rowCard(page, "ORG-HR-01");
    await hr.getByTestId("control-import-reject").click();
    const ok = hr.getByTestId("control-import-reject-confirm-ok");
    await expect(ok).toBeDisabled();
    await hr
      .getByTestId("control-import-reject-reason")
      .fill("Covered by the vendor's SOC 2");
    await ok.click();
    await expect(hr).toHaveAttribute("data-status", "rejected");
    await expect(hr.getByTestId("control-import-row-decision")).toHaveText(
      "Rejected: Covered by the vendor's SOC 2",
    );

    // Approve-clear asks first, then approves the clear row.
    await page.getByTestId("control-import-approve-clear").click();
    expect(captured.clears).toBe(0);
    await page.getByTestId("control-import-approve-clear-confirm-ok").click();
    await expect(mfa).toHaveAttribute("data-status", "approved");
    await expect(
      page
        .getByTestId("toast")
        .filter({ hasText: "Approved 1 of 1 clear row" }),
    ).toHaveCount(1);

    expect(captured.approvals).toEqual([
      { row: "r1785-2", body: { candidate_rank: 2 } },
      {
        row: "r1785-3",
        body: { scf_id: "MON-01", implementation_type: "automated" },
      },
    ]);
    expect(captured.rejections).toEqual([
      { row: "r1785-4", body: { reason: "Covered by the vendor's SOC 2" } },
    ]);
    expect(captured.clears).toBe(1);

    await expect(
      page.getByTestId("control-import-progress-approved"),
    ).toHaveText("3");
    await expect(
      page.getByTestId("control-import-progress-rejected"),
    ).toHaveText("1");
    await expect(
      page.getByTestId("control-import-progress-pending"),
    ).toHaveText("0");
    await expect(page.getByTestId("control-import-export")).toHaveAttribute(
      "href",
      `/api/controls/imports/${BATCH_ID}/export`,
    );

    // The approved controls are on the controls page.
    await page.getByRole("link", { name: "Back to controls" }).click();
    await expect(page).toHaveURL(/\/controls$/);
    const table = page.getByTestId("list-table-wrap");
    await expect(table.getByTestId("list-table-row")).toHaveCount(3, {
      timeout: 30_000,
    });
    for (const title of [
      "MFA for workforce (1785)",
      "Quarterly access review (1785)",
      "Incident monitoring (1785)",
    ]) {
      await expect(table.getByText(title)).toBeVisible();
    }
    await expect(table.getByText("Background checks (1785)")).toHaveCount(0);
  });

  test("the no-candidate-reaches filter shows the flagged row", async ({
    authedPage: page,
  }) => {
    await mockImport(page);
    await page.goto(`/controls/imports/${BATCH_ID}`);
    await expect(page.getByTestId("control-import-row")).toHaveCount(4, {
      timeout: 30_000,
    });

    await page.getByLabel("Fit").selectOption("none");
    await expect(page.getByTestId("control-import-row")).toHaveCount(1);
    const flagged = rowCard(page, "ORG-OP-01");
    await expect(flagged).toBeVisible();
    await expect(
      flagged.getByTestId("control-import-row-no-candidate-reaches"),
    ).toHaveText("No candidate reaches a tested criterion");

    // The status filter narrows on top of it.
    await page.getByLabel("Status").selectOption("approved");
    await expect(page.getByTestId("control-import-rows-empty")).toBeVisible();
  });

  test("an approve-clear already running is reported as a toast", async ({
    authedPage: page,
  }) => {
    await mockImport(page, { clearStatus: 409 });
    await page.goto(`/controls/imports/${BATCH_ID}`);
    await expect(page.getByTestId("control-import-row")).toHaveCount(4, {
      timeout: 30_000,
    });
    await page.getByTestId("control-import-approve-clear").click();
    await page.getByTestId("control-import-approve-clear-confirm-ok").click();
    await expect(page.getByTestId("toast")).toContainText(
      "an approve-clear of this batch is already running",
    );
    await expect(page.getByTestId("control-import-clear-error")).toHaveCount(0);
    await expect(rowCard(page, "ORG-AC-01")).toHaveAttribute(
      "data-status",
      "pending",
    );
  });
});
