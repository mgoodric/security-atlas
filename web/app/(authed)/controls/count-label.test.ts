// Slice 666 — unit tests for the controls-list header count label.
//
// The label is the copy/semantics fix for ATLAS-007: the header must no
// longer use the verb "Showing" (which collided with the pagination
// footer's "Showing M–N of TOTAL"), and it must read consistently with
// that footer. These tests pin:
//
//   - the verb "Showing" never appears in the header label (AC-1/AC-2);
//   - the unfiltered case is a plain total ("53 SCF anchors");
//   - the filtered case is "N of M SCF anchors", with the filtered
//     count first (AC-3 — the filtered total drives the header);
//   - defensive clamping so the label can never read "60 of 53" or a
//     negative count.

import { describe, expect, it } from "vitest";

import { controlsCountLabel, SCF_ANCHOR_NOUN } from "./count-label";

describe("controlsCountLabel", () => {
  it("renders a plain total when nothing is filtered (filtered === total)", () => {
    const label = controlsCountLabel(53, 53);
    expect(label.text).toBe("53 SCF anchors");
    expect(label.isFiltered).toBe(false);
    expect(label.filtered).toBe(53);
    expect(label.total).toBe(53);
  });

  it("renders 'N of M' when a filter narrows the set (AC-3)", () => {
    const label = controlsCountLabel(42, 53);
    expect(label.text).toBe("42 of 53 SCF anchors");
    expect(label.isFiltered).toBe(true);
    expect(label.filtered).toBe(42);
    expect(label.total).toBe(53);
  });

  it("never uses the verb 'Showing' (AC-1/AC-2 — no footer collision)", () => {
    // The footer owns "Showing M–N of TOTAL"; the header is a count.
    expect(controlsCountLabel(42, 53).text).not.toContain("Showing");
    expect(controlsCountLabel(53, 53).text).not.toContain("Showing");
    expect(controlsCountLabel(0, 53).text).not.toContain("Showing");
  });

  it("handles the all-filtered-out case honestly (0 of M)", () => {
    const label = controlsCountLabel(0, 53);
    expect(label.text).toBe("0 of 53 SCF anchors");
    expect(label.isFiltered).toBe(true);
  });

  it("renders an empty catalog as '0 SCF anchors' (not filtered)", () => {
    const label = controlsCountLabel(0, 0);
    expect(label.text).toBe("0 SCF anchors");
    expect(label.isFiltered).toBe(false);
  });

  it("clamps a filtered count above the total down to the total", () => {
    // The page never produces filtered > total (visible ⊆ rows), but the
    // label must be defensive so it can never read "60 of 53".
    const label = controlsCountLabel(60, 53);
    expect(label.text).toBe("53 SCF anchors");
    expect(label.isFiltered).toBe(false);
    expect(label.filtered).toBe(53);
  });

  it("clamps negative inputs to zero", () => {
    expect(controlsCountLabel(-5, -3).text).toBe("0 SCF anchors");
    expect(controlsCountLabel(-1, 10).text).toBe("0 of 10 SCF anchors");
  });

  it("floors fractional inputs", () => {
    const label = controlsCountLabel(41.9, 53.4);
    expect(label.filtered).toBe(41);
    expect(label.total).toBe(53);
    expect(label.text).toBe("41 of 53 SCF anchors");
  });

  it("exposes the shared noun constant", () => {
    expect(SCF_ANCHOR_NOUN).toBe("SCF anchors");
  });
});
