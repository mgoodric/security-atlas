// Slice 613 — vitest unit coverage for the gate-mode pure helpers.
//
// Node-only tier (slice 069): exercises every branch of the mode set, the
// parser's fail-safe-toward-strict fallback, the type guard, and the
// description lookup. The page control itself is Playwright-covered.

import { describe, expect, test } from "vitest";

import {
  DEFAULT_GATE_MODE,
  GATE_MODES,
  GATE_MODE_OPTIONS,
  GateMode,
  describeGateMode,
  isGateMode,
  parseGateMode,
} from "./gate-mode";

describe("gate-mode constants", () => {
  test("DEFAULT_GATE_MODE is strict (slice 608 D2)", () => {
    expect(DEFAULT_GATE_MODE).toBe("strict");
  });

  test("GATE_MODES lists exactly the three modes, strict first", () => {
    expect([...GATE_MODES]).toEqual(["strict", "advisory", "mandatory_tests"]);
  });

  test("every mode has a label and a non-empty AC-3 explanation", () => {
    expect(GATE_MODE_OPTIONS.map((o) => o.value)).toEqual([...GATE_MODES]);
    for (const opt of GATE_MODE_OPTIONS) {
      expect(opt.label.length).toBeGreaterThan(0);
      expect(opt.description.length).toBeGreaterThan(0);
    }
  });
});

describe("isGateMode", () => {
  test.each([
    ["strict", true],
    ["advisory", true],
    ["mandatory_tests", true],
    ["", false],
    ["STRICT", false],
    ["off", false],
    [null, false],
    [undefined, false],
    [42, false],
    [{ value: "strict" }, false],
  ])("isGateMode(%o) === %s", (input, expected) => {
    expect(isGateMode(input)).toBe(expected);
  });
});

describe("parseGateMode", () => {
  test.each<[unknown, GateMode]>([
    ["strict", "strict"],
    ["advisory", "advisory"],
    ["mandatory_tests", "mandatory_tests"],
  ])("passes through valid value %o", (input, expected) => {
    expect(parseGateMode(input)).toBe(expected);
  });

  test.each<[unknown]>([[""], ["bogus"], [null], [undefined], [123], [{}]])(
    "falls back to strict for invalid value %o",
    (input) => {
      expect(parseGateMode(input)).toBe("strict");
    },
  );
});

describe("describeGateMode", () => {
  test("returns the matching AC-3 explanation for each mode", () => {
    for (const opt of GATE_MODE_OPTIONS) {
      expect(describeGateMode(opt.value)).toBe(opt.description);
    }
  });

  test("returns the strict explanation for an out-of-set value (defensive)", () => {
    // Cast an invalid value through the public surface to prove the
    // fallback branch — a malformed call must never render an empty string.
    expect(describeGateMode("nope" as GateMode)).toBe(
      GATE_MODE_OPTIONS[0].description,
    );
  });
});
