// Slice 098 — vitest unit coverage for /controls filter logic.
//
// Pure-data tests. No React, no DOM, no fetch. Just the row-narrowing
// + filter-set helpers from `./filters`.
//
// All test fixtures use neutral identifiers — NO vendor token prefixes,
// NO scf_id strings that look like real auditor IDs (per slice 098
// anti-criterion P0-A5 and slice 069 hardening).

import { describe, expect, test } from "vitest";

import {
  ALL,
  applyFilters,
  clearFilters,
  DEFAULT_FILTERS,
  formatFrameworksCell,
  FRAMEWORKS_EMPTY_PLACEHOLDER,
  FRAMEWORKS_JOIN_SEPARATOR,
  isDefault,
  setFilter,
  uniqueFamilies,
  type AnchorRow,
} from "./filters";

// Slice 226 — `frameworks` is required on AnchorRow. Default to the
// empty array so the existing filter assertions remain agnostic to
// the new column (the filter logic ignores the field; the column is
// renderer-only).
function row(
  family: string,
  state: AnchorRow["state"],
  scfSuffix: string,
  frameworks: string[] = [],
): AnchorRow {
  return {
    anchor: {
      id: `00000000-0000-0000-0000-0000000000${scfSuffix}`,
      scf_id: `test-${scfSuffix}`,
      family,
      name: `test anchor ${scfSuffix}`,
      description: "",
    },
    state,
    frameworks,
  };
}

describe("DEFAULT_FILTERS / isDefault / clearFilters", () => {
  test("DEFAULT_FILTERS is all-ALL", () => {
    expect(DEFAULT_FILTERS.framework).toBe(ALL);
    expect(DEFAULT_FILTERS.family).toBe(ALL);
    expect(DEFAULT_FILTERS.result).toBe(ALL);
    expect(DEFAULT_FILTERS.freshness).toBe(ALL);
    // Slice 224 — scope is the 5th filter; defaults to ALL like the others.
    expect(DEFAULT_FILTERS.scope).toBe(ALL);
  });

  test("isDefault is true for the default set", () => {
    expect(isDefault(DEFAULT_FILTERS)).toBe(true);
  });

  test("isDefault is false when any filter is narrowed", () => {
    expect(isDefault({ ...DEFAULT_FILTERS, family: "AAA" })).toBe(false);
    expect(isDefault({ ...DEFAULT_FILTERS, result: "pass" })).toBe(false);
    // Slice 224 — narrowing scope flips isDefault to false the same
    // way the other four pills do.
    expect(
      isDefault({
        ...DEFAULT_FILTERS,
        scope: "00000000-0000-0000-0000-000000000001",
      }),
    ).toBe(false);
  });

  test("clearFilters returns a fresh ALL set (no shared reference)", () => {
    const cleared = clearFilters();
    expect(cleared).toEqual(DEFAULT_FILTERS);
    expect(cleared).not.toBe(DEFAULT_FILTERS);
  });
});

describe("setFilter", () => {
  test("merges one key without mutating the input", () => {
    const input = clearFilters();
    const next = setFilter(input, "family", "AAA");
    expect(next.family).toBe("AAA");
    expect(input.family).toBe(ALL); // unchanged
  });

  test("multiple sets compose left-to-right", () => {
    const next = setFilter(
      setFilter(clearFilters(), "family", "AAA"),
      "result",
      "pass",
    );
    expect(next.family).toBe("AAA");
    expect(next.result).toBe("pass");
  });
});

describe("uniqueFamilies", () => {
  test("returns the unique family set sorted alphabetically", () => {
    const rows = [
      row("CRY", null, "01"),
      row("AAA", null, "02"),
      row("CRY", null, "03"),
      row("BCD", null, "04"),
    ];
    expect(uniqueFamilies(rows)).toEqual(["AAA", "BCD", "CRY"]);
  });

  test("skips empty family strings", () => {
    const rows = [row("", null, "01"), row("AAA", null, "02")];
    expect(uniqueFamilies(rows)).toEqual(["AAA"]);
  });

  test("returns empty array for empty input", () => {
    expect(uniqueFamilies([])).toEqual([]);
  });
});

