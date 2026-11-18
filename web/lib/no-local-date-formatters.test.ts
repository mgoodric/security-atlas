// #2365 — repo-wide sentinel: pages render dates through the one shared
// formatter, `<Timestamp>` (`components/ui/timestamp.tsx`) over
// `formatTimestamp` (`lib/relative-time.ts`), never a formatter of their own.
//
// #2084 built the shared formatter and #2365 moved the last per-page
// copies onto it. This test stops a new copy from landing. It bans, in
// `app/` and `components/`:
//
//   * a function named like a date formatter (`formatDate`, `dateLabel`,
//     `formatTimestamp`, `relativeTime`, ...) — the #2084 / #2365
//     acceptance grep, verbatim;
//   * `new Date(...).toLocaleString(...)` — a raw locale render of a
//     timestamp, which ignores the shared styles, the UTC rule for
//     calendar days and the exact-value tooltip.
//
// `lib/` is not scanned: the shared formatter itself lives there.

import { readFileSync, readdirSync, statSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join, relative } from "node:path";
import { describe, expect, test } from "vitest";

const WEB_ROOT = join(dirname(fileURLToPath(import.meta.url)), "..");
const SCANNED_DIRS = ["app", "components"];
const SOURCE_EXT = /\.(ts|tsx)$/;
const TEST_FILE = /\.test\.tsx?$/;

const LOCAL_FORMATTER =
  /function \w*(formatDate|fmtDate|dateLabel|formatTime|formatTimestamp|formatDetailDate|formatRelative|relativeTime)\w*\(/;
const RAW_LOCALE_RENDER =
  /new Date\([^()]*(\([^()]*\))?[^()]*\)\.toLocaleString\(/;

function walk(dir: string, out: string[] = []): string[] {
  for (const entry of readdirSync(dir)) {
    const full = join(dir, entry);
    if (statSync(full).isDirectory()) {
      walk(full, out);
    } else if (SOURCE_EXT.test(entry) && !TEST_FILE.test(entry)) {
      out.push(full);
    }
  }
  return out;
}

function offenders(pattern: RegExp): string[] {
  const hits: string[] = [];
  for (const dir of SCANNED_DIRS) {
    for (const file of walk(join(WEB_ROOT, dir))) {
      readFileSync(file, "utf8")
        .split("\n")
        .forEach((line, i) => {
          if (pattern.test(line)) {
            hits.push(`${relative(WEB_ROOT, file)}:${i + 1}: ${line.trim()}`);
          }
        });
    }
  }
  return hits;
}

describe("dates render through the shared formatter (#2365)", () => {
  test("no page defines its own date formatter", () => {
    expect(offenders(LOCAL_FORMATTER)).toEqual([]);
  });

  test("no page renders a timestamp with new Date(...).toLocaleString()", () => {
    expect(offenders(RAW_LOCALE_RENDER)).toEqual([]);
  });

  test("the patterns catch what they ban", () => {
    expect(LOCAL_FORMATTER.test("function dateLabel(iso?: string) {")).toBe(
      true,
    );
    expect(LOCAL_FORMATTER.test("export function formatDetailDate(x) {")).toBe(
      true,
    );
    expect(LOCAL_FORMATTER.test("function formatValue(v: number) {")).toBe(
      false,
    );
    expect(
      RAW_LOCALE_RENDER.test("{new Date(q.updated_at).toLocaleString()}"),
    ).toBe(true);
    expect(
      RAW_LOCALE_RENDER.test("new Date(Date.now()).toLocaleString()"),
    ).toBe(true);
    expect(RAW_LOCALE_RENDER.test("amount.toLocaleString(undefined, {})")).toBe(
      false,
    );
  });
});
