import { describe, expect, it } from "vitest";

import {
  kindOptions,
  predicateBody,
  rawToPicker,
  toggleKind,
} from "./scope-builder";

describe("predicateBody", () => {
  it("sends Everything as the column default {}", () => {
    expect(predicateBody({ op: "true" })).toBe("{}");
    expect(predicateBody({})).toBe("{}");
  });

  it("sends a picked predicate as its JSON", () => {
    const eq = { op: "eq", dim: "environment", value: "prod" };
    expect(JSON.parse(predicateBody(eq))).toEqual(eq);
  });
});

describe("rawToPicker", () => {
  it("loads a predicate the builder can show", () => {
    const both = { op: "in", dim: "environment", values: ["prod", "staging"] };
    expect(rawToPicker(JSON.stringify(both))).toEqual({
      ok: true,
      predicate: both,
    });
  });

  it("reads blank text as Everything", () => {
    expect(rawToPicker("  ")).toEqual({ ok: true, predicate: { op: "true" } });
  });

  it("says why invalid JSON cannot go back", () => {
    const r = rawToPicker("{nope");
    expect(r.ok).toBe(false);
    if (!r.ok) expect(r.error).toMatch(/^Invalid JSON/);
  });

  it("says why a predicate the builder cannot show stays as JSON", () => {
    const r = rawToPicker(
      JSON.stringify({
        op: "or",
        args: [{ op: "eq", dim: "environment", value: "prod" }],
      }),
    );
    expect(r.ok).toBe(false);
    if (!r.ok) {
      expect(r.error).toContain('uses "or"');
      expect(r.error).toContain("issue the key with it as written");
    }
  });

  it("refuses a value that is not a JSON object", () => {
    const r = rawToPicker("null");
    expect(r.ok).toBe(false);
  });
});

describe("kindOptions", () => {
  it("lists each registered kind once, sorted, plus any selected", () => {
    expect(
      kindOptions(
        [{ kind: "b.kind.v1" }, { kind: "a.kind.v1" }, { kind: "b.kind.v1" }],
        ["custom.kind.v1"],
      ),
    ).toEqual(["a.kind.v1", "b.kind.v1", "custom.kind.v1"]);
  });
});

describe("toggleKind", () => {
  it("adds and removes, keeping option order", () => {
    const opts = ["a", "b", "c"];
    expect(toggleKind(["c"], "a", opts)).toEqual(["a", "c"]);
    expect(toggleKind(["a", "c"], "a", opts)).toEqual(["c"]);
  });
});
