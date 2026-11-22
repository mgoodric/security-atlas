// #2847 — the risk import review page's states, through its pure helpers.

import { describe, expect, it } from "vitest";

import {
  type BatchRow,
  type RiskImportBatch,
  NO_FILTER,
  approveAllMessage,
  approveAllWarning,
  batchPath,
  batchProgress,
  emptyRowsMessage,
  filterRows,
  flagLabel,
  mappedFields,
  rejectReasonLabel,
  reviewMode,
  rowHasUnmappedValue,
  rowPath,
  unmappedEntries,
  unmappedPendingCount,
  unmappedValues,
} from "./risk-imports";

function row(over: Partial<BatchRow>): BatchRow {
  return {
    id: "r",
    row_number: 3,
    title: "Vendor outage",
    description: "",
    category: "operational",
    unmapped: {},
    flags: [],
    status: "pending",
    ...over,
  };
}

function batch(staged: BatchRow[], rejected = 0): RiskImportBatch {
  return {
    id: "b",
    source_filename: "register.csv",
    source_format: "csv",
    uploaded_by: "user:u",
    status: "staged",
    total_rows: staged.length + rejected,
    staged_count: staged.length,
    rejected_count: rejected,
    unmapped_columns: [],
    created_at: "2026-11-21T00:00:00Z",
    staged,
    rejected: [],
  };
}

const rows = [
  row({ id: "a", row_number: 3, flags: ["owner_unlinked"] }),
  row({ id: "b", row_number: 4 }),
  row({ id: "c", row_number: 5, status: "approved", risk_id: "x" }),
  row({
    id: "d",
    row_number: 6,
    status: "rejected",
    flags: ["treatment_downgraded", "parent_link_failed"],
  }),
  row({ id: "e", row_number: 7, status: "duplicate", flags: null }),
];

describe("batchProgress", () => {
  it("tallies staged rows by decision and flags", () => {
    expect(batchProgress(batch(rows, 2))).toEqual({
      staged: 5,
      approved: 1,
      rejected: 1,
      duplicate: 1,
      pending: 2,
      flagged: 2,
      notStaged: 2,
    });
  });

  it("treats a null staged list as empty", () => {
    const b = { ...batch([]), staged: null };
    expect(batchProgress(b).pending).toBe(0);
  });
});

describe("filterRows", () => {
  it("keeps every row with no filter", () => {
    expect(filterRows(rows, NO_FILTER).map((r) => r.id)).toEqual([
      "a",
      "b",
      "c",
      "d",
      "e",
    ]);
  });

  it("filters to flagged rows", () => {
    expect(
      filterRows(rows, { status: "", flaggedOnly: true }).map((r) => r.id),
    ).toEqual(["a", "d"]);
  });

  it("combines status and flagged", () => {
    expect(
      filterRows(rows, { status: "pending", flaggedOnly: true }).map(
        (r) => r.id,
      ),
    ).toEqual(["a"]);
    expect(
      filterRows(rows, { status: "pending", flaggedOnly: false }).map(
        (r) => r.id,
      ),
    ).toEqual(["a", "b"]);
  });

  it("handles a null list", () => {
    expect(filterRows(null, NO_FILTER)).toEqual([]);
  });
});

describe("reviewMode", () => {
  it("lets an approver decide", () => {
    expect(reviewMode({ is_approver: true })).toBe("decide");
  });

  it("is read-only for a non-approver, and while /v1/me is unknown", () => {
    expect(reviewMode({ is_approver: false })).toBe("read-only");
    expect(reviewMode(undefined)).toBe("read-only");
  });
});

describe("emptyRowsMessage", () => {
  it("says when nothing was staged", () => {
    expect(emptyRowsMessage(batch([], 3), NO_FILTER)).toBe(
      "No rows were staged from this file.",
    );
  });

  it("says when no row is flagged", () => {
    expect(
      emptyRowsMessage(batch([row({})]), { status: "", flaggedOnly: true }),
    ).toBe("No staged row is flagged for review.");
  });

  it("says when the filters match nothing", () => {
    expect(
      emptyRowsMessage(batch([row({})]), {
        status: "approved",
        flaggedOnly: false,
      }),
    ).toBe("No staged rows match these filters.");
  });
});

