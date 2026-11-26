// #2148 — the bundle check finds a Mermaid runtime where there is one.
//
// The CI step (`node scripts/check-no-mermaid-bundle.mjs` after `next
// build`) proves the app bundle is clean; this proves the check would
// notice otherwise, against Mermaid's own dist files.

import { mkdtempSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { createRequire } from "node:module";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { describe, expect, it } from "vitest";

import {
  findMermaidInBundle,
  mermaidSignatures,
} from "./check-no-mermaid-bundle.mjs";

const require = createRequire(import.meta.url);
const dist = dirname(require.resolve("mermaid/dist/mermaid.min.js"));

describe("check-no-mermaid-bundle", () => {
  it.each(["mermaid.min.js", "mermaid.esm.min.mjs", "mermaid.core.mjs"])(
    "detects Mermaid's own %s",
    (file) => {
      expect(
        mermaidSignatures(readFileSync(join(dist, file), "utf8")).length,
      ).toBeGreaterThan(0);
    },
  );

  it("passes help source text that names mermaid", () => {
    expect(
      mermaidSignatures(
        "```mermaid\n%% lifecycle: exception\nstateDiagram-v2\n```",
      ),
    ).toEqual([]);
  });

  it("reports the asset that carries the runtime", async () => {
    const root = mkdtempSync(join(tmpdir(), "bundle-"));
    mkdirSync(join(root, "chunks"));
    writeFileSync(join(root, "chunks", "clean.js"), "console.log(1)");
    writeFileSync(
      join(root, "chunks", "dirty.js"),
      "x.registerExternalDiagrams=1",
    );
    const { scanned, hits } = await findMermaidInBundle(root);
    expect(scanned).toBe(2);
    expect(hits).toEqual([
      {
        path: join(root, "chunks", "dirty.js"),
        found: ["registerExternalDiagrams"],
      },
    ]);
  });
});
