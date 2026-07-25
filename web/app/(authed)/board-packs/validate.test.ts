// Slice 665 — vitest pin for the board-pack generate-draft validation fn.
//
// Locks in the client-side gate the slice mandates: an empty (or malformed)
// quarter-end date produces a field-level error BEFORE submit, not a silent
// no-op (audit ATLAS-015). Mirrors the risks/new/validate.test.ts shape.

import { describe, expect, test } from "vitest";

import { hasErrors, validateGenerateForm } from "./validate";

describe("validateGenerateForm — quarter-end date required", () => {
  test("passes for a complete ISO calendar date", () => {
    const e = validateGenerateForm({ periodEnd: "2026-03-31" });
    expect(e).toEqual({});
    expect(hasErrors(e)).toBe(false);
  });

  test("fails when the date is empty", () => {
    const e = validateGenerateForm({ periodEnd: "" });
    expect(e.periodEnd).toMatch(/enter a quarter-end date/i);
    expect(hasErrors(e)).toBe(true);
  });

  test("fails when the date is whitespace only", () => {
    const e = validateGenerateForm({ periodEnd: "   " });
    expect(e.periodEnd).toMatch(/enter a quarter-end date/i);
    expect(hasErrors(e)).toBe(true);
  });

  test("fails for a malformed (non-ISO) date string", () => {
    const e = validateGenerateForm({ periodEnd: "31/03/2026" });
    expect(e.periodEnd).toMatch(/valid quarter-end date/i);
    expect(hasErrors(e)).toBe(true);
  });

  test("fails for an ISO-shaped but impossible calendar date", () => {
    const e = validateGenerateForm({ periodEnd: "2026-13-40" });
    expect(e.periodEnd).toMatch(/valid quarter-end date/i);
    expect(hasErrors(e)).toBe(true);
  });

  test("trims surrounding whitespace before validating a real date", () => {
    const e = validateGenerateForm({ periodEnd: "  2026-06-30  " });
    expect(e).toEqual({});
    expect(hasErrors(e)).toBe(false);
  });
});