describe("row fields", () => {
  it("lists mapped fields, leaving unset optional ones out", () => {
    expect(mappedFields(row({}))).toEqual([
      ["Title", "Vendor outage"],
      ["Category", "operational"],
    ]);
    expect(
      mappedFields(
        row({
          external_ref: "R-001",
          review_due: "2027-01-15",
          description: "Down a day.",
        }),
      ),
    ).toEqual([
      ["Risk ID", "R-001"],
      ["Title", "Vendor outage"],
      ["Category", "operational"],
      ["Review due", "2027-01-15"],
      ["Description", "Down a day."],
    ]);
  });

  it("shows an unmapped category value, and theme values beside their mapping (#2844)", () => {
    expect(
      mappedFields(
        row({
          category_value: "Ops",
          category: "",
          theme_values: ["Vendors", "Availability"],
          themes: ["availability"],
        }),
      ),
    ).toEqual([
      ["Title", "Vendor outage"],
      ["Category", "Ops (unmapped)"],
      ["Theme values", "Vendors, Availability"],
      ["Themes", "availability"],
    ]);
    expect(
      mappedFields(
        row({
          category_value: "Ops",
          category: "operational",
          theme_values: ["People"],
          themes: [],
        }),
      ),
    ).toEqual([
      ["Title", "Vendor outage"],
      ["Category", "operational"],
      ["Theme values", "People"],
      ["Themes", "(none mapped)"],
    ]);
  });

  it("shows an accepted row as proposed, with its accepter as history (#2842)", () => {
    expect(
      mappedFields(
        row({
          treatment: "accept",
          justification: "Fix costs more than the exposure",
          proposed_until: "2027-06-30",
          historical_accepter: "Jo",
        }),
      ),
    ).toEqual([
      ["Title", "Vendor outage"],
      ["Category", "operational"],
      ["Treatment", "accept (proposed)"],
      ["Justification", "Fix costs more than the exposure"],
      ["Proposed until", "2027-06-30"],
      ["Accepter (historical)", "Jo"],
    ]);
  });

  it("shows a downgraded treatment with the register's, and control links", () => {
    expect(
      mappedFields(
        row({
          treatment: "untreated",
          requested_treatment: "mitigate",
          linked_control_ids: [],
          unresolved_controls: ["NO-SUCH"],
        }),
      ),
    ).toEqual([
      ["Title", "Vendor outage"],
      ["Category", "operational"],
      ["Treatment", "untreated (register: mitigate)"],
      ["Unresolved controls", "NO-SUCH"],
    ]);
  });

  it("shows a matched owner by email and an unmatched one as unlinked (#2843)", () => {
    expect(mappedFields(row({ owner_user_id: "u-1" }))).toEqual([
      ["Title", "Vendor outage"],
      ["Category", "operational"],
      ["Owner", "u-1"],
    ]);
    expect(mappedFields(row({ owner_unlinked: "Platform team" }))).toEqual([
      ["Title", "Vendor outage"],
      ["Category", "operational"],
      ["Owner (unlinked)", "Platform team"],
    ]);
  });

  it("shows the level and the org unit as written (#2845)", () => {
    expect(
      mappedFields(
        row({ level: "org", org_unit_value: "Platform", org_unit_id: "ou-1" }),
      ),
    ).toEqual([
      ["Title", "Vendor outage"],
      ["Category", "operational"],
      ["Level", "org"],
      ["Org unit", "Platform"],
    ]);
  });

  it("lists unmapped values by header, blanks left out", () => {
    expect(
      unmappedEntries(
        row({
          unmapped: {
            "Target Date": "2027-03-01",
            "Risk Owner": "a@x",
            Notes: " ",
          },
        }),
      ),
    ).toEqual([
      ["Risk Owner", "a@x"],
      ["Target Date", "2027-03-01"],
    ]);
    expect(unmappedEntries(row({ unmapped: null }))).toEqual([]);
  });
});

