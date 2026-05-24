// Slice 254 — vitest coverage for the control-detail tab helpers.
//
// Pure-helper coverage per the established `coverage.ts/test.ts`
// sibling pattern (slice 256). The page-level renderer delegates to
// these helpers so the literal-union math + chip-formatting rules
// don't need to be re-derived in render-shape tests.

import { describe, expect, it } from "vitest";

import { CONTROL_TABS, formatTabCount, isTabKey } from "./tabs";

describe("CONTROL_TABS", () => {
  it("renders the seven mockup tabs in mockup order", () => {
    // Plans/mockups/control.html lines 143-149: Overview, Evidence,
    // Mappings, Effective scope, Policies, Risks, History.
    expect(CONTROL_TABS.map((t) => t.key)).toEqual([
      "overview",
      "evidence",
      "mappings",
      "scope",
      "policies",
      "risks",
      "history",
    ]);
  });

  it("tab labels match the mockup copy", () => {
    expect(CONTROL_TABS.map((t) => t.label)).toEqual([
      "Overview",
      "Evidence",
      "Mappings",
      "Effective scope",
      "Policies",
      "Risks",
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

  it("rejects null", () => {
    expect(isTabKey(null)).toBe(false);
  });

  it("rejects undefined", () => {
    expect(isTabKey(undefined)).toBe(false);
  });

  it("rejects the empty string", () => {
    expect(isTabKey("")).toBe(false);
  });

  it("rejects unrecognised strings", () => {
    expect(isTabKey("foo")).toBe(false);
    expect(isTabKey("Overview")).toBe(false); // case-sensitive
    expect(isTabKey("settings")).toBe(false);
  });
});

describe("formatTabCount", () => {
  it("renders zero as `0` (not `—`) — zero is real data", () => {
    expect(formatTabCount(0)).toBe("0");
  });

  it("renders small counts verbatim", () => {
    expect(formatTabCount(1)).toBe("1");
    expect(formatTabCount(11)).toBe("11");
    expect(formatTabCount(847)).toBe("847");
  });

  it("inserts comma thousands separators for large counts (D3)", () => {
    expect(formatTabCount(1247)).toBe("1,247");
    expect(formatTabCount(12345)).toBe("12,345");
    expect(formatTabCount(1234567)).toBe("1,234,567");
  });

  it("renders `—` for null (loading / unknown)", () => {
    expect(formatTabCount(null)).toBe("—");
  });

  it("renders `—` for undefined", () => {
    expect(formatTabCount(undefined)).toBe("—");
  });

  it("renders `—` for NaN / Infinity (defensive)", () => {
    expect(formatTabCount(Number.NaN)).toBe("—");
    expect(formatTabCount(Number.POSITIVE_INFINITY)).toBe("—");
    expect(formatTabCount(Number.NEGATIVE_INFINITY)).toBe("—");
  });

  it("renders `—` for negative counts (defensive — backend never returns)", () => {
    expect(formatTabCount(-1)).toBe("—");
  });

  it("truncates fractional counts (backend never returns, but defensive)", () => {
    expect(formatTabCount(847.9)).toBe("847");
  });
});
