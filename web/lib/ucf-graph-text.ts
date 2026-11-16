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

// Average advance width per character, in em, rounded up.
export const CHAR_EM = {
  sans: 0.6,
  sansBold: 0.66,
  mono: 0.62,
} as const;

export type CharEm = (typeof CHAR_EM)[keyof typeof CHAR_EM];

// estimateTextWidth returns the estimated rendered width of `text`, in
// SVG user units, at `fontSize` with the given per-character em factor.
export function estimateTextWidth(
  text: string,
  fontSize: number,
  charEm: CharEm,
): number {
  return Array.from(text).length * fontSize * charEm;
}

// wrapText splits `text` into lines no wider than `maxWidth`. Words are
// kept whole where they fit; a single word wider than a line is broken
// across lines. Every non-whitespace character of the input appears in
// the output, in order: nothing is truncated.
export function wrapText(
  text: string,
  maxWidth: number,
  fontSize: number,
  charEm: CharEm,
): string[] {
  const perLine = Math.max(1, Math.floor(maxWidth / (fontSize * charEm)));
  const words = text.trim().split(/\s+/).filter(Boolean);
  if (words.length === 0) return [""];

  const lines: string[] = [];
  let current = "";
  for (const word of words) {
    let rest = Array.from(word);
    const candidate = current ? `${current} ${word}` : word;
    if (Array.from(candidate).length <= perLine) {
      current = candidate;
      continue;
    }
    if (current) {
      lines.push(current);
      current = "";
    }
    while (rest.length > perLine) {
      lines.push(rest.slice(0, perLine).join(""));
      rest = rest.slice(perLine);
    }
    current = rest.join("");
  }
  if (current) lines.push(current);
  return lines;
}
