// Slice 219 — unit coverage for the board-pack header meta-strip label
// list. The vitest config (web/vitest.config.ts) is node-env / no-JSX so
// the component itself cannot be rendered here — the testable surface is
// the labels array the component renders from, per the slice 222
// (posture-coverage-caption.test.ts) precedent.
//
// What this test pins:
//   * The meta strip has exactly 3 cells (not 4 — slice 219 dropped the
//     hardcoded "Author" em-dash cell).
//   * The 3 labels are exactly Period end, Generated, Approver, in that
//     order — left-to-right reading is load-bearing for the board
//     audience.
//   * "Author" is NOT in the list (regression guard — re-introducing
//     the hardcoded em-dash would reopen the slice 219 honesty gap).

import { describe, expect, test } from "vitest";

import { PACK_HEADER_META_LABELS } from "./pack-header-meta";

describe("PACK_HEADER_META_LABELS", () => {
  test("contains exactly the three honest cells, in order", () => {
    expect([...PACK_HEADER_META_LABELS]).toEqual([
      "Period end",
      "Generated",
      "Approver",
    ]);
  });

  test("does not contain Author (slice 219 honesty fix)", () => {
    // Regression guard. The backend board-pack record has no author
    // field; re-adding this label with a hardcoded em-dash placeholder
    // reopens the UI-honesty gap that slice 219 closed. If a future
    // slice models authorship on the pack record, the label can return
    // — but only when it is data-backed.
    expect([...PACK_HEADER_META_LABELS]).not.toContain("Author");
  });

  test("has length 3 (not 4) — Author cell removed in slice 219", () => {
    expect(PACK_HEADER_META_LABELS.length).toBe(3);
  });
});
