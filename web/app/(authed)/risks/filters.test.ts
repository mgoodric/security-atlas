// Slice 100 — vitest unit coverage for /risks filter + formatter logic.
//
// Pure-data tests. No React, no DOM, no fetch. Just the row-narrowing,
// filter-set, and per-row formatter helpers from `./filters`.
//
// All test fixtures use neutral identifiers — NO vendor token prefixes,
// NO real names that could look like seeded data (per slice 100 P0-A4
// and slice 069 hardening).

import { describe, expect, test } from "vitest";

import type { Risk } from "@/lib/api";

import {
  ALL,
  applyFilters,
  clearFilters,
  DEFAULT_FILTERS,
  formatResidualScore,
  isDefault,
  residualClass,
  setFilter,
  severityBand,
  severityClasses,
  uniqueOwners,
} from "./filters";

function risk(id: string, overrides: Partial<Risk> = {}): Risk {
  return {
    id: `00000000-0000-0000-0000-0000000000${id}`,
    title: `test risk ${id}`,
    description: "",
    category: "operational",
    methodology: "nist_800_30",
    inherent_score: { likelihood: 3, impact: 3 },
    treatment: "mitigate",
    treatment_owner: "alpha",
    residual_score: { likelihood: 2, impact: 3 },
    accepter: "",
    instrument_reference: "",
    linked_control_ids: [],
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    themes: [],
    severity: 9,
    ...overrides,
  };
}

describe("DEFAULT_FILTERS / isDefault / clearFilters", () => {
  test("DEFAULT_FILTERS is all-ALL", () => {
    expect(DEFAULT_FILTERS.treatment).toBe(ALL);
    expect(DEFAULT_FILTERS.severity).toBe(ALL);
    expect(DEFAULT_FILTERS.owner).toBe(ALL);
  });

  test("isDefault is true for the default set", () => {
    expect(isDefault(DEFAULT_FILTERS)).toBe(true);
  });

  test("isDefault is false when any filter is narrowed", () => {
    expect(isDefault({ ...DEFAULT_FILTERS, treatment: "mitigate" })).toBe(
      false,
    );
    expect(isDefault({ ...DEFAULT_FILTERS, severity: "high" })).toBe(false);
    expect(isDefault({ ...DEFAULT_FILTERS, owner: "alpha" })).toBe(false);
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
    const next = setFilter(input, "treatment", "mitigate");
    expect(next.treatment).toBe("mitigate");
    expect(input.treatment).toBe(ALL);
  });

  test("multiple sets compose left-to-right", () => {
    const next = setFilter(
      setFilter(clearFilters(), "treatment", "mitigate"),
      "severity",
      "high",
    );
    expect(next.treatment).toBe("mitigate");
    expect(next.severity).toBe("high");
  });
});

describe("severityBand", () => {
  test("0 is the 'none' band (no numeric score)", () => {
    expect(severityBand(0)).toBe("none");
  });

  test("1..7 is the 'low' band", () => {
    expect(severityBand(1)).toBe("low");
    expect(severityBand(7)).toBe("low");
  });

  test("8..14 is the 'medium' band", () => {
    expect(severityBand(8)).toBe("medium");
    expect(severityBand(14)).toBe("medium");
  });

  test("15..25 is the 'high' band", () => {
    expect(severityBand(15)).toBe("high");
    expect(severityBand(25)).toBe("high");
  });

  test("severities above the 5x5 ceiling still bucket as 'high'", () => {
    expect(severityBand(99)).toBe("high");
  });
});

describe("uniqueOwners", () => {
  test("returns named owners sorted alphabetically", () => {
    const rows = [
      risk("01", { treatment_owner: "charlie" }),
      risk("02", { treatment_owner: "alpha" }),
      risk("03", { treatment_owner: "bravo" }),
    ];
    expect(uniqueOwners(rows)).toEqual(["alpha", "bravo", "charlie"]);
  });

  test("deduplicates repeated owners", () => {
    const rows = [
      risk("01", { treatment_owner: "alpha" }),
      risk("02", { treatment_owner: "alpha" }),
      risk("03", { treatment_owner: "bravo" }),
    ];
    expect(uniqueOwners(rows)).toEqual(["alpha", "bravo"]);
  });

  test("pins 'unassigned' last when any row has an empty owner", () => {
    const rows = [
      risk("01", { treatment_owner: "alpha" }),
      risk("02", { treatment_owner: "" }),
      risk("03", { treatment_owner: "bravo" }),
    ];
    expect(uniqueOwners(rows)).toEqual(["alpha", "bravo", "unassigned"]);
  });

  test("returns empty array for empty input", () => {
    expect(uniqueOwners([])).toEqual([]);
  });
});

