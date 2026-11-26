// #2135 — the /needs-attention page's URL state.

import { describe, expect, test } from "vitest";

import {
  needsAttentionSearchString,
  parseNeedsAttentionSearch,
  toggleType,
} from "./search";

const parse = (qs: string) =>
  parseNeedsAttentionSearch(new URLSearchParams(qs));

describe("parseNeedsAttentionSearch", () => {
  test("an empty URL leaves the view to the server, no types, page 1", () => {
    expect(parse("")).toEqual({ view: undefined, types: [], offset: 0 });
  });

  test("reads for, type and offset", () => {
    expect(parse("for=all&type=control,risk&offset=25")).toEqual({
      view: "all",
      types: ["control", "risk"],
      offset: 25,
    });
  });

  test("drops an unknown view, unknown or repeated types and a bad offset", () => {
    expect(parse("for=team&type=control,bogus,control&offset=-3")).toEqual({
      view: undefined,
      types: ["control"],
      offset: 0,
    });
    expect(parse("offset=abc").offset).toBe(0);
  });
});

describe("needsAttentionSearchString", () => {
  test("round-trips and omits empty keys", () => {
    expect(needsAttentionSearchString(parse(""))).toBe("");
    expect(
      needsAttentionSearchString({
        view: "me",
        types: ["policy"],
        offset: 50,
      }),
    ).toBe("?for=me&type=policy&offset=50");
  });
});

describe("toggleType", () => {
  test("adds and removes a type and returns to the first page", () => {
    const s = { view: "all" as const, types: ["control"], offset: 25 };
    expect(toggleType(s, "risk")).toEqual({
      view: "all",
      types: ["control", "risk"],
      offset: 0,
    });
    expect(toggleType(s, "control")).toEqual({
      view: "all",
      types: [],
      offset: 0,
    });
  });
});
