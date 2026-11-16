// #2106 — the /action-plans/new?risk_id= prefill.

import { describe, expect, test } from "vitest";

import { prefillFromSearch } from "./prefill";

const RISK = "3f2a1b4c-5d6e-4f70-8a9b-0c1d2e3f4a5b";

describe("prefillFromSearch (#2106)", () => {
  test("a risk id starts the form linked to it and returns to the risk", () => {
    expect(prefillFromSearch({ risk_id: RISK })).toEqual({
      riskIds: [RISK],
      returnTo: `/risks/${RISK}`,
    });
  });

  test("the id is lowercased", () => {
    expect(prefillFromSearch({ risk_id: RISK.toUpperCase() }).riskIds).toEqual([
      RISK,
    ]);
  });

  test("the first of a repeated parameter is used", () => {
    expect(prefillFromSearch({ risk_id: [RISK, "other"] }).riskIds).toEqual([
      RISK,
    ]);
  });

  test.each([
    ["no parameter", {}],
    ["an empty value", { risk_id: "" }],
    ["a non-UUID", { risk_id: "../controls" }],
    ["the nil UUID", { risk_id: "00000000-0000-0000-0000-000000000000" }],
  ])("%s prefills nothing", (_name, search) => {
    expect(prefillFromSearch(search)).toEqual({ riskIds: [], returnTo: null });
  });
});
