import { describe, expect, it } from "vitest";

import {
  CHAR_EM,
  type CharEm,
  UPPER_SCALE,
  estimateTextWidth,
  graphemes,
  graphemesFallback,
  wrapText,
} from "./ucf-graph-text";

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

// #2374 — grapheme clusters and wide glyphs.
const FAMILY = "\u{1F468}‍\u{1F469}‍\u{1F467}";
const E_ACUTE = "é"; // "é", decomposed: two code points

describe("estimateTextWidth (#2374)", () => {
  it("weights CJK and fullwidth characters at a full em", () => {
    expect(estimateTextWidth("情報セキュリティ", 10, CHAR_EM.sans)).toBe(80);
    expect(estimateTextWidth("ＡＢＣ", 10, CHAR_EM.mono)).toBe(30);
    expect(estimateTextWidth("abc", 10, CHAR_EM.sans)).toBeCloseTo(18);
  });

  it("counts a base with its combining mark as one glyph", () => {
    const word = `${E_ACUTE}t${E_ACUTE}`;
    expect(Array.from(word)).toHaveLength(5);
    expect(estimateTextWidth(word, 10, CHAR_EM.mono)).toBeCloseTo(3 * 6.2);
  });

  it("counts a ZWJ emoji sequence as one wide glyph", () => {
    expect(Array.from(FAMILY)).toHaveLength(5);
    expect(estimateTextWidth(FAMILY, 10, CHAR_EM.sans)).toBe(10);
  });

  it("weights uppercase-heavy bold codes wider than the average glyph", () => {
    expect(estimateTextWidth("MMMM", 10, CHAR_EM.sansBold)).toBeCloseTo(
      4 * 10 * CHAR_EM.sansBold * UPPER_SCALE,
    );
    const code = "NIST SP 800-53 · PS.SCREEN-4";
    expect(estimateTextWidth(code, 10, CHAR_EM.sansBold)).toBeGreaterThan(
      Array.from(code).length * 10 * CHAR_EM.sansBold,
    );
    // Monospace advances do not depend on case.
    expect(estimateTextWidth("MMMM", 10, CHAR_EM.mono)).toBeCloseTo(
      estimateTextWidth("mmmm", 10, CHAR_EM.mono),
    );
  });
});

describe("wrapText (#2374)", () => {
  const within = (lines: string[], max: number, em: CharEm = CHAR_EM.sans) =>
    lines.every((l) => estimateTextWidth(l, 10, em) <= max);

  it("wraps CJK text at a full em per glyph, keeping every character", () => {
    const text = "個人情報保護方針に基づく従業員の身元調査";
    const lines = wrapText(text, 60, 10, CHAR_EM.sans);
    expect(lines.join("")).toBe(text);
    expect(lines.every((l) => Array.from(l).length <= 6)).toBe(true);
    expect(within(lines, 60)).toBe(true);
  });

  it("never splits a combining mark from its base", () => {
    const word = E_ACUTE.repeat(20);
    const lines = wrapText(word, 30, 10, CHAR_EM.sans);
    expect(lines.length).toBeGreaterThan(1);
    expect(lines.join("")).toBe(word);
    for (const line of lines) expect(line.startsWith("́")).toBe(false);
    expect(within(lines, 30)).toBe(true);
  });

  it("never splits a ZWJ emoji sequence", () => {
    const lines = wrapText(FAMILY.repeat(5), 20, 10, CHAR_EM.sans);
    expect(lines).toEqual([FAMILY + FAMILY, FAMILY + FAMILY, FAMILY]);
  });

  it("keeps a glyph wider than the line rather than dropping it", () => {
    expect(wrapText("情報", 5, 10, CHAR_EM.sans)).toEqual(["情", "報"]);
  });

  it("wraps an uppercase-heavy bold code inside the line", () => {
    const code = "ISO/IEC 27001 · A.5.1.1-PERSONNEL-SCREENING-CONTROLS";
    const lines = wrapText(code, 120, 10, CHAR_EM.sansBold);
    expect(lines.length).toBeGreaterThan(1);
    expect(lines.join("").replace(/\s+/g, "")).toBe(code.replace(/\s+/g, ""));
    expect(within(lines, 120, CHAR_EM.sansBold)).toBe(true);
  });
});

describe("graphemesFallback (#2374)", () => {
  it("clusters marks, ZWJ emoji, skin tones and CJK like Intl.Segmenter", () => {
    const text = `${E_ACUTE} 情${FAMILY}\u{1F44D}\u{1F3FD}a`;
    expect(graphemesFallback(text)).toEqual(graphemes(text));
    expect(graphemesFallback(text)).toHaveLength(6);
  });
});
