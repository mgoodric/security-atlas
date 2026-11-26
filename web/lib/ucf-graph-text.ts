// #2096 — text measurement and wrapping for the hand-rolled UCF graph
// SVG (components/control/ucf-mini-viz.tsx).
//
// SVG <text> does not wrap, and the graph used to `.slice()` labels to
// fit its fixed-size nodes ("Background checks during h"). The graph now
// wraps every label onto as many lines as it needs and sizes each node to
// its lines. Width is estimated from a per-character em factor rather
// than measured in the DOM, so the layout is identical on the server
// render and the client, and deterministic in tests. The factors are
// deliberately generous (wider than the average glyph) so an estimate
// errs toward an extra line, never toward text spilling past a node edge.
//
// #2374 — text is measured per grapheme cluster, not per code point, so
// a combining mark adds no width and a ZWJ emoji sequence counts as one
// glyph; a hard break never splits a cluster. East Asian Wide and
// Fullwidth characters, and emoji, are weighted at a full em, and
// uppercase Latin in a proportional face is weighted wider than the
// average glyph, so uppercase-heavy bold codes still fit their node.

// Average advance width per character, in em, rounded up.
export const CHAR_EM = {
  sans: 0.6,
  sansBold: 0.66,
  mono: 0.62,
} as const;

export type CharEm = (typeof CHAR_EM)[keyof typeof CHAR_EM];

// Advance of a wide glyph (CJK, fullwidth forms, emoji), in em.
export const WIDE_EM = 1.0;
// Uppercase Latin in a proportional face, relative to its average glyph.
export const UPPER_SCALE = 1.2;

// East Asian Width W and F ranges (UAX #11), coarsened to whole blocks.
const WIDE_RE =
  /[ᄀ-ᅟ⺀-〾ぁ-㏿㐀-䶿一-鿿ꀀ-꓏ꥠ-꥿가-힣豈-﫿︐-︙︰-﹯＀-｠￠-￦\u{1F000}-\u{1FAFF}\u{20000}-\u{3FFFD}]/u;
const EMOJI_RE = /\p{Extended_Pictographic}/u;
const UPPER_RE = /^\p{Lu}/u;

// Fallback cluster: a base with its combining marks, variation selectors
// and skin-tone modifiers, plus any ZWJ-joined bases; or a lone run of marks.
const CLUSTER_RE =
  /\P{M}[\p{M}️\u{1F3FB}-\u{1F3FF}]*(?:‍\P{M}[\p{M}️\u{1F3FB}-\u{1F3FF}]*)*|\p{M}+/gu;

// graphemesFallback is the splitter used where Intl.Segmenter is missing.
export function graphemesFallback(text: string): string[] {
  return text.match(CLUSTER_RE) ?? [];
}

const segmenter =
  typeof Intl !== "undefined" && "Segmenter" in Intl
    ? new Intl.Segmenter(undefined, { granularity: "grapheme" })
    : null;

// graphemes splits `text` into user-perceived characters.
export function graphemes(text: string): string[] {
  if (!segmenter) return graphemesFallback(text);
  return Array.from(segmenter.segment(text), (s) => s.segment);
}

// graphemeEm returns the advance of one grapheme cluster, in em.
function graphemeEm(g: string, charEm: CharEm): number {
  if (WIDE_RE.test(g) || EMOJI_RE.test(g)) return Math.max(WIDE_EM, charEm);
  if (charEm !== CHAR_EM.mono && UPPER_RE.test(g)) return charEm * UPPER_SCALE;
  return charEm;
}

function clustersWidth(gs: string[], fontSize: number, charEm: CharEm) {
  return gs.reduce((w, g) => w + graphemeEm(g, charEm), 0) * fontSize;
}

// estimateTextWidth returns the estimated rendered width of `text`, in
// SVG user units, at `fontSize` with the given per-character em factor.
export function estimateTextWidth(
  text: string,
  fontSize: number,
  charEm: CharEm,
): number {
  return clustersWidth(graphemes(text), fontSize, charEm);
}

// wrapText splits `text` into lines no wider than `maxWidth`. Words are
// kept whole where they fit; a single word wider than a line is broken
// across lines between grapheme clusters. Every non-whitespace character
// of the input appears in the output, in order: nothing is truncated. A
// line holds at least one cluster, so a glyph wider than `maxWidth` still
// appears.
export function wrapText(
  text: string,
  maxWidth: number,
  fontSize: number,
  charEm: CharEm,
): string[] {
  const words = text.trim().split(/\s+/).filter(Boolean);
  if (words.length === 0) return [""];
  const fits = (s: string) =>
    estimateTextWidth(s, fontSize, charEm) <= maxWidth;

  const lines: string[] = [];
  let current = "";
  for (const word of words) {
    const candidate = current ? `${current} ${word}` : word;
    if (fits(candidate)) {
      current = candidate;
      continue;
    }
    if (current) {
      lines.push(current);
      current = "";
    }
    let line: string[] = [];
    let lineW = 0;
    for (const g of graphemes(word)) {
      const gW = clustersWidth([g], fontSize, charEm);
      if (line.length > 0 && lineW + gW > maxWidth) {
        lines.push(line.join(""));
        line = [];
        lineW = 0;
      }
      line.push(g);
      lineW += gW;
    }
    current = line.join("");
  }
  if (current) lines.push(current);
  return lines;
}
