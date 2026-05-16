#!/usr/bin/env node
// scripts/regen-logo-variants.mjs
//
// Slice 075 — Logo integration. Deterministic generator for every
// derived logo asset across the six integration surfaces.
//
// Source of truth (light-palette SVG):
//   docs/design/logo-candidates/candidate-04/mark.svg
//
// Favicon-simplified source (slice 075 D4 — favicon-scale variant):
//   docs/design/logo-candidates/candidate-04/mark-favicon.svg
//
// What this script produces (idempotent — re-running overwrites in place):
//
//   web/public/logo-light.svg                 light-palette canonical SVG
//   web/public/logo-dark.svg                  dark-palette  canonical SVG
//   web/public/logo-light.png                 256x256 raster (light)
//   web/public/logo-dark.png                  256x256 raster (dark)
//   web/public/apple-touch-icon.png           180x180 (full mark, light bg)
//   web/public/icon-192.png                   192x192 (full mark, light bg)
//   web/public/icon-512.png                   512x512 (full mark, light bg)
//   web/public/favicon.ico                    multi-resolution 16/32/48
//                                             (simplified favicon variant)
//   web/public/og-image.png                   1200x630 Open Graph preview
//   web/public/twitter-card.png               1200x675 Twitter summary_large_image
//   docs-site/docs/assets/logo-light.svg      mkdocs theme.logo (light)
//   docs-site/docs/assets/logo-dark.svg       mkdocs theme.logo (dark)
//   docs-site/docs/assets/favicon.png         mkdocs theme.favicon (32x32)
//   docs/images/logo-light.png                README hero <picture> (light)
//   docs/images/logo-dark.png                 README hero <picture> (dark)
//
// Toolchain (slice 075 D2):
//   - Sharp (transitive of next@^16; resolves from repo-root node_modules
//     via npm workspace hoisting). NO new npm dep added (P0 of AC-10).
//   - Hand-rolled ICO encoder (Node Buffer only) so favicon.ico ships
//     without adding `png-to-ico` or `to-ico` (P0 of AC-10).
//   - Plain ESM (.mjs) — no TypeScript transpile step, no flags. Node
//     >=20 per the web/ engines field. The slice 075 AC-2 names the
//     script `scripts/regen-logo-variants.ts` literally; the .mjs
//     deviation is recorded in docs/audit-log/075-logo-integration-decisions.md
//     (D1) with rationale (zero deps + no Node experimental flags).
//
// Light → dark palette mapping (matches LIGHT_TO_DARK_V6 in
// tools/logo-gen/recolor_by_weight.py, the slice-074 helper kept in the
// repo for contrast.py verification value):
//
//   #9f1239 (rose-800)    → #f2a2b3 (pink)         apex roof
//   #be185d (pink-700)    → #f9c3c3 (pale pink)    apex tangent
//   #9a3412 (orange-800)  → #f7d4c0 (peach)        outrigger braces
//   #854d0e (amber-700)   → #f9e6c1 (cream)        A legs
//   #065f46 (emerald-800) → #d1e7e0 (mint)         crossbars
//   #075985 (sky-800)     → #a0d1e8 (pale sky)     inner diagonals
//   #0369a1 (sky-700)     → #7ab8e1 (medium sky)   CROSS_MID→base
//   #1e40af (blue-800)    → #4b8db5 (deep blue)    base + braces + nodes
//
// Usage:
//   just regen-logo
// or:
//   node scripts/regen-logo-variants.mjs

