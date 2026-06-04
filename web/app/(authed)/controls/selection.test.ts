// Slice 448 — vitest for the pure multi-select logic. Node-env, no DOM.

import { describe, expect, it } from "vitest";

import {
  cappedSelection,
  isOverCap,
  pruneSelection,
  SELECTION_CAP,
  selectAllState,
  toggleSelectAll,
  toggleSelection,
} from "./selection";

describe("toggleSelection", () => {
  it("adds an absent id", () => {
    const out = toggleSelection(new Set(["a"]), "b");
    expect([...out].sort()).toEqual(["a", "b"]);
  });

  it("removes a present id", () => {
    const out = toggleSelection(new Set(["a", "b"]), "a");
    expect([...out]).toEqual(["b"]);
  });

  it("returns a new set (immutable)", () => {
    const src = new Set(["a"]);
    const out = toggleSelection(src, "b");
    expect(out).not.toBe(src);
    expect(src.has("b")).toBe(false);
  });
});

describe("selectAllState", () => {
  it("none when nothing visible is selected", () => {
    expect(selectAllState(["a", "b"], new Set())).toBe("none");
    expect(selectAllState(["a", "b"], new Set(["z"]))).toBe("none");
  });

  it("all when every visible row is selected", () => {
    expect(selectAllState(["a", "b"], new Set(["a", "b"]))).toBe("all");
  });

  it("some for a non-empty strict subset", () => {
    expect(selectAllState(["a", "b", "c"], new Set(["a"]))).toBe("some");
  });

  it("none for an empty visible set", () => {
    expect(selectAllState([], new Set(["a"]))).toBe("none");
  });
});

describe("toggleSelectAll", () => {
  it("selects every visible row when not all are selected", () => {
    const out = toggleSelectAll(["a", "b", "c"], new Set(["a"]));
    expect([...out].sort()).toEqual(["a", "b", "c"]);
  });

  it("deselects only the visible rows when all visible are selected", () => {
    // "x" is selected from a prior filtered view; toggling header off
    // must preserve it (only the visible a/b are cleared).
    const out = toggleSelectAll(["a", "b"], new Set(["a", "b", "x"]));
    expect([...out]).toEqual(["x"]);
  });

  it("preserves out-of-view selections when selecting all in view", () => {
    const out = toggleSelectAll(["a", "b"], new Set(["x"]));
    expect([...out].sort()).toEqual(["a", "b", "x"]);
  });
});

describe("isOverCap / cappedSelection", () => {
  it("false at or under the cap", () => {
    const atCap = new Set(
      Array.from({ length: SELECTION_CAP }, (_, i) => `id-${i}`),
    );
    expect(isOverCap(atCap)).toBe(false);
  });

  it("true above the cap", () => {
    const overCap = new Set(
      Array.from({ length: SELECTION_CAP + 1 }, (_, i) => `id-${i}`),
    );
    expect(isOverCap(overCap)).toBe(true);
  });

  it("cappedSelection truncates to the cap", () => {
    const overCap = new Set(
      Array.from({ length: SELECTION_CAP + 50 }, (_, i) => `id-${i}`),
    );
    expect(cappedSelection(overCap)).toHaveLength(SELECTION_CAP);
  });
});

describe("pruneSelection", () => {
  it("drops ids no longer present", () => {
    const out = pruneSelection(new Set(["a", "b", "c"]), ["a", "c"]);
    expect([...out].sort()).toEqual(["a", "c"]);
  });

  it("keeps everything when all present", () => {
    const out = pruneSelection(new Set(["a", "b"]), ["a", "b", "z"]);
    expect([...out].sort()).toEqual(["a", "b"]);
  });

  it("returns an empty set when nothing is present", () => {
    expect(pruneSelection(new Set(["a"]), [])).toEqual(new Set());
  });
});
