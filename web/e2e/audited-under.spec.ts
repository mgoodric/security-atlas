// Issue #1802 — the "audited under" report page (/catalog/audited-under).
//
//   * the report lists the fixture gap; Add overlay edge with a rationale
//     posts the edge (criterion -> the control's anchor, source pre-filled
//     from the tested-under note) and the row disappears;
//   * a refusal (403) shows as a toast and the row stays;
//   * candidate anchors are shown for the row;
//   * the CSV export downloads, carrying the framework filter.
//
// HERMETIC except the export: the report and overlay POST BFFs are
// route-mocked (slice 594 convention), so the spec pins the page; the CSV
// download goes through the real BFF and platform. The report's rule
// (which criteria are reached, candidate release, RLS) is pinned by
// internal/api/mappingoverlays/audited_under_integration_test.go, and the
// overlay POST's role guard and validation by that package's tests.

import type { Page, Route } from "@playwright/test";

import type {
  AuditedUnderReport,
  AuditedUnderRow,
} from "../lib/api/audited-under";
import { enumLabel } from "../lib/labels/enum-labels";
import { anchorFitLabel } from "../app/(authed)/controls/filters";

import { expect, test } from "./fixtures";

const REPORT = "**/api/mapping-overlays/audited-under";
const OVERLAY = "**/api/mapping-overlays";

// Neutral test strings only (P0-A9 / GitGuardian).
const GAP: AuditedUnderRow = {
  control_id: "c1802000-0000-4000-8000-000000000001",
  control_bundle_id: "ctrl-1802-nda",
  control_title: "Confidentiality agreements (1802)",
  anchor_id: "a1802000-0000-4000-8000-000000000001",
  anchor_code: "IAC-06",
  anchor_scf_release: "test-2026.3",
  anchor_fit: "partial",
  anchor_fit_note: "IAC-06 covers MFA, not NDAs.",
  framework: "soc2",
  framework_version: "2017",
  requirement_id: "f1802000-0000-4000-8000-000000000001",
  requirement_code: "CC1.4",
  requirement_title: "Commitment to competence",
  tested_under_note: "FY25 report s4",
  candidate_anchors: [
    {
      anchor_id: "a1802000-0000-4000-8000-0000000000c1",
      anchor_code: "HRS-06",
      anchor_title: "Confidentiality agreements",
      relationship_type: "subset_of",
      strength: 0.7,
    },
    {
      anchor_id: "a1802000-0000-4000-8000-0000000000c2",
      anchor_code: "GOV-01",
      anchor_title: "Cybersecurity governance program",
      relationship_type: "intersects_with",
      strength: 0.4,
    },
  ],
};

const OTHER: AuditedUnderRow = {
  ...GAP,
  control_id: "c1802000-0000-4000-8000-000000000002",
  control_bundle_id: "ctrl-1802-backup",
  control_title: "Backups are tested (1802)",
  anchor_code: "BCD-11",
  anchor_fit: "exact",
  anchor_fit_note: undefined,
  framework: "iso27001",
  framework_version: "2022",
  requirement_id: "f1802000-0000-4000-8000-000000000002",
  requirement_code: "A.8.13",
  requirement_title: "Information backup",
  tested_under_note: undefined,
  candidate_anchors: [],
};

function report(
  rows: AuditedUnderRow[],
  extra: Partial<AuditedUnderReport> = {},
): AuditedUnderReport {
  return {
    rows,
    count: rows.length,
    total: rows.length,
    limit: 5000,
    truncated: false,
    unresolved_references: [],
    unresolved_count: 0,
    candidate_scf_release: "test-2026.3",
    ...extra,
  };
}

// mockReport serves the report; once `closed` holds the gap's key, later
// reads leave that row out (the platform's view after the edge lands).
async function mockReport(page: Page, closed: Set<string>) {
  await page.route(REPORT, async (route: Route) => {
    const rows = [GAP, OTHER].filter(
      (r) => !closed.has(`${r.control_id}:${r.requirement_id}`),
    );
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify(report(rows)),
    });
  });
}

