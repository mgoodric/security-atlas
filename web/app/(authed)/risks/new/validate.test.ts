// Slice 151 — vitest pin for the risk-create form's validation fn.
//
// Covers the mitigate-requires-link rule (the slice's headline behavior)
// plus the carry-over title/treatment_owner required-field checks. The
// goal is to lock in the client-side gate that P0-RISK-1 mandates
// ("UI MUST enforce validation client-side; user experience is
// field-level error before submit, not submit-then-see-error").

import { describe, expect, test } from "vitest";

import { DEFAULT_TREATMENT, hasErrors, validateRiskForm } from "./validate";

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

describe("DEFAULT_TREATMENT — slice 663 fresh-tenant dead-end", () => {
  test("the form's opening default treatment is not mitigate", () => {
    // mitigate requires a linked control, which a fresh tenant cannot
    // satisfy. The opening default must be a treatment with no
    // unsatisfiable required field.
    expect(DEFAULT_TREATMENT).not.toBe("mitigate");
  });

  test("the default treatment is avoid (status-only, zero required fields)", () => {
    expect(DEFAULT_TREATMENT).toBe("avoid");
  });

  test("AC-1: an empty-tenant default-flow submit is valid with zero linked controls", () => {
    // Simulate a brand-new operator: fills title + owner, leaves the
    // default treatment, has zero controls to link. validateRiskForm
    // must return no errors so the create flow does not dead-end.
    const e = validateRiskForm({
      title: "First program risk",
      treatment_owner: "security-lead",
      treatment: DEFAULT_TREATMENT,
      linked_control_ids: [],
    });
    expect(e).toEqual({});
    expect(hasErrors(e)).toBe(false);
  });

  test("AC-3: switching to mitigate in any tenant still requires a linked control", () => {
    // The relaxation is the opening default only — mitigate keeps its
    // canvas §6.1 invariant.
    const e = validateRiskForm({
      title: "First program risk",
      treatment_owner: "security-lead",
      treatment: "mitigate",
      linked_control_ids: [],
    });
    expect(e.linked_control_ids).toMatch(/at least one control/i);
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
