// Slice 254 — vitest coverage for the control-detail tab helpers.
//
// #2140 cut the tabs to Overview, Framework map and History.

import { describe, expect, it } from "vitest";

import { CONTROL_TABS, isTabKey } from "./tabs";

describe("CONTROL_TABS", () => {
  it("is the overview, the framework map and history, in that order", () => {
    expect(CONTROL_TABS.map((t) => t.key)).toEqual([
      "overview",
      "map",
      "history",
    ]);
    expect(CONTROL_TABS.map((t) => t.label)).toEqual([
      "Overview",
      "Framework map",
      "History",
    ]);
  });
});

describe("isTabKey", () => {
  it("accepts every key in CONTROL_TABS", () => {
    for (const t of CONTROL_TABS) {
      expect(isTabKey(t.key)).toBe(true);
    }
  });

  it("rejects null, undefined and the empty string", () => {
    expect(isTabKey(null)).toBe(false);
    expect(isTabKey(undefined)).toBe(false);
    expect(isTabKey("")).toBe(false);
  });

  it("rejects unrecognised strings", () => {
    expect(isTabKey("foo")).toBe(false);
    expect(isTabKey("Overview")).toBe(false); // case-sensitive
  });

  // #2140: an old deep link to a retired tab lands on the overview,
  // which now holds that content.
  it("rejects the retired tab keys", () => {
    for (const key of ["evidence", "mappings", "scope", "policies", "risks"]) {
      expect(isTabKey(key)).toBe(false);
    }
  });
});