describe("applyFilters", () => {
  const allRows: AnchorRow[] = [
    row(
      "AAA",
      { result: "pass", freshness_status: "fresh", last_observed_at: null },
      "01",
    ),
    row(
      "AAA",
      { result: "fail", freshness_status: "stale", last_observed_at: null },
      "02",
    ),
    row(
      "CRY",
      { result: "pass", freshness_status: "fresh", last_observed_at: null },
      "03",
    ),
    row("CRY", null, "04"), // no state attached
    row("BCD", null, "05"),
  ];

  test("DEFAULT_FILTERS returns every row", () => {
    expect(applyFilters(allRows, DEFAULT_FILTERS)).toHaveLength(5);
  });

  test("family filter narrows by SCF family (case-insensitive)", () => {
    const out = applyFilters(allRows, { ...DEFAULT_FILTERS, family: "aaa" });
    expect(out).toHaveLength(2);
    expect(out.every((r) => r.anchor.family === "AAA")).toBe(true);
  });

  test("result filter excludes rows with no state attached", () => {
    const out = applyFilters(allRows, { ...DEFAULT_FILTERS, result: "pass" });
    expect(out).toHaveLength(2);
    expect(out.every((r) => r.state?.result === "pass")).toBe(true);
  });

  test("freshness filter excludes rows with no state attached", () => {
    const out = applyFilters(allRows, {
      ...DEFAULT_FILTERS,
      freshness: "stale",
    });
    expect(out).toHaveLength(1);
    expect(out[0]?.state?.freshness_status).toBe("stale");
  });

  test("framework filter is a no-op for v1 (anchorWire has no framework set)", () => {
    // Even with framework narrowed, every row stays — until spillover 104
    // adds per-anchor framework data. The pill still renders so the UI
    // shape stays stable across slices.
    const out = applyFilters(allRows, {
      ...DEFAULT_FILTERS,
      framework: "soc2",
    });
    expect(out).toHaveLength(5);
  });

  test("combining filters narrows on every active predicate", () => {
    const out = applyFilters(allRows, {
      ...DEFAULT_FILTERS,
      family: "AAA",
      result: "pass",
    });
    expect(out).toHaveLength(1);
    expect(out[0]?.anchor.family).toBe("AAA");
    expect(out[0]?.state?.result).toBe("pass");
  });

  test("returns empty when no row matches", () => {
    const out = applyFilters(allRows, {
      ...DEFAULT_FILTERS,
      family: "DOES-NOT-EXIST",
    });
    expect(out).toEqual([]);
  });
});

// Slice 226 — AC-5 / AC-6 / AC-7 column-render-helper coverage.
describe("formatFrameworksCell", () => {
  test("AC-5: single framework renders verbatim (no separator)", () => {
    expect(formatFrameworksCell(["SOC2"])).toBe("SOC2");
  });

  test("AC-5 + AC-7: multi-framework joins with middle-dot separator", () => {
    // Matches `Plans/mockups/controls.html` line 217 example
    // (`SOC2 · ISO · CSF` on IAC-06).
    expect(formatFrameworksCell(["SOC2", "ISO", "CSF"])).toBe(
      "SOC2 · ISO · CSF",
    );
  });

  test("AC-5: four-framework row renders the full strip", () => {
    // Mockup CRY-04 example (`SOC2 · ISO · CSF · GDPR`).
    expect(formatFrameworksCell(["SOC2", "ISO", "CSF", "GDPR"])).toBe(
      "SOC2 · ISO · CSF · GDPR",
    );
  });

  test("AC-6: empty array renders the em-dash placeholder", () => {
    expect(formatFrameworksCell([])).toBe(FRAMEWORKS_EMPTY_PLACEHOLDER);
    expect(FRAMEWORKS_EMPTY_PLACEHOLDER).toBe("—");
  });

  test("does NOT sort the input — caller controls order", () => {
    // The backend ships sorted display abbreviations; the renderer
    // trusts that and ships them verbatim. If a caller passes
    // unsorted input, the cell renders unsorted — the contract is
    // "render what you got".
    expect(formatFrameworksCell(["GDPR", "SOC2", "CSF"])).toBe(
      "GDPR · SOC2 · CSF",
    );
  });

  test("does NOT map or transform abbreviations (P0-226-2)", () => {
    // Slug-shaped input passes through unchanged — proves the helper
    // is renderer-only, no map.
    expect(formatFrameworksCell(["soc2", "iso27001"])).toBe("soc2 · iso27001");
  });

  test("separator constant is the canonical middle-dot pattern", () => {
    // Pin the separator so a future refactor doesn't quietly flip
    // to ", " or " | " and break visual parity with the mockup.
    expect(FRAMEWORKS_JOIN_SEPARATOR).toBe(" · ");
  });
});
