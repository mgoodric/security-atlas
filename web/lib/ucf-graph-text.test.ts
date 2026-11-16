import { describe, expect, it } from "vitest";

import { CHAR_EM, estimateTextWidth, wrapText } from "./ucf-graph-text";

describe("wrapText", () => {
  it("keeps short text on one line", () => {
    expect(wrapText("Logical access", 200, 10, CHAR_EM.sans)).toEqual([
      "Logical access",
    ]);
  });

  it("wraps on word boundaries without dropping any word", () => {
    const text =
      "Background checks during hiring for personnel with privileged access";
    const lines = wrapText(text, 120, 10, CHAR_EM.sans);
    expect(lines.length).toBeGreaterThan(1);
    expect(lines.join(" ")).toBe(text);
    for (const line of lines) {
      expect(estimateTextWidth(line, 10, CHAR_EM.sans)).toBeLessThanOrEqual(
        120,
      );
    }
  });

  it("hard-breaks a word longer than a line, keeping every character", () => {
    const word = "x".repeat(50);
    const lines = wrapText(word, 60, 10, CHAR_EM.sans);
    expect(lines.join("")).toBe(word);
    for (const line of lines) {
      expect(estimateTextWidth(line, 10, CHAR_EM.sans)).toBeLessThanOrEqual(60);
    }
  });

  it("returns one empty line for blank input", () => {
    expect(wrapText("   ", 100, 10, CHAR_EM.sans)).toEqual([""]);
  });
});
