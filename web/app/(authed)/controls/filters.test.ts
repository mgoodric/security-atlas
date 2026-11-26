// Slice 098 — vitest unit coverage for /controls filter logic.
//
// Pure-data tests. No React, no DOM, no fetch. Just the filter-set
// helpers from `./filters`; #2131 moved the row narrowing to the server.
//
// All test fixtures use neutral identifiers — NO vendor token prefixes,
// NO scf_id strings that look like real auditor IDs (per slice 098
// anti-criterion P0-A5 and slice 069 hardening).

import { describe, expect, test } from "vitest";

import {
  ALL,
  anchorFitLabel,
  anchorFitMarker,
  clearFilters,
  DEFAULT_FILTERS,
  formatFrameworksCell,
  FRAMEWORKS_EMPTY_PLACEHOLDER,
  FRAMEWORKS_JOIN_SEPARATOR,
  isDefault,
  setFilter,
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
    key: `00000000-0000-0000-0000-0000000000${scfSuffix}`,
    controlId: null,
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
    // Issue #1736 — anchor fit.
    expect(DEFAULT_FILTERS.anchor_fit).toBe(ALL);
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

describe("formatFrameworksCell", () => {
  test("AC-5: single framework renders verbatim (no separator)", () => {
    expect(formatFrameworksCell(["SOC2"])).toBe("SOC2");
  });

  test("AC-5 + AC-7: multi-framework joins with middle-dot separator", () => {
    // Matches `Plans/_archive/mockups/controls.html` line 217 example
    // (`SOC2 · ISO · CSF` on IAC-37).
    expect(formatFrameworksCell(["SOC2", "ISO", "CSF"])).toBe(
      "SOC2 · ISO · CSF",
    );
  });

  test("AC-5: four-framework row renders the full strip", () => {
    // Mockup CRY-07 example (`SOC2 · ISO · CSF · GDPR`).
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

// Issue #1736 — anchor fit filter, marker and label.
describe("anchor fit", () => {
  const withFit = (
    suffix: string,
    anchorFit: AnchorRow["anchorFit"],
    anchorFitNote: string | null = null,
  ): AnchorRow => ({
    ...row("IAC", null, suffix),
    anchorFit,
    anchorFitNote,
  });
  const exact = withFit("01", "exact");
  const partial = withFit("02", "partial", "covers cadence, not independence");
  const none = withFit("03", "none", "no SCF control covers retention");
  const catalog = row("IAC", null, "04"); // catalog anchor: no fit

  test("a fit filter is not the default filter set", () => {
    expect(isDefault(setFilter(DEFAULT_FILTERS, "anchor_fit", "none"))).toBe(
      false,
    );
  });

  test("only non-exact rows carry a marker, titled with the note", () => {
    expect(anchorFitMarker(exact)).toBeNull();
    expect(anchorFitMarker(catalog)).toBeNull();
    expect(anchorFitMarker(partial)).toEqual({
      label: "Partial fit",
      title: "covers cadence, not independence",
    });
    expect(anchorFitMarker(none)).toEqual({
      label: "No SCF fit",
      title: "no SCF control covers retention",
    });
    // A row whose note did not arrive still gets the marker, untitled.
    expect(anchorFitMarker({ anchorFit: "partial" })).toEqual({
      label: "Partial fit",
      title: "",
    });
  });

  test("labels each fit; absent reads as exact", () => {
    expect(anchorFitLabel(undefined)).toBe("Exact");
    expect(anchorFitLabel("exact")).toBe("Exact");
    expect(anchorFitLabel("partial")).toBe("Partial");
    expect(anchorFitLabel("none")).toBe("No SCF fit");
  });
});
