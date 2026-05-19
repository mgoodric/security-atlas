// Slice 151 — vitest pin for the risk-create form's validation fn.
//
// Covers the mitigate-requires-link rule (the slice's headline behavior)
// plus the carry-over title/treatment_owner required-field checks. The
// goal is to lock in the client-side gate that P0-RISK-1 mandates
// ("UI MUST enforce validation client-side; user experience is
// field-level error before submit, not submit-then-see-error").

import { describe, expect, test } from "vitest";

import { hasErrors, validateRiskForm } from "./validate";

const baseValid = {
  title: "Vendor data breach exposure",
  treatment_owner: "alice",
  treatment: "mitigate" as const,
  linked_control_ids: ["00000000-0000-0000-0000-000000000010"],
};

describe("validateRiskForm — mitigate + linked controls", () => {
  test("passes when mitigate + at least one linked control", () => {
    const e = validateRiskForm(baseValid);
    expect(e).toEqual({});
    expect(hasErrors(e)).toBe(false);
  });

  test("fails when mitigate + zero linked controls", () => {
    const e = validateRiskForm({ ...baseValid, linked_control_ids: [] });
    expect(e.linked_control_ids).toMatch(/at least one control/i);
    expect(hasErrors(e)).toBe(true);
  });

  test("passes when treatment is accept + zero linked controls", () => {
    const e = validateRiskForm({
      ...baseValid,
      treatment: "accept",
      linked_control_ids: [],
    });
    expect(e.linked_control_ids).toBeUndefined();
    expect(hasErrors(e)).toBe(false);
  });

  test("passes when treatment is transfer + zero linked controls", () => {
    const e = validateRiskForm({
      ...baseValid,
      treatment: "transfer",
      linked_control_ids: [],
    });
    expect(e.linked_control_ids).toBeUndefined();
  });

  test("passes when treatment is avoid + zero linked controls", () => {
    const e = validateRiskForm({
      ...baseValid,
      treatment: "avoid",
      linked_control_ids: [],
    });
    expect(e.linked_control_ids).toBeUndefined();
  });

  test("passes when mitigate + multiple linked controls", () => {
    const e = validateRiskForm({
      ...baseValid,
      linked_control_ids: [
        "00000000-0000-0000-0000-000000000010",
        "00000000-0000-0000-0000-000000000011",
        "00000000-0000-0000-0000-000000000012",
      ],
    });
    expect(e).toEqual({});
  });
});

describe("validateRiskForm — carry-over required fields", () => {
  test("fails when title is empty", () => {
    const e = validateRiskForm({ ...baseValid, title: "" });
    expect(e.title).toMatch(/required/i);
    expect(hasErrors(e)).toBe(true);
  });

  test("fails when title is whitespace only", () => {
    const e = validateRiskForm({ ...baseValid, title: "   " });
    expect(e.title).toMatch(/required/i);
  });

  test("fails when treatment_owner is empty", () => {
    const e = validateRiskForm({ ...baseValid, treatment_owner: "" });
    expect(e.treatment_owner).toMatch(/required/i);
  });

  test("aggregates multiple errors", () => {
    const e = validateRiskForm({
      title: "",
      treatment_owner: "",
      treatment: "mitigate",
      linked_control_ids: [],
    });
    expect(e.title).toBeDefined();
    expect(e.treatment_owner).toBeDefined();
    expect(e.linked_control_ids).toBeDefined();
    expect(hasErrors(e)).toBe(true);
  });
});

describe("hasErrors", () => {
  test("returns false for empty error object", () => {
    expect(hasErrors({})).toBe(false);
  });

  test("returns true when any field has an error", () => {
    expect(hasErrors({ title: "Required" })).toBe(true);
  });

  test("ignores empty-string error values", () => {
    expect(hasErrors({ title: "" })).toBe(false);
  });
});
