// #2135 — the needs-attention query builder and labels.

import { describe, expect, test } from "vitest";

import {
  needsAttentionBandLabel,
  needsAttentionQuery,
  needsAttentionTypeLabel,
} from "./needs-attention";

describe("needsAttentionQuery", () => {
  test("is empty with no params, so the server picks the view", () => {
    expect(needsAttentionQuery({})).toBe("");
  });

  test("sets for, type, limit and a non-zero offset", () => {
    expect(
      needsAttentionQuery({
        view: "all",
        types: ["control", "risk"],
        limit: 10,
        offset: 20,
      }),
    ).toBe("?for=all&type=control%2Crisk&limit=10&offset=20");
  });

  test("drops an empty type list and a zero offset", () => {
    expect(needsAttentionQuery({ view: "me", types: [], offset: 0 })).toBe(
      "?for=me",
    );
  });
});

describe("labels", () => {
  test("names known types and bands, and humanizes unknown ones", () => {
    expect(needsAttentionTypeLabel("action_plan")).toBe("Action plan");
    expect(needsAttentionTypeLabel("new_thing")).toBe("new thing");
    expect(needsAttentionBandLabel("ownership_gap")).toBe("No owner");
    expect(needsAttentionBandLabel("due_soon")).toBe("Due soon");
  });
});
