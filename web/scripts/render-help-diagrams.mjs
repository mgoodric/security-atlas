// #2148 — pre-render the lifecycle diagrams in module help to SVG.
//
//   just help-diagrams            (or: cd web && node scripts/render-help-diagrams.mjs)
//
// For every ```mermaid block in web/help/modules/*.md (see
// lib/help/lifecycle-diagram.ts), this writes a light and a dark SVG to
// public/help/diagrams/, themed from the app's tokens in app/globals.css.
// The SVGs are committed; the panel serves them as images, so no diagram
// library ships to the browser. A file whose stamp already matches is left
// alone, so a run with nothing changed writes nothing.
//
// Mermaid (a pinned devDependency) runs inside the Chromium that
// @playwright/test already installs for the e2e suite (`npx playwright
// install chromium`); there is no second browser download.
//
// `diagrams.test.ts` fails when a committed SVG's stamp does not match its
// source, so a diagram edited without a re-render cannot merge.
//
// Node runs the two .ts modules directly (type stripping, Node >= 22.18).

import { mkdir, readFile, readdir, unlink, writeFile } from "node:fs/promises";
import { createRequire } from "node:module";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

import { chromium } from "@playwright/test";

import {
  diagramStamp,
  MERMAID_THEME_MAP,
  readStamp,
  readThemeTokens,
  sanitizeSvg,
  withStamp,
} from "../lib/help/diagram-build.ts";
import {
  extractLifecycleBlocks,
  lifecycleDiagramFile,
} from "../lib/help/lifecycle-diagram.ts";

const WEB = join(dirname(fileURLToPath(import.meta.url)), "..");
const MODULES = join(WEB, "help", "modules");
const OUT = join(WEB, "public", "help", "diagrams");
const require = createRequire(import.meta.url);
const MERMAID_JS = require.resolve("mermaid/dist/mermaid.min.js");
const MERMAID_VERSION = JSON.parse(
  await readFile(require.resolve("mermaid/package.json"), "utf8"),
).version;
const FONT =
  "ui-sans-serif, system-ui, -apple-system, 'Segoe UI', Roboto, Helvetica, Arial, sans-serif";

function body(source) {
  const lines = source.replace(/\r\n/g, "\n").split("\n");
  const end = lines.indexOf("---", 1);
  if (lines[0] !== "---" || end < 0) throw new Error("no front matter");
  return lines.slice(end + 1).join("\n");
}

const tokens = readThemeTokens(
  await readFile(join(WEB, "app", "globals.css"), "utf8"),
);

const jobs = [];
for (const file of (await readdir(MODULES))
  .filter((f) => f.endsWith(".md"))
  .sort()) {
  const id = file.slice(0, -3);
  const { blocks } = extractLifecycleBlocks(
    body(await readFile(join(MODULES, file), "utf8")),
  );
  for (const block of blocks) {
    for (const theme of ["light", "dark"]) {
      jobs.push({
        from: `web/help/modules/${file}`,
        out: lifecycleDiagramFile(id, block.lifecycle, theme),
        theme,
        source: block.source,
        stamp: diagramStamp({
          mermaidVersion: MERMAID_VERSION,
          theme,
          tokens: tokens[theme],
          source: block.source,
        }),
      });
    }
  }
}

// A rendered file no longer drawn by any help file is removed.
await mkdir(OUT, { recursive: true });
const wanted = new Set(jobs.map((j) => j.out));
for (const f of await readdir(OUT).catch(() => [])) {
  if (/-(light|dark)\.svg$/.test(f) && !wanted.has(f)) {
    await unlink(join(OUT, f));
    console.log(`removed ${f}`);
  }
}

const stale = [];
for (const job of jobs) {
  const current = await readFile(join(OUT, job.out), "utf8").catch(() => "");
  if (readStamp(current) !== job.stamp) stale.push(job);
}
if (stale.length === 0) {
  console.log(`${jobs.length} diagram file(s) up to date`);
  process.exit(0);
}

const browser = await chromium.launch();
try {
  const page = await browser.newPage();
  await page.setContent("<!doctype html><html><body></body></html>");
  await page.addScriptTag({ path: MERMAID_JS });
  for (const job of stale) {
    const svg = await page.evaluate(
      async ({ source, map, tokens, font }) => {
        // Resolve each token (oklch, maybe with alpha) to an opaque hex
        // colour over the theme background: Mermaid's colour maths only
        // reads hex, rgb and hsl.
        const canvas = document.createElement("canvas");
        canvas.width = canvas.height = 1;
        const ctx = canvas.getContext("2d", { willReadFrequently: true });
        const hex = (color) => {
          ctx.clearRect(0, 0, 1, 1);
          ctx.fillStyle = tokens.background;
          ctx.fillRect(0, 0, 1, 1);
          ctx.fillStyle = color;
          ctx.fillRect(0, 0, 1, 1);
          const [r, g, b] = ctx.getImageData(0, 0, 1, 1).data;
          return `#${[r, g, b]
            .map((n) => n.toString(16).padStart(2, "0"))
            .join("")}`;
        };
        const themeVariables = { fontFamily: font };
        for (const [variable, token] of Object.entries(map)) {
          themeVariables[variable] = hex(tokens[token]);
        }
        // The page global the script tag above defined.
        const mermaid = globalThis.mermaid;
        mermaid.initialize({
          startOnLoad: false,
          securityLevel: "strict",
          theme: "base",
          themeVariables,
          fontFamily: font,
          htmlLabels: false,
          state: { htmlLabels: false },
          flowchart: { htmlLabels: false },
        });
        const { svg } = await mermaid.render("lifecycle", source);
        return svg;
      },
      {
        source: job.source,
        map: MERMAID_THEME_MAP,
        tokens: tokens[job.theme],
        font: FONT,
      },
    );
    await writeFile(
      join(OUT, job.out),
      withStamp(sanitizeSvg(svg), job.from, job.stamp),
    );
    console.log(`rendered ${job.out}`);
  }
} finally {
  await browser.close();
}
