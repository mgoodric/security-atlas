// Slice 237 — vitest unit coverage for the cursor-stack helpers.
//
// Pure-data tests for `pushCursor` and `popCursor` exported from
// `./cursor-pagination`. No React, no DOM — vitest env is `node` per
// slice 069 P0-A3.
//
// The helpers back the client-side "Previous" stack on the `/evidence`
// page (slice 237 AC-3/AC-4/AC-5). They are pure so the page-level
// wiring stays testable without a render harness.

import { describe, expect, test } from "vitest";

import { popCursor, pushCursor } from "./cursor-pagination";

describe("pushCursor", () => {
  test("empty stack returns single-element stack", () => {
    expect(pushCursor([], "c1")).toEqual(["c1"]);
  });

  test("non-empty stack appends cursor", () => {
    expect(pushCursor(["c1", "c2"], "c3")).toEqual(["c1", "c2", "c3"]);
  });

  test("returns a NEW array (not the same reference)", () => {
    const before: string[] = ["c1"];
    const after = pushCursor(before, "c2");
    expect(after).not.toBe(before);
    // Original is unmutated.
    expect(before).toEqual(["c1"]);
  });

  test("pushing an empty-string cursor still appends it", () => {
    // The page-level wiring SHOULD avoid pushing empty cursors, but the
    // helper itself does not police that — it is a pure stack op.
    expect(pushCursor(["c1"], "")).toEqual(["c1", ""]);
  });
});

describe("popCursor", () => {
  test("empty stack returns { popped: undefined, rest: [] }", () => {
    expect(popCursor([])).toEqual({ popped: undefined, rest: [] });
  });

  test("single-element stack returns { popped: 'c1', rest: [] }", () => {
    expect(popCursor(["c1"])).toEqual({ popped: "c1", rest: [] });
  });

  test("multi-element stack pops the LAST cursor", () => {
    expect(popCursor(["c1", "c2", "c3"])).toEqual({
      popped: "c3",
      rest: ["c1", "c2"],
    });
  });

  test("returns a NEW rest array (not a slice mutation of the input)", () => {
    const before: string[] = ["c1", "c2"];
    const { rest } = popCursor(before);
    expect(rest).not.toBe(before);
    // Original is unmutated.
    expect(before).toEqual(["c1", "c2"]);
  });
});

describe("push / pop round-trip", () => {
  // Models the page-level Next → Previous round-trip. The cursor that
  // was on the URL when the user clicked Next gets pushed; clicking
  // Previous pops it back as the cursor to re-fetch.
  test("Next → Previous returns the original cursor", () => {
    const stack0: string[] = [];
    // User is on the first page (no URL cursor). Clicks Next; current
    // (empty / undefined) cursor would not be pushed by the page wiring,
    // but for the unit-level the helper is agnostic — we model the page
    // having captured the cursor that was on the URL.
    const stack1 = pushCursor(stack0, "page1-cursor");
    expect(stack1).toEqual(["page1-cursor"]);

    // User is now on page 2 (URL has page1-cursor). Clicks Next again;
    // the page captures the page-2 cursor onto the stack.
    const stack2 = pushCursor(stack1, "page2-cursor");
    expect(stack2).toEqual(["page1-cursor", "page2-cursor"]);

    // User clicks Previous: pop the most-recent cursor.
    const { popped: pop1, rest: stack3 } = popCursor(stack2);
    expect(pop1).toBe("page2-cursor");
    expect(stack3).toEqual(["page1-cursor"]);

    // User clicks Previous again: pop the original cursor.
    const { popped: pop2, rest: stack4 } = popCursor(stack3);
    expect(pop2).toBe("page1-cursor");
    expect(stack4).toEqual([]);

    // Stack is now empty — the next Previous click should clear the
    // URL cursor (page-level concern; the helper just reports the empty
    // stack).
    const { popped: pop3, rest: stack5 } = popCursor(stack4);
    expect(pop3).toBeUndefined();
    expect(stack5).toEqual([]);
  });
});