test.describe("Audited under report", () => {
  test("lists the fixture gap; Add overlay edge with a rationale closes it", async ({
    authedPage: page,
  }) => {
    const closed = new Set<string>();
    await mockReport(page, closed);
    const posted: unknown[] = [];
    await page.route(OVERLAY, async (route: Route) => {
      if (route.request().method() !== "POST") return route.fallback();
      posted.push(route.request().postDataJSON());
      closed.add(`${GAP.control_id}:${GAP.requirement_id}`);
      await route.fulfill({
        status: 201,
        contentType: "application/json",
        body: JSON.stringify({ overlay_edge: { id: "e1" } }),
      });
    });

    await page.goto("/catalog/audited-under");
    await expect(page).toHaveTitle(/Audited under/);
    const rows = page.getByTestId("audited-under-row");
    await expect(rows).toHaveCount(2);
    const gap = rows.filter({ hasText: "CC1.4" });
    await expect(gap).toContainText("IAC-06@test-2026.3");
    await expect(gap.getByTestId("audited-under-fit")).toHaveText(
      anchorFitLabel(GAP.anchor_fit),
    );
    await expect(gap).toContainText("IAC-06 covers MFA, not NDAs.");

    await gap.getByTestId("audited-under-add-edge").click();
    const form = page.getByTestId("audited-under-edge-form");
    await expect(page.getByTestId("audited-under-edge-source")).toHaveValue(
      "FY25 report s4",
    );
    const submit = page.getByTestId("audited-under-edge-submit");
    // The rationale is required.
    await expect(submit).toBeDisabled();
    await page
      .getByTestId("audited-under-edge-rationale")
      .fill("The FY25 auditor tested CC1.4 against this control.");
    await submit.click();

    await expect(form).toHaveCount(0);
    await expect(rows).toHaveCount(1);
    await expect(rows.filter({ hasText: "CC1.4" })).toHaveCount(0);
    await expect(page.getByTestId("toast")).toContainText(
      "Overlay edge added: soc2 CC1.4 to IAC-06",
    );
    expect(posted).toEqual([
      {
        framework: "soc2",
        framework_version: "2017",
        requirement_code: "CC1.4",
        scf_release: "test-2026.3",
        anchor_code: "IAC-06",
        relationship_type: "intersects_with",
        strength: 0.5,
        rationale: "The FY25 auditor tested CC1.4 against this control.",
        source_ref: "FY25 report s4",
      },
    ]);
  });

  test("a refusal shows as a toast and the row stays", async ({
    authedPage: page,
  }) => {
    await mockReport(page, new Set());
    await page.route(OVERLAY, async (route: Route) => {
      if (route.request().method() !== "POST") return route.fallback();
      await route.fulfill({
        status: 403,
        contentType: "application/json",
        body: JSON.stringify({ error: "forbidden" }),
      });
    });

    await page.goto("/catalog/audited-under");
    const gap = page
      .getByTestId("audited-under-row")
      .filter({ hasText: "CC1.4" });
    await gap.getByTestId("audited-under-add-edge").click();
    await page.getByTestId("audited-under-edge-rationale").fill("Relied on.");
    await page.getByTestId("audited-under-edge-submit").click();

    await expect(page.getByTestId("toast")).toContainText(
      "Overlay edge not added: forbidden",
    );
    await expect(page.getByTestId("audited-under-edge-error")).toHaveText(
      "forbidden",
    );
    await expect(gap).toHaveCount(1);
  });

  test("candidate anchors are shown for the row", async ({
    authedPage: page,
  }) => {
    await mockReport(page, new Set());
    await page.goto("/catalog/audited-under");
    const gap = page
      .getByTestId("audited-under-row")
      .filter({ hasText: "CC1.4" });
    const candidates = gap.getByTestId("audited-under-candidate");
    await expect(candidates).toHaveCount(2);
    await expect(candidates.nth(0)).toContainText("HRS-06");
    // The label comes from the shared module (#2330), so a label change
    // moves the page and this assertion together.
    const top = GAP.candidate_anchors[0];
    await expect(candidates.nth(0)).toContainText(
      `${top.anchor_title} (${enumLabel(
        "StrmRelationshipType",
        top.relationship_type,
      )}, ${top.strength})`,
    );
    await expect(
      candidates.nth(0).getByRole("link", { name: "HRS-06" }),
    ).toHaveAttribute(
      "href",
      `/catalog/scf/${GAP.candidate_anchors[0].anchor_id}`,
    );
    await expect(candidates.nth(1)).toContainText("GOV-01");
    await expect(page.getByTestId("audited-under-count")).toContainText(
      "candidates from SCF test-2026.3",
    );
  });

  test("unresolved references show as a data-quality note; a cut report says so", async ({
    authedPage: page,
  }) => {
    await page.route(REPORT, async (route: Route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(
          report([GAP], {
            total: 3,
            limit: 1,
            truncated: true,
            unresolved_count: 2,
            unresolved_references: [
              {
                control_id: GAP.control_id,
                control_bundle_id: GAP.control_bundle_id,
                control_title: GAP.control_title,
                system: "soc2",
                code: "CC9.9",
                tested_under_note: "FY25 import",
              },
            ],
          }),
        ),
      });
    });

    await page.goto("/catalog/audited-under");
    await expect(page.getByTestId("audited-under-row")).toHaveCount(1);
    await expect(page.getByTestId("audited-under-truncated")).toContainText(
      "Showing the first 1 of 3 gaps",
    );
    const note = page.getByTestId("audited-under-unresolved");
    await expect(note).toContainText(
      "2 tested-under references do not resolve",
    );
    await note.getByText("Show the first 1").click();
    const ref = note.getByTestId("audited-under-unresolved-ref");
    await expect(ref).toContainText("soc2 CC9.9");
    await expect(ref).toContainText("(FY25 import)");
    await expect(
      ref.getByRole("link", { name: GAP.control_title }),
    ).toHaveAttribute("href", `/controls/${GAP.control_id}`);
  });

  test("a clean report shows no note and no truncation", async ({
    authedPage: page,
  }) => {
    await mockReport(page, new Set());
    await page.goto("/catalog/audited-under");
    await expect(page.getByTestId("audited-under-row")).toHaveCount(2);
    await expect(page.getByTestId("audited-under-unresolved")).toHaveCount(0);
    await expect(page.getByTestId("audited-under-truncated")).toHaveCount(0);
  });

  test("a cut unresolved list alone shows no gap truncation banner", async ({
    authedPage: page,
  }) => {
    await page.route(REPORT, async (route: Route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(
          report([GAP, OTHER], {
            truncated: true,
            unresolved_count: 9,
            unresolved_references: [
              {
                control_id: GAP.control_id,
                control_bundle_id: GAP.control_bundle_id,
                control_title: GAP.control_title,
                system: "soc2",
                code: "CC9.9",
              },
            ],
          }),
        ),
      });
    });
    await page.goto("/catalog/audited-under");
    await expect(page.getByTestId("audited-under-row")).toHaveCount(2);
    await expect(page.getByTestId("audited-under-unresolved")).toContainText(
      "9 tested-under references do not resolve",
    );
    await expect(page.getByTestId("audited-under-truncated")).toHaveCount(0);
  });

  test("filters by framework, and the CSV export downloads", async ({
    authedPage: page,
  }) => {
    await mockReport(page, new Set());

    await page.goto("/catalog/audited-under");
    await expect(page.getByTestId("audited-under-row")).toHaveCount(2);
    await page.getByTestId("audited-under-framework").selectOption("soc2");
    await expect(page.getByTestId("audited-under-row")).toHaveCount(1);
    await expect(page.getByTestId("audited-under-row")).toContainText("CC1.4");

    const link = page.getByTestId("audited-under-export-csv");
    await expect(link).toHaveAttribute(
      "href",
      "/api/mapping-overlays/audited-under/export?framework=soc2",
    );

    // The export is not mocked: page.route does not intercept the download
    // an <a download> starts, so the platform answers through the BFF. The
    // seeded tenant may have no gaps, so the body is checked for the header.
    const download = page.waitForEvent("download");
    await link.click();
    const csv = await download;
    expect(csv.suggestedFilename()).toMatch(/^audited-under_\d{8}\.csv$/);
    const chunks: Buffer[] = [];
    for await (const chunk of await csv.createReadStream()) {
      chunks.push(chunk as Buffer);
    }
    expect(Buffer.concat(chunks).toString("utf8")).toMatch(
      /^﻿?framework,framework_version,requirement_code,/,
    );
  });
});
