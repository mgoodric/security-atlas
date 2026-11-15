import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { fileURLToPath } from "node:url";

import { describe, expect, it } from "vitest";

import {
  dimensionOptions,
  fromPredicate,
  toPredicate,
  toggleValue,
  type PickerState,
} from "./scope-picker-model";

// The fixture internal/scope/picker_fixture_test.go also reads: there every
// predicate goes through scope.Evaluate; here the picker emits and loads it.
const fixturePath = resolve(
  fileURLToPath(new URL(".", import.meta.url)),
  "../../../internal/scope/testdata/picker-predicates.json",
);
const fixture = JSON.parse(readFileSync(fixturePath, "utf8")) as {
  cases: {
    name: string;
    predicate: unknown;
    picker: PickerState | "advanced";
    generates: boolean;
  }[];
};

describe("scope picker model — shared fixture", () => {
  it("has cases", () => {
    expect(fixture.cases.length).toBeGreaterThan(0);
  });

  for (const tc of fixture.cases) {
    if (tc.generates && tc.picker !== "advanced") {
      const state = tc.picker;
      it(`emits the predicate for: ${tc.name}`, () => {
        expect(toPredicate(state)).toEqual(tc.predicate);
      });
    }
    it(`loads: ${tc.name}`, () => {
      const got = fromPredicate(tc.predicate);
      if (tc.picker === "advanced") {
        expect(got.kind).toBe("advanced");
      } else {
        expect(got).toEqual({ kind: "picker", state: tc.picker });
      }
    });
  }
});

describe("toPredicate", () => {
  it("Everything yields the always-true predicate", () => {
    expect(toPredicate({ mode: "everything" })).toEqual({ op: "true" });
  });

  it("environment=prod plus data classification in {restricted, confidential} yields the all-of predicate", () => {
    expect(
      toPredicate({
        mode: "dimensions",
        selections: {
          data_classification: ["restricted", "confidential"],
          environment: ["prod"],
        },
      }),
    ).toEqual({
      op: "and",
      args: [
        { op: "eq", dim: "environment", value: "prod" },
        {
          op: "in",
          dim: "data_classification",
          values: ["restricted", "confidential"],
        },
      ],
    });
  });

  it("is null when no dimension has a value picked", () => {
    expect(toPredicate({ mode: "dimensions", selections: {} })).toBeNull();
    expect(
      toPredicate({ mode: "dimensions", selections: { environment: [] } }),
    ).toBeNull();
  });

  it("round-trips through fromPredicate", () => {
    const state: PickerState = {
      mode: "dimensions",
      selections: { geography: ["us", "eu"], product_line: ["core"] },
    };
    expect(fromPredicate(toPredicate(state))).toEqual({
      kind: "picker",
      state,
    });
  });
});

describe("fromPredicate", () => {
  it("reports a non-object as Advanced-only", () => {
    expect(fromPredicate("true").kind).toBe("advanced");
    expect(fromPredicate([]).kind).toBe("advanced");
  });

  it("reports an empty all-of and malformed clauses as Advanced-only", () => {
    expect(fromPredicate({ op: "and", args: [] }).kind).toBe("advanced");
    expect(fromPredicate({ op: "eq", dim: "environment" }).kind).toBe(
      "advanced",
    );
    expect(
      fromPredicate({ op: "in", dim: "environment", values: [1] }).kind,
    ).toBe("advanced");
    expect(fromPredicate({ op: "and", args: ["x"] }).kind).toBe("advanced");
  });

  it("names the reason", () => {
    expect(
      fromPredicate({ op: "eq", dim: "region_tier", value: "gold" }),
    ).toEqual({
      kind: "advanced",
      reason: 'filters on "region_tier", not a built-in dimension',
    });
  });
});

describe("dimensionOptions", () => {
  const dims = [
    {
      name: "environment",
      value_type: "string",
      allowed_values: ["prod", "staging"],
      is_required: true,
      is_builtin: true,
    },
    {
      name: "geography",
      value_type: "string",
      allowed_values: null,
      is_required: false,
      is_builtin: true,
    },
  ];
  const cells = [
    { dimensions: { environment: "prod", geography: "us" } },
    { dimensions: { environment: "staging", geography: "eu" } },
    { dimensions: { environment: "prod", geography: "us" } },
  ];

  it("uses allowed values, else the values cells use", () => {
    const got = dimensionOptions(dims, cells);
    expect(got.environment).toEqual(["prod", "staging"]);
    expect(got.geography).toEqual(["eu", "us"]);
    expect(got.business_unit).toEqual([]);
  });

  it("keeps a selected value the options do not list", () => {
    const got = dimensionOptions(dims, cells, { environment: ["legacy"] });
    expect(got.environment).toEqual(["prod", "staging", "legacy"]);
  });
});

describe("toggleValue", () => {
  const options = ["restricted", "confidential", "internal", "public"];

  it("adds in option order and removes", () => {
    let s = toggleValue({}, "data_classification", "public", options);
    s = toggleValue(s, "data_classification", "restricted", options);
    expect(s.data_classification).toEqual(["restricted", "public"]);
    s = toggleValue(s, "data_classification", "public", options);
    expect(s.data_classification).toEqual(["restricted"]);
  });

  it("puts a value outside the options last", () => {
    const s = toggleValue(
      { data_classification: ["legacy"] },
      "data_classification",
      "internal",
      options,
    );
    expect(s.data_classification).toEqual(["internal", "legacy"]);
  });
});
