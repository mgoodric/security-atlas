// Slice 099 — unit tests for the /evidence filter logic.

import { describe, expect, test } from "vitest";

import type { Anchor } from "@/lib/api";

import {
  ALL,
  NONE,
  buildControlOptions,
  clearFilters,
  DEFAULT_FILTERS,
  isDefault,
  isNoneSelected,
  setFilter,
} from "./filters";

describe("evidence filters", () => {
  test("DEFAULT_FILTERS has no control selected", () => {
    expect(DEFAULT_FILTERS.controlId).toBe(NONE);
    expect(isNoneSelected(DEFAULT_FILTERS)).toBe(true);
    expect(isDefault(DEFAULT_FILTERS)).toBe(true);
  });

  test("setFilter returns a new filter with the updated key", () => {
    const next = setFilter(
      DEFAULT_FILTERS,
      "controlId",
      "33333333-3333-3333-3333-333333333333",
    );
    expect(next.controlId).toBe("33333333-3333-3333-3333-333333333333");
    expect(isNoneSelected(next)).toBe(false);
    expect(isDefault(next)).toBe(false);
    // immutability
    expect(DEFAULT_FILTERS.controlId).toBe(NONE);
  });

  test("clearFilters returns the default", () => {
    const cleared = clearFilters();
    expect(cleared.controlId).toBe(NONE);
    expect(isNoneSelected(cleared)).toBe(true);
  });

  test("ALL sentinel is distinct from NONE sentinel", () => {
    // The two sentinels overlap in intent ("no narrowing") but mean
    // different things to the data-fetch gate: ALL is used by the
    // shared 098 shell, NONE is the v1 reality that we cannot fetch
    // without a control_id. They must stay distinct values so a future
    // refactor doesn't collapse them.
    expect(ALL).not.toBe(NONE);
  });

  test("buildControlOptions prepends the NONE sentinel", () => {
    const anchors: Anchor[] = [
      {
        id: "11111111-1111-1111-1111-111111111111",
        scf_id: "SCF:IAC-06",
        family: "IAC",
        name: "Multi-factor authentication",
        description: "",
      },
      {
        id: "22222222-2222-2222-2222-222222222222",
        scf_id: "SCF:CRY-04",
        family: "CRY",
        name: "Encryption at rest",
        description: "",
      },
    ];
    const opts = buildControlOptions(anchors);
    expect(opts).toHaveLength(3);
    expect(opts[0]).toEqual({ value: NONE, label: "Select a control…" });
    // Sorted alphabetically by scf_id (CRY before IAC).
    expect(opts[1]?.value).toBe("22222222-2222-2222-2222-222222222222");
    expect(opts[1]?.label).toBe("SCF:CRY-04 · Encryption at rest");
    expect(opts[2]?.value).toBe("11111111-1111-1111-1111-111111111111");
  });

  test("buildControlOptions handles an empty anchor list", () => {
    const opts = buildControlOptions([]);
    expect(opts).toHaveLength(1);
    expect(opts[0]).toEqual({ value: NONE, label: "Select a control…" });
  });
});