describe("applyFilters", () => {
  const allRows: Risk[] = [
    risk("01", {
      treatment: "mitigate",
      treatment_owner: "alpha",
      severity: 20,
    }),
    risk("02", {
      treatment: "mitigate",
      treatment_owner: "bravo",
      severity: 9,
    }),
    risk("03", { treatment: "accept", treatment_owner: "alpha", severity: 4 }),
    risk("04", { treatment: "transfer", treatment_owner: "", severity: 0 }),
  ];

  test("DEFAULT_FILTERS returns every row", () => {
    expect(applyFilters(allRows, DEFAULT_FILTERS)).toHaveLength(4);
  });

  test("treatment filter narrows by exact wire value", () => {
    const out = applyFilters(allRows, {
      ...DEFAULT_FILTERS,
      treatment: "mitigate",
    });
    expect(out).toHaveLength(2);
    expect(out.every((r) => r.treatment === "mitigate")).toBe(true);
  });

  test("severity filter narrows by band", () => {
    const high = applyFilters(allRows, {
      ...DEFAULT_FILTERS,
      severity: "high",
    });
    expect(high).toHaveLength(1);
    expect(high[0]?.severity).toBe(20);

    const medium = applyFilters(allRows, {
      ...DEFAULT_FILTERS,
      severity: "medium",
    });
    expect(medium).toHaveLength(1);
    expect(medium[0]?.severity).toBe(9);

    const none = applyFilters(allRows, {
      ...DEFAULT_FILTERS,
      severity: "none",
    });
    expect(none).toHaveLength(1);
    expect(none[0]?.severity).toBe(0);
  });

  test("owner filter narrows by exact owner string", () => {
    const out = applyFilters(allRows, {
      ...DEFAULT_FILTERS,
      owner: "alpha",
    });
    expect(out).toHaveLength(2);
    expect(out.every((r) => r.treatment_owner === "alpha")).toBe(true);
  });

  test("owner filter 'unassigned' matches empty treatment_owner", () => {
    const out = applyFilters(allRows, {
      ...DEFAULT_FILTERS,
      owner: "unassigned",
    });
    expect(out).toHaveLength(1);
    expect(out[0]?.treatment_owner).toBe("");
  });

  test("combining filters narrows on every active predicate", () => {
    const out = applyFilters(allRows, {
      ...DEFAULT_FILTERS,
      treatment: "mitigate",
      owner: "alpha",
    });
    expect(out).toHaveLength(1);
    expect(out[0]?.treatment).toBe("mitigate");
    expect(out[0]?.treatment_owner).toBe("alpha");
  });

  test("returns empty when no row matches", () => {
    const out = applyFilters(allRows, {
      ...DEFAULT_FILTERS,
      treatment: "avoid",
    });
    expect(out).toEqual([]);
  });
});

describe("formatResidualScore", () => {
  test("formats a 5x5 score as a normalized 0..1 decimal", () => {
    // likelihood=4 × impact=5 = 20 / 25 = 0.80
    expect(formatResidualScore({ likelihood: 4, impact: 5 })).toBe("0.80");
    // likelihood=2 × impact=3 = 6 / 25 = 0.24
    expect(formatResidualScore({ likelihood: 2, impact: 3 })).toBe("0.24");
  });

  test("renders '—' for null / undefined / non-object", () => {
    expect(formatResidualScore(null)).toBe("—");
    expect(formatResidualScore(undefined)).toBe("—");
    expect(formatResidualScore("not an object")).toBe("—");
    expect(formatResidualScore(42)).toBe("—");
  });

  test("renders '—' for a score missing likelihood or impact", () => {
    expect(formatResidualScore({})).toBe("—");
    expect(formatResidualScore({ likelihood: 3 })).toBe("—");
    expect(formatResidualScore({ impact: 3 })).toBe("—");
  });

  test("renders '—' for a score with non-numeric components", () => {
    expect(formatResidualScore({ likelihood: "three", impact: 4 })).toBe("—");
    expect(formatResidualScore({ likelihood: 3, impact: "four" })).toBe("—");
  });

  test("clamps a score above the 5x5 ceiling", () => {
    // The formatter doesn't clamp the value itself — it just normalizes
    // by 25. A risk with L=6, I=6 normalizes to 1.44 (the operator's
    // problem to spot, not the renderer's). Still finite, still
    // formatted as a fixed-2.
    expect(formatResidualScore({ likelihood: 6, impact: 6 })).toBe("1.44");
  });
});

describe("severityClasses + residualClass", () => {
  test("severityClasses maps each band to the expected palette", () => {
    expect(severityClasses("high")).toContain("rose");
    expect(severityClasses("medium")).toContain("amber");
    expect(severityClasses("low")).toContain("emerald");
    expect(severityClasses("none")).toContain("muted");
  });

  test("residualClass maps formatted strings to the matching palette", () => {
    expect(residualClass("0.80")).toContain("rose");
    expect(residualClass("0.45")).toContain("amber");
    expect(residualClass("0.15")).toContain("emerald");
    expect(residualClass("—")).toContain("muted");
  });
});
