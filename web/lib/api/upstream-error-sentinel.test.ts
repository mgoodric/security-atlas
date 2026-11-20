// #2645 sentinel: an error response's text goes through
// `upstreamErrorMessage` / `upstreamErrorText` (lib/api/_shared.ts), which
// pass a 4xx `{error}` through and show generic text for a 5xx. A
// hand-rolled read of the body's `error` field shows a 5xx body too, so a
// handler that wrote `err.Error()` there would leak internals (#2433).
//
// This scans the non-test sources of web/ for the hand-rolled shapes the
// #2645 sweep removed. A new site that needs the body's `error` calls the
// helper instead; there is no allowlist beyond the helper's own file.

import { readdirSync, readFileSync, statSync } from "node:fs";
import path from "node:path";

import { describe, expect, test } from "vitest";

const WEB = path.resolve(__dirname, "../..");
const ROOTS = ["app", "components", "lib"];
const HELPER = path.join("lib", "api", "_shared.ts");

// Each pattern is a way to pull `error` out of a parsed response body.
const PATTERNS: { name: string; re: RegExp }[] = [
  // `(await res.json()) as { error?: string }`, `JSON.parse(t) as { error?: … }`,
  // `(raw as { error?: string }).error`, and the multi-line object forms.
  { name: "cast to { error?: … }", re: /\bas\s*\{[^{}]*\berror\?:/ },
  // `{ items: T[]; error?: string }` intersected onto a body type.
  { name: "& { error?: … }", re: /&\s*\{[^{}]*\berror\?:/ },
  // `body.error ?? `${res.status} …`` and similar status-line fallbacks.
  {
    name: ".error ?? status fallback",
    re: /\.error\s*\?\?\s*`[^`]*\.status\b/,
  },
];

function sources(dir: string): string[] {
  const out: string[] = [];
  for (const name of readdirSync(dir)) {
    if (name === "node_modules" || name === "generated") continue;
    const full = path.join(dir, name);
    if (statSync(full).isDirectory()) {
      out.push(...sources(full));
    } else if (/\.(ts|tsx)$/.test(name) && !/\.test\.tsx?$/.test(name)) {
      out.push(full);
    }
  }
  return out;
}

export function findPassThroughs(
  files: { path: string; text: string }[],
): string[] {
  const hits: string[] = [];
  for (const f of files) {
    if (f.path === HELPER) continue;
    for (const p of PATTERNS) {
      if (p.re.test(f.text)) hits.push(`${f.path}: ${p.name}`);
    }
  }
  return hits;
}

describe("BFF error pass-through sentinel (#2645)", () => {
  test("no source reads a response body's error by hand", () => {
    const files = ROOTS.flatMap((r) => sources(path.join(WEB, r))).map(
      (full) => ({
        path: path.relative(WEB, full),
        text: readFileSync(full, "utf8"),
      }),
    );
    expect(files.length).toBeGreaterThan(100);
    expect(findPassThroughs(files)).toEqual([]);
  });

  test.each([
    [
      "cast after res.json()",
      "const j = (await res.json()) as { error?: string };\nif (j.error) msg = j.error;",
    ],
    [
      "multi-line cast",
      "const body = (await res.json()) as {\n  error?: string;\n} & Row;",
    ],
    [
      "JSON.parse cast",
      "parsed = JSON.parse(await res.text()) as { error?: string };",
    ],
    [
      "raw view-model cast",
      'typeof (raw as { error?: string }).error === "string"',
    ],
    ["intersected list type", "type L = Row & { error?: string };"],
    [
      "status-line fallback",
      "throw new Error(body.error ?? `${res.status} ${res.statusText}`);",
    ],
  ])("flags the %s shape", (_name, text) => {
    expect(findPassThroughs([{ path: "x.ts", text }])).toHaveLength(1);
  });

  test("does not flag the helper or a prop named error", () => {
    expect(
      findPassThroughs([
        {
          path: HELPER,
          text: "const e = (body as { error?: unknown } | null)?.error;",
        },
        { path: "y.tsx", text: "function F({ error }: { error?: string }) {}" },
        { path: "z.ts", text: "await upstreamErrorMessage(res);" },
      ]),
    ).toEqual([]);
  });
});
