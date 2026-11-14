import { describe, expect, it } from "vitest";

import type { ExternalRefWire } from "@/lib/api/control-detail";

import { formatExternalRef, groupExternalRefs } from "./external-refs";

describe("groupExternalRefs (#1737)", () => {
  it("returns no groups for an absent or empty list", () => {
    expect(groupExternalRefs(undefined)).toEqual([]);
    expect(groupExternalRefs([])).toEqual([]);
  });

  it("puts tested_under first and keeps manifest order within a group", () => {
    const refs: ExternalRefWire[] = [
      { system: "acme-audit", kind: "reference", id: "RA-03" },
      { system: "soc2", kind: "tested_under", id: "CC3.1", note: "FY25" },
      { system: "acme-audit", kind: "reference", id: "RA-07" },
    ];
    const groups = groupExternalRefs(refs);
    expect(groups.map((g) => g.label)).toEqual(["Tested under", "References"]);
    expect(groups[0].refs.map((r) => r.id)).toEqual(["CC3.1"]);
    expect(groups[1].refs.map((r) => r.id)).toEqual(["RA-03", "RA-07"]);
  });

  it("drops a group with no entries", () => {
    const groups = groupExternalRefs([
      { system: "acme-audit", kind: "reference", id: "RA-03" },
    ]);
    expect(groups.map((g) => g.kind)).toEqual(["reference"]);
  });
});

describe("formatExternalRef", () => {
  it("joins system and id", () => {
    expect(
      formatExternalRef({ system: "soc2", kind: "tested_under", id: "CC3.1" }),
    ).toBe("soc2 CC3.1");
  });
});
