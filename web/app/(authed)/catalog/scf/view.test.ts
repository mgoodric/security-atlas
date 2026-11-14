import { describe, expect, it } from "vitest";

import type { AnchorWithState } from "@/lib/api/anchors";

import { anchorHasControl, catalogCountLabel, searchAnchors } from "./view";

function anchor(
  scf_id: string,
  name: string,
  family: string,
  extra: Partial<AnchorWithState> = {},
): AnchorWithState {
  return {
    id: `id-${scf_id}`,
    scf_id,
    name,
    family,
    description: "",
    state: null,
    frameworks: [],
    ...extra,
  };
}

const rows = [
  anchor("IAC-06", "Multi-Factor Authentication", "IAC"),
  anchor("CRY-05", "Encrypting Data At Rest", "CRY"),
  anchor("GOV-01", "Cybersecurity Governance Program", "GOV"),
];

describe("searchAnchors (issue 2066)", () => {
  it("keeps every anchor for a blank query", () => {
    expect(searchAnchors(rows, "  ")).toHaveLength(3);
  });

  it("finds an anchor by code, ignoring case", () => {
    expect(searchAnchors(rows, "iac-06").map((a) => a.scf_id)).toEqual([
      "IAC-06",
    ]);
  });

  it("finds an anchor by a title word", () => {
    expect(searchAnchors(rows, "encrypting").map((a) => a.scf_id)).toEqual([
      "CRY-05",
    ]);
  });

  it("finds anchors by family", () => {
    expect(searchAnchors(rows, "gov").map((a) => a.scf_id)).toEqual(["GOV-01"]);
  });

  it("requires every word to match", () => {
    expect(searchAnchors(rows, "data rest").map((a) => a.scf_id)).toEqual([
      "CRY-05",
    ]);
    expect(searchAnchors(rows, "data governance")).toEqual([]);
  });
});

describe("anchorHasControl (issue 2066)", () => {
  it("reads control_count and control_id", () => {
    expect(anchorHasControl(anchor("A-1", "a", "A"))).toBe(false);
    expect(
      anchorHasControl(anchor("A-1", "a", "A", { control_count: 0 })),
    ).toBe(false);
    expect(
      anchorHasControl(anchor("A-1", "a", "A", { control_count: 2 })),
    ).toBe(true);
    expect(anchorHasControl(anchor("A-1", "a", "A", { control_id: "c" }))).toBe(
      true,
    );
  });
});

describe("catalogCountLabel (issue 2066)", () => {
  it("prefers the server total over the rows loaded", () => {
    expect(catalogCountLabel(100, 1591)).toBe("1,591 anchors");
  });

  it("falls back to the rows loaded", () => {
    expect(catalogCountLabel(42)).toBe("42 anchors");
    expect(catalogCountLabel(1)).toBe("1 anchor");
  });
});
