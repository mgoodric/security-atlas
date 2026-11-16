// #2148 — the browser bundle carries no Mermaid runtime.
//
//   cd web && npm run build && node scripts/check-no-mermaid-bundle.mjs
//
// Lifecycle diagrams are pre-rendered to SVG at build time
// (scripts/render-help-diagrams.mjs), so Mermaid is a devDependency only
// and nothing in the app imports it. This walks every client asset `next
// build` wrote under .next/static and fails when one carries a Mermaid
// runtime signature: strings from Mermaid's public API and its error
// renderer, which survive minification in every Mermaid build (the IIFE,
// the ESM entry and its chunks). The help files' Mermaid *source* does
// reach the bundle as text, so the word "mermaid" alone is not a signal.
//
// `check-no-mermaid-bundle.test.ts` proves the signatures match Mermaid's
// own dist files, so a Mermaid upgrade that drops them fails there first.

import { readdir, readFile } from "node:fs/promises";
import { dirname, join, relative } from "node:path";
import { fileURLToPath } from "node:url";

export const MERMAID_SIGNATURES = [
  "registerExternalDiagrams",
  "Syntax error in text",
];

/** The Mermaid runtime signatures `text` carries; empty when none. */
export function mermaidSignatures(text) {
  return MERMAID_SIGNATURES.filter((s) => text.includes(s));
}

async function* files(dir) {
  for (const entry of await readdir(dir, { withFileTypes: true })) {
    const path = join(dir, entry.name);
    if (entry.isDirectory()) yield* files(path);
    else if (/\.(m?js|css|html)$/.test(entry.name)) yield path;
  }
}

/** Every client asset under `staticDir` that carries a signature. */
export async function findMermaidInBundle(staticDir) {
  const hits = [];
  let scanned = 0;
  for await (const path of files(staticDir)) {
    scanned += 1;
    const found = mermaidSignatures(await readFile(path, "utf8"));
    if (found.length > 0) hits.push({ path, found });
  }
  return { scanned, hits };
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  const web = join(dirname(fileURLToPath(import.meta.url)), "..");
  const staticDir = join(web, ".next", "static");
  const { scanned, hits } = await findMermaidInBundle(staticDir).catch(
    (err) => {
      console.error(
        `no client bundle at ${staticDir}; run \`npm run build\` first (${err.message})`,
      );
      process.exit(2);
    },
  );
  if (scanned === 0) {
    console.error(`no client assets under ${staticDir}`);
    process.exit(2);
  }
  if (hits.length > 0) {
    for (const h of hits) {
      console.error(`${relative(web, h.path)}: ${h.found.join(", ")}`);
    }
    console.error("the browser bundle carries a Mermaid runtime (#2148)");
    process.exit(1);
  }
  console.log(`no Mermaid runtime in ${scanned} client assets`);
}
