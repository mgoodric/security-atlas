// #1802 — the audited-under page's pure helpers.

import { describe, expect, test } from "vitest";

import {
  auditedUnderExportURL,
  defaultSourceRef,
  edgeFor,
  frameworksOf,
  groupRows,
  type AuditedUnderRow,
} from "./audited-under";

function row(over: Partial<AuditedUnderRow>): AuditedUnderRow {
  return {
    control_id: "c1",
    control_bundle_id: "bundle-1",
    control_title: "Control one",
    anchor_id: "a1",
    anchor_code: "IAC-06",
    anchor_scf_release: "2026.3",
    anchor_fit: "exact",
    framework: "soc2",
    framework_version: "2017",
    requirement_id: "r1",
    requirement_code: "CC6.1",
    requirement_title: "Logical access",
    tested_under_note: "FY25 report s4",
    candidate_anchors: [],
    ...over,
  };
}

const INPUT = {
  relationship_type: "intersects_with",
  strength: 0.5,
  rationale: "  Audit relied on it  ",
  source_ref: " FY25 report s4 ",
};

describe("groupRows", () => {
  const rows = [
    row({}),
    row({
      requirement_id: "r2",
      framework: "iso27001",
      framework_version: "2022",
    }),
    row({
      control_id: "c2",
      control_title: "Control two",
      control_bundle_id: "bundle-2",
    }),
  ];

  test("by control keeps report order", () => {
    const g = groupRows(rows, "control");
    expect(g.map((x) => [x.label, x.rows.length])).toEqual([
      ["Control one", 2],
      ["Control two", 1],
    ]);
  });

  test("by framework splits on framework and version", () => {
    const g = groupRows(rows, "framework");
    expect(g.map((x) => [x.label, x.detail, x.rows.length])).toEqual([
      ["soc2", "2017", 2],
      ["iso27001", "2022", 1],
    ]);
  });

  test("frameworksOf lists distinct slugs", () => {
    expect(frameworksOf(rows)).toEqual(["soc2", "iso27001"]);
  });
});

describe("edgeFor", () => {
  test("maps the criterion to the control's anchor, trimmed", () => {
    expect(edgeFor(row({}), INPUT)).toEqual({
      framework: "soc2",
      framework_version: "2017",
      requirement_code: "CC6.1",
      scf_release: "2026.3",
      anchor_code: "IAC-06",
      relationship_type: "intersects_with",
      strength: 0.5,
      rationale: "Audit relied on it",
      source_ref: "FY25 report s4",
    });
  });

  test("an unanchored control has no edge", () => {
    expect(
      edgeFor(row({ anchor_id: null, anchor_code: undefined }), INPUT),
    ).toBeNull();
  });
});

describe("defaultSourceRef", () => {
  test("is the tested-under note", () => {
    expect(defaultSourceRef(row({}))).toBe("FY25 report s4");
  });

  test("falls back to the criterion", () => {
    expect(defaultSourceRef(row({ tested_under_note: " " }))).toBe(
      "Tested under soc2 CC6.1",
    );
  });
});

describe("auditedUnderExportURL", () => {
  test("carries the framework filter", () => {
    expect(auditedUnderExportURL("")).toBe(
      "/api/mapping-overlays/audited-under/export",
    );
    expect(auditedUnderExportURL("a&b")).toBe(
      "/api/mapping-overlays/audited-under/export?framework=a%26b",
    );
  });
});
