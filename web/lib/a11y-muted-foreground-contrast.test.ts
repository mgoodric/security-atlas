import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";
import { describe, expect, it } from "vitest";

/**
 * Slice 360 (closes slice 331 A11Y-2) — WCAG SC 1.4.3 (AA) regression guard.
 *
 * The light-mode `--muted-foreground` token is the foreground for 300+
 * `text-muted-foreground` usages (subtitles, table secondary content,
 * breadcrumbs, "Showing N of M" meta lines, form descriptions). It MUST
 * keep a >=4.5:1 contrast ratio against the light-mode `--background`.
 *
 * This test parses the two tokens straight out of `app/globals.css`,
 * converts the achromatic OKLCh values through the same OKLab -> linear
 * sRGB -> sRGB-gamma pipeline a browser uses, then computes the WCAG
 * relative-luminance contrast ratio. If a future edit lowers the token
 * back under the AA floor, this goes red.
 *
 * Math is validated against the canonical AA boundary gray: #767676 on
 * white = 4.54:1 (the well-known WCAG normal-text boundary).
 */

const __dirname = dirname(fileURLToPath(import.meta.url));
const GLOBALS_CSS = join(__dirname, "..", "app", "globals.css");

const AA_NORMAL_TEXT_FLOOR = 4.5;

/** Parse `oklch(L C H ...)` lightness from a `--token: oklch(...)` line in :root. */
function readRootOklchLightness(css: string, token: string): number {
  // grab the :root { ... } block (first occurrence — light mode)
  const rootMatch = css.match(/:root\s*\{([\s\S]*?)\}/);
  if (!rootMatch)
    throw new Error("could not locate :root block in globals.css");
  const rootBlock = rootMatch[1];
  const tokenRe = new RegExp(`--${token}\\s*:\\s*oklch\\(\\s*([0-9.]+)`, "m");
  const m = rootBlock.match(tokenRe);
  if (!m) throw new Error(`could not locate --${token} oklch() in :root block`);
  return Number.parseFloat(m[1]);
}

/** Achromatic OKLab (a=b=0) lightness -> linear sRGB triple. */
function oklabLightnessToLinearSrgb(L: number): [number, number, number] {
  const l = L ** 3;
  const m = L ** 3;
  const s = L ** 3;
  const r = 4.0767416621 * l - 3.3077115913 * m + 0.2309699292 * s;
  const g = -1.2684380046 * l + 2.6097574011 * m - 0.3413193965 * s;
  const b = -0.0041960863 * l - 0.7034186147 * m + 1.707614701 * s;
  return [r, g, b];
}

/** linear sRGB channel -> gamma-encoded sRGB (0..1), clamped. */
function gammaEncode(c: number): number {
  const x = Math.min(1, Math.max(0, c));
  return x <= 0.0031308 ? 12.92 * x : 1.055 * Math.pow(x, 1 / 2.4) - 0.055;
}

/** gamma-encoded sRGB channel -> linear, per WCAG. */
function wcagLinearize(srgb: number): number {
  return srgb <= 0.04045 ? srgb / 12.92 : Math.pow((srgb + 0.055) / 1.055, 2.4);
}

function relativeLuminanceFromOklchLightness(L: number): number {
  const [r, g, b] = oklabLightnessToLinearSrgb(L);
  const sr = gammaEncode(r);
  const sg = gammaEncode(g);
  const sb = gammaEncode(b);
  return (
    0.2126 * wcagLinearize(sr) +
    0.7152 * wcagLinearize(sg) +
    0.0722 * wcagLinearize(sb)
  );
}

function contrastRatio(l1: number, l2: number): number {
  const hi = Math.max(l1, l2);
  const lo = Math.min(l1, l2);
  return (hi + 0.05) / (lo + 0.05);
}

/** WCAG contrast for a #rrggbb hex on a given luminance — used for the validation anchor. */
function hexLuminance(hex: string): number {
  const r = parseInt(hex.slice(0, 2), 16) / 255;
  const g = parseInt(hex.slice(2, 4), 16) / 255;
  const b = parseInt(hex.slice(4, 6), 16) / 255;
  return (
    0.2126 * wcagLinearize(r) +
    0.7152 * wcagLinearize(g) +
    0.0722 * wcagLinearize(b)
  );
}

describe("a11y: light-mode --muted-foreground contrast (WCAG 1.4.3 AA)", () => {
  const css = readFileSync(GLOBALS_CSS, "utf8");

  it("validates the contrast math against the canonical AA boundary gray (#767676 = 4.54:1)", () => {
    const ratio = contrastRatio(hexLuminance("767676"), hexLuminance("ffffff"));
    expect(ratio).toBeCloseTo(4.54, 1);
  });

  it("keeps --muted-foreground at or above the 4.5:1 AA floor against --background", () => {
    const fgL = readRootOklchLightness(css, "muted-foreground");
    const bgL = readRootOklchLightness(css, "background");
    const ratio = contrastRatio(
      relativeLuminanceFromOklchLightness(fgL),
      relativeLuminanceFromOklchLightness(bgL),
    );
    expect(ratio).toBeGreaterThanOrEqual(AA_NORMAL_TEXT_FLOOR);
  });

  it("keeps the token visibly muted (not driven to near-full foreground)", () => {
    const fgL = readRootOklchLightness(css, "muted-foreground");
    const fgPlainL = readRootOklchLightness(css, "foreground");
    // muted must be lighter (higher OKLCh L) than the full foreground token,
    // otherwise it has stopped being "muted".
    expect(fgL).toBeGreaterThan(fgPlainL);
    // and it should not have collapsed onto full foreground.
    expect(fgL - fgPlainL).toBeGreaterThan(0.15);
  });

  it("does not touch the dark-mode token (P0-360-1)", () => {
    const darkMatch = css.match(/\.dark\s*\{([\s\S]*?)\}/);
    expect(darkMatch).not.toBeNull();
    const darkBlock = darkMatch![1];
    expect(darkBlock).toMatch(/--muted-foreground:\s*oklch\(0\.708 0 0\)/);
  });
});