import { mkdirSync, readFileSync, writeFileSync, statSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { createRequire } from "node:module";

const __dirname = dirname(fileURLToPath(import.meta.url));
const repoRoot = resolve(__dirname, "..");
const require = createRequire(import.meta.url);
const sharp = require("sharp");

// Canonical source paths.
const CANONICAL_SVG = join(
  repoRoot,
  "docs/design/logo-candidates/candidate-04/mark.svg",
);
const FAVICON_SVG = join(
  repoRoot,
  "docs/design/logo-candidates/candidate-04/mark-favicon.svg",
);

// Light-to-dark palette swap (V6 — see file header).
const LIGHT_TO_DARK = {
  "#9f1239": "#f2a2b3",
  "#be185d": "#f9c3c3",
  "#9a3412": "#f7d4c0",
  "#854d0e": "#f9e6c1",
  "#065f46": "#d1e7e0",
  "#075985": "#a0d1e8",
  "#0369a1": "#7ab8e1",
  "#1e40af": "#4b8db5",
};

/**
 * Token-swap hex colors in SVG source. Case-insensitive on source,
 * emits the mapping value verbatim. Mirrors the Python helper at
 * tools/logo-gen/recolor_by_weight.py:swap_palette.
 */
function swapPalette(svgText, mapping) {
  let out = svgText;
  for (const [src, dst] of Object.entries(mapping)) {
    out = out.replaceAll(src, dst);
    out = out.replaceAll(src.toUpperCase(), dst);
  }
  return out;
}

/**
 * Build a multi-resolution ICO file from N PNG buffers.
 *
 * ICO format (https://en.wikipedia.org/wiki/ICO_(file_format)):
 *   - 6-byte ICONDIR header
 *   - N x 16-byte ICONDIRENTRY directory entries
 *   - N x raw PNG payloads
 *
 * Modern ICO loaders accept embedded PNGs (Vista+, every browser today).
 * Hand-rolled to avoid adding a png-to-ico npm dependency (P0 of AC-10).
 */
function buildIco(pngBuffers) {
  // ICONDIR (6 bytes): reserved=0, type=1 (icon), count=N
  const header = Buffer.alloc(6);
  header.writeUInt16LE(0, 0);
  header.writeUInt16LE(1, 2);
  header.writeUInt16LE(pngBuffers.length, 4);

  const entrySize = 16;
  let dataOffset = 6 + entrySize * pngBuffers.length;

  const entries = [];
  for (const { size, buf } of pngBuffers) {
    const entry = Buffer.alloc(entrySize);
    // width / height: 0 means 256 in ICO. For 16/32/48 we write the size literally.
    entry.writeUInt8(size === 256 ? 0 : size, 0); // width
    entry.writeUInt8(size === 256 ? 0 : size, 1); // height
    entry.writeUInt8(0, 2); // color palette count (0 = no palette)
    entry.writeUInt8(0, 3); // reserved
    entry.writeUInt16LE(1, 4); // color planes
    entry.writeUInt16LE(32, 6); // bits per pixel (PNG carries its own; declare 32)
    entry.writeUInt32LE(buf.length, 8); // image data size
    entry.writeUInt32LE(dataOffset, 12); // offset to PNG payload
    entries.push(entry);
    dataOffset += buf.length;
  }

  return Buffer.concat([header, ...entries, ...pngBuffers.map((p) => p.buf)]);
}

/** Render an SVG byte-string to a PNG Buffer at NxN pixels. */
async function renderPng(svgBytes, size, background = null) {
  let img = sharp(svgBytes, { density: 384 }).resize(size, size, {
    fit: "contain",
    background: { r: 0, g: 0, b: 0, alpha: 0 },
  });
  if (background) {
    img = img.flatten({ background });
  }
  return img.png({ compressionLevel: 9 }).toBuffer();
}

/** Build the social-card SVG template (Open Graph / Twitter summary_large_image). */
function socialCardSvg({ width, height, logoSvgB64, theme }) {
  // Theme presets — match the canonical light + dark mark variants.
  // OG cards are picked once by the scraper at unfurl time; we ship the
  // LIGHT-themed card as both og-image.png and twitter-card.png so the
  // scraper renders consistently regardless of viewer preference.
  const bg = theme === "dark" ? "#0a0a0a" : "#fafafa";
  const titleColor = theme === "dark" ? "#fafafa" : "#0a0a0a";
  const taglineColor = theme === "dark" ? "#a1a1aa" : "#52525b"; // zinc-400 / zinc-600
  const accentColor = theme === "dark" ? "#4b8db5" : "#1e40af"; // node anchor color per variant

  // Logo box: 240x240, top-left at (80, (height - 240) / 2)
  const logoSize = 240;
  const logoX = 80;
  const logoY = Math.round((height - logoSize) / 2);

  // Text block: starts to the right of the logo, vertically aligned to center.
  // The text uses font-family with system fallback chain (Inter → system-ui →
  // sans-serif). Sharp + librsvg use fontconfig to resolve font-family at
  // render time. Linux CI provisions a fontconfig sans-serif fallback;
  // macOS dev environments resolve Inter via system fonts. This is
  // recorded as a revisit item in docs/audit-log/075-logo-integration-decisions.md.
  const textX = logoX + logoSize + 56;
  const titleY = Math.round(height / 2 - 28);
  const taglineY = Math.round(height / 2 + 32);
  const ruleY = titleY - 56;

  // Accent rule under the title — anchors the typography to the mark color.
  const ruleWidth = 96;

  return `<?xml version="1.0" encoding="UTF-8"?>
<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink" viewBox="0 0 ${width} ${height}" width="${width}" height="${height}">
  <rect width="${width}" height="${height}" fill="${bg}"/>
  <rect x="${textX}" y="${ruleY}" width="${ruleWidth}" height="6" fill="${accentColor}" rx="3"/>
  <image x="${logoX}" y="${logoY}" width="${logoSize}" height="${logoSize}" xlink:href="data:image/svg+xml;base64,${logoSvgB64}"/>
  <text x="${textX}" y="${titleY}" font-family="Inter, 'Helvetica Neue', Helvetica, Arial, system-ui, sans-serif" font-size="76" font-weight="700" fill="${titleColor}">security-atlas</text>
  <text x="${textX}" y="${taglineY}" font-family="Inter, 'Helvetica Neue', Helvetica, Arial, system-ui, sans-serif" font-size="32" font-weight="400" fill="${taglineColor}">Open-source GRC — one control graph, every framework.</text>
</svg>
`;
}

function ensureDir(path) {
  mkdirSync(dirname(path), { recursive: true });
}

function writeAsset(path, data) {
  ensureDir(path);
  writeFileSync(path, data);
  const kb = statSync(path).size / 1024;
  const rel = path.replace(repoRoot + "/", "");
  console.log(`  wrote ${rel}  ${kb.toFixed(1)} KB`);
}

// readSource reads a required SVG source file, exiting with a friendly error
// if missing. Single-step (no separate existsSync/statSync pre-check) to avoid
// the TOCTOU pattern CodeQL's js/file-system-race flags.
function readSource(path, label) {
  try {
    return readFileSync(path, "utf-8");
  } catch (err) {
    if (err.code === "ENOENT") {
      console.error(`error: ${label} not found at ${path}`);
      process.exit(2);
    }
    throw err;
  }
}

async function main() {
  console.log("Reading canonical SVG sources...");
  const lightSvg = readSource(CANONICAL_SVG, "canonical SVG");
  const darkSvg = swapPalette(lightSvg, LIGHT_TO_DARK);
  const faviconLightSvg = readSource(FAVICON_SVG, "favicon-simplified SVG");
  // Favicons don't ship a dark variant — they render against tab/dock
  // chrome which varies; the simplified single-color mark works against both.

  // ---------- 1. SVG variants ----------
  console.log("\nVariant SVGs:");
  const lightSvgBytes = Buffer.from(lightSvg, "utf-8");
  const darkSvgBytes = Buffer.from(darkSvg, "utf-8");

  writeAsset(join(repoRoot, "web/public/logo-light.svg"), lightSvg);
  writeAsset(join(repoRoot, "web/public/logo-dark.svg"), darkSvg);
  writeAsset(join(repoRoot, "docs-site/docs/assets/logo-light.svg"), lightSvg);
  writeAsset(join(repoRoot, "docs-site/docs/assets/logo-dark.svg"), darkSvg);

  // ---------- 2. Web PNG variants (256x256 — for <picture> fallback / OG-less surfaces) ----------
  console.log("\n256x256 PNG variants:");
  const logoLight256 = await renderPng(lightSvgBytes, 256);
  const logoDark256 = await renderPng(darkSvgBytes, 256);
  writeAsset(join(repoRoot, "web/public/logo-light.png"), logoLight256);
  writeAsset(join(repoRoot, "web/public/logo-dark.png"), logoDark256);

  // ---------- 3. README hero PNGs ----------
  console.log("\nREADME hero PNGs (docs/images/):");
  writeAsset(join(repoRoot, "docs/images/logo-light.png"), logoLight256);
  writeAsset(join(repoRoot, "docs/images/logo-dark.png"), logoDark256);

  // ---------- 4. Favicon set ----------
  console.log("\nFavicon set (web/public/):");

  // favicon.ico — multi-resolution, uses the SIMPLIFIED favicon variant
  // at 16/32/48. Flattened onto a transparent bg (favicon viewers
  // composite onto browser chrome).
  const faviconSizes = [16, 32, 48];
  const faviconBuffers = await Promise.all(
    faviconSizes.map(async (size) => ({
      size,
      buf: await renderPng(Buffer.from(faviconLightSvg, "utf-8"), size),
    })),
  );
  const icoBuf = buildIco(faviconBuffers);
  writeAsset(join(repoRoot, "web/public/favicon.ico"), icoBuf);

  // mkdocs theme.favicon is rendered at small chrome size; reuse the
  // 32px simplified variant as a PNG.
  writeAsset(
    join(repoRoot, "docs-site/docs/assets/favicon.png"),
    faviconBuffers[1].buf,
  );

  // apple-touch + PWA icons — use the FULL mark (these render at 180 / 192 /
  // 512 px where the 16-line gradient reads cleanly). Light bg variant
  // because apple-touch + PWA chrome historically assume light bg.
  console.log("\nFull-mark raster targets:");
  const appleTouch = await renderPng(lightSvgBytes, 180, {
    r: 0xfa,
    g: 0xfa,
    b: 0xfa,
    alpha: 1,
  });
  const icon192 = await renderPng(lightSvgBytes, 192, {
    r: 0xfa,
    g: 0xfa,
    b: 0xfa,
    alpha: 1,
  });
  const icon512 = await renderPng(lightSvgBytes, 512, {
    r: 0xfa,
    g: 0xfa,
    b: 0xfa,
    alpha: 1,
  });
  writeAsset(join(repoRoot, "web/public/apple-touch-icon.png"), appleTouch);
  writeAsset(join(repoRoot, "web/public/icon-192.png"), icon192);
  writeAsset(join(repoRoot, "web/public/icon-512.png"), icon512);

  // ---------- 5. Social-share cards ----------
  console.log("\nSocial-share cards:");
  // Embed the light-variant SVG into the card template as a base64
  // data URI so Sharp's librsvg backend doesn't have to follow file
  // references at render time (deterministic + offline).
  const lightSvgB64 = lightSvgBytes.toString("base64");

  const ogSvg = socialCardSvg({
    width: 1200,
    height: 630,
    logoSvgB64: lightSvgB64,
    theme: "light",
  });
  const ogPng = await sharp(Buffer.from(ogSvg, "utf-8"), { density: 96 })
    .resize(1200, 630)
    .png({ compressionLevel: 9, palette: true })
    .toBuffer();
  writeAsset(join(repoRoot, "web/public/og-image.png"), ogPng);

  const twitterSvg = socialCardSvg({
    width: 1200,
    height: 675,
    logoSvgB64: lightSvgB64,
    theme: "light",
  });
  const twitterPng = await sharp(Buffer.from(twitterSvg, "utf-8"), {
    density: 96,
  })
    .resize(1200, 675)
    .png({ compressionLevel: 9, palette: true })
    .toBuffer();
  writeAsset(join(repoRoot, "web/public/twitter-card.png"), twitterPng);

  // ---------- 6. Summary ----------
  console.log("\nDone.");
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});
