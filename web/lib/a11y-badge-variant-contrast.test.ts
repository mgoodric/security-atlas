import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";
import { describe, expect, it } from "vitest";

/**
 * #2199 — WCAG SC 1.4.3 (AA) guard for the tinted Badge variants.
 *
 * Each status variant (`pass`, `info`, `warning`, `critical`, `progress`,
 * plus `destructive`, which shares the shape) renders its hue as text on a
 * translucent tint of the same hue: `bg-<hue>/N text-<hue>`, with a
 * different N under `dark:`. Measured in a browser this was ~3.9:1 before
 * #2097 darkened the light hues. The axe route sweep only sees badges a
 * route happens to render, so this test covers every variant in both
 * themes from the tokens alone.
 *
 * Both inputs are read from source, so a token edit in `app/globals.css`
 * or a tint edit in `components/ui/badge.tsx` is what turns this red:
 * - the hue's `oklch(L C H)` from the `:root` (light) and `.dark` blocks;
 * - the tint percentages from the variant's `bg-<hue>/N` and
 *   `dark:bg-<hue>/N` classes.
 *
 * Tailwind 4 compiles `bg-<hue>/N` to the hue at alpha N%, which the
 * browser composites over the surface underneath in gamma-encoded sRGB.
 * A badge sits on either `--background` or `--card`, so both are checked.
 */

const __dirname = dirname(fileURLToPath(import.meta.url));
const GLOBALS_CSS = readFileSync(
  join(__dirname, "..", "app", "globals.css"),
  "utf8",
);
const BADGE_TSX = readFileSync(
  join(__dirname, "..", "components", "ui", "badge.tsx"),
  "utf8",
);

const AA_NORMAL_TEXT_FLOOR = 4.5;
const VARIANTS = [
  "pass",
  "info",
  "warning",
  "critical",
  "progress",
  "destructive",
] as const;
const SURFACES = ["background", "card"] as const;

type Rgb = [number, number, number];

function block(selector: RegExp): string {
  const m = GLOBALS_CSS.match(selector);
  if (!m) throw new Error(`could not locate ${selector} in globals.css`);
  return m[1];
}

const THEMES = {
  // `:root, .paper {` — the board-pack sheet shares the light set (#2057).
  light: block(/:root\s*(?:,[^{]*)?\{([\s\S]*?)\}/),
  dark: block(/\.dark\s*\{([\s\S]*?)\}/),
};

function readOklch(css: string, token: string): [number, number, number] {
  const m = css.match(
    new RegExp(
      `--${token}\\s*:\\s*oklch\\(\\s*([0-9.]+)\\s+([0-9.]+)\\s+([0-9.]+)\\s*\\)`,
    ),
  );
  if (!m) throw new Error(`could not locate --${token}: oklch(L C H)`);
  return [Number(m[1]), Number(m[2]), Number(m[3])];
}

function gammaEncode(c: number): number {
  const x = Math.min(1, Math.max(0, c));
  return x <= 0.0031308 ? 12.92 * x : 1.055 * Math.pow(x, 1 / 2.4) - 0.055;
}

function linearize(c: number): number {
  return c <= 0.04045 ? c / 12.92 : Math.pow((c + 0.055) / 1.055, 2.4);
}

/** OKLCh -> gamma-encoded sRGB (0..1), clamped to the sRGB gamut. */
function oklchToSrgb([L, C, H]: [number, number, number]): Rgb {
  const h = (H * Math.PI) / 180;
  const a = C * Math.cos(h);
  const b = C * Math.sin(h);
  const l = (L + 0.3963377774 * a + 0.2158037573 * b) ** 3;
  const m = (L - 0.1055613458 * a - 0.0638541728 * b) ** 3;
  const s = (L - 0.0894841775 * a - 1.291485548 * b) ** 3;
  return [
    gammaEncode(4.0767416621 * l - 3.3077115913 * m + 0.2309699292 * s),
    gammaEncode(-1.2684380046 * l + 2.6097574011 * m - 0.3413193965 * s),
    gammaEncode(-0.0041960863 * l - 0.7034186147 * m + 1.707614701 * s),
  ];
}

function luminance([r, g, b]: Rgb): number {
  return 0.2126 * linearize(r) + 0.7152 * linearize(g) + 0.0722 * linearize(b);
}

function contrast(fg: Rgb, bg: Rgb): number {
  const a = luminance(fg);
  const b = luminance(bg);
  return (Math.max(a, b) + 0.05) / (Math.min(a, b) + 0.05);
}

/** `alpha` of `top` composited over an opaque `under`. */
function over(top: Rgb, alpha: number, under: Rgb): Rgb {
  return top.map((c, i) => alpha * c + (1 - alpha) * under[i]) as Rgb;
}

/** The variant's tint alpha per theme, read from its badge.tsx class string. */
function tintAlpha(variant: string): { light: number; dark: number } {
  const line = BADGE_TSX.match(
    new RegExp(`\\b${variant}:\\s*\\n?\\s*"([^"]*)"`),
  );
  if (!line) throw new Error(`no ${variant} variant in badge.tsx`);
  const classes = line[1].split(/\s+/);
  const pct = (prefix: string) => {
    const c = classes.find((x) =>
      new RegExp(`^${prefix}bg-${variant}/\\d+$`).test(x),
    );
    if (!c) throw new Error(`${variant}: no ${prefix}bg-${variant}/N class`);
    return Number(c.split("/")[1]) / 100;
  };
  if (!classes.includes(`text-${variant}`))
    throw new Error(`${variant}: text is not text-${variant}`);
  const light = pct("");
  return { light, dark: pct("dark:") };
}

describe("a11y: Badge variant text on its own tint (WCAG 1.4.3 AA, #2199)", () => {
  it("validates the OKLCh conversion against a known value (oklch(0.628 0.2577 29.23) = #ff0000)", () => {
    const [r, g, b] = oklchToSrgb([0.628, 0.2577, 29.23]);
    expect(r).toBeCloseTo(1, 2);
    expect(g).toBeCloseTo(0, 2);
    expect(b).toBeCloseTo(0, 2);
  });

  for (const theme of ["light", "dark"] as const) {
    for (const variant of VARIANTS) {
      for (const surface of SURFACES) {
        it(`${theme}: ${variant} on its tint over --${surface} >= ${AA_NORMAL_TEXT_FLOOR}:1`, () => {
          const css = THEMES[theme];
          const hue = oklchToSrgb(readOklch(css, variant));
          const under = oklchToSrgb(readOklch(css, surface));
          const tint = over(hue, tintAlpha(variant)[theme], under);
          expect(contrast(hue, tint)).toBeGreaterThanOrEqual(
            AA_NORMAL_TEXT_FLOOR,
          );
        });
      }
    }
  }
});