describe("labels", () => {
  it("names the three flags and passes an unknown one through", () => {
    expect(flagLabel("owner_unlinked")).toBe("Owner not linked to a user");
    expect(flagLabel("treatment_downgraded")).toBe(
      "Treatment downgraded to untreated",
    );
    expect(flagLabel("parent_link_failed")).toBe("Parent link failed");
    expect(flagLabel("something_new")).toBe("something_new");
  });

  it("names upload rejection reasons", () => {
    expect(rejectReasonLabel("unknown_category")).toBe("Unknown category");
    expect(rejectReasonLabel("other")).toBe("other");
  });
});

describe("approveAllMessage", () => {
  it("summarises approvals, duplicates and skips", () => {
    expect(
      approveAllMessage({
        batch_id: "b",
        approved_count: 1,
        approved: [],
        duplicates: [],
        skipped: [],
      }),
    ).toBe("Approved 1 row");
    expect(
      approveAllMessage({
        batch_id: "b",
        approved_count: 4,
        approved: [],
        duplicates: [
          {
            batch_id: "b",
            row_id: "x",
            row_number: 3,
            status: "duplicate",
            decided_by: "u",
          },
        ],
        skipped: [{ row_id: "y", row_number: 4, reason: "archived" }],
      }),
    ).toBe("Approved 4 rows; 1 marked duplicate; 1 skipped");
  });
});

describe("unmapped values (#2844)", () => {
  const mapped = row({
    id: "m",
    category_value: "Ops",
    category: "operational",
    theme_values: ["Vendors"],
    themes: ["vendor-risk"],
  });
  const noCategory = row({ id: "c", category_value: "nonsense", category: "" });
  const noTheme = row({
    id: "t",
    category_value: "Ops",
    category: "operational",
    theme_values: ["Vendors", "People"],
    themes: ["vendor-risk"],
  });
  const decided = row({
    id: "d",
    status: "rejected",
    category_value: "nonsense",
    category: "",
  });
  const b = {
    ...batch([mapped, noCategory, noTheme, decided]),
    category_values: [
      {
        kind: "category" as const,
        value: "Ops",
        row_count: 2,
        target: "operational",
      },
      { kind: "category" as const, value: "nonsense", row_count: 2 },
    ],
    theme_values: [
      {
        kind: "theme" as const,
        value: "Vendors",
        row_count: 2,
        target: "vendor-risk",
      },
      { kind: "theme" as const, value: "People", row_count: 1 },
    ],
  };

  it("finds the rows and values that map to nothing", () => {
    expect(rowHasUnmappedValue(mapped)).toBe(false);
    expect(rowHasUnmappedValue(noCategory)).toBe(true);
    expect(rowHasUnmappedValue(noTheme)).toBe(true);
    // A row from before value mapping carries no category_value.
    expect(rowHasUnmappedValue(row({}))).toBe(false);
    expect(unmappedValues(b).map((v) => v.value)).toEqual([
      "nonsense",
      "People",
    ]);
    expect(unmappedPendingCount(b)).toBe(2);
  });

  it("names the count and values approve-all would reject", () => {
    expect(approveAllWarning(b)).toBe(
      '2 pending rows carry an unmapped category or theme value and will be rejected, not created (category "nonsense", theme "People"). A rejection is final: map the values first to import them.',
    );
    expect(approveAllWarning(batch([mapped]))).toBe("");
  });

  it("reports rows approval rejected", () => {
    expect(
      approveAllMessage({
        batch_id: "b",
        approved_count: 1,
        approved: [],
        duplicates: [],
        rejected: [
          {
            batch_id: "b",
            row_id: "c",
            row_number: 4,
            status: "rejected",
            decided_by: "u",
          },
        ],
        skipped: [],
      }),
    ).toBe("Approved 1 row; 1 rejected (unmapped value)");
  });
});

describe("paths", () => {
  it("builds BFF paths with encoded ids", () => {
    expect(batchPath("a/b")).toBe("/api/risks/imports/a%2Fb");
    expect(rowPath("b", "r", "approve")).toBe(
      "/api/risks/imports/b/rows/r/approve",
    );
  });
});
