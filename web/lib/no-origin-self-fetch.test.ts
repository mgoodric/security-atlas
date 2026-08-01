// OE-549 — repo-wide sentinel: no server-side code may rebuild this app's
// own origin from the request Host header and fetch itself.
//
// The behavioural test (`lib/api/self.server.test.ts`) proves the probes
// that exist TODAY resolve correctly in a container. This test stops the
// pattern from coming back somewhere new — which matters because the
// original defect spread by copy-paste: five files carried the same six
// lines, each comment citing the previous file as the precedent
// ("same pattern as the slice 186 admin probe").
//
// The pattern that is banned:
//
//     const host = h.get("host") ?? "localhost:3000";
//     const proto = h.get("x-forwarded-proto") ?? "http";
//     await fetch(`${proto}://${host}/api/...`);
//
// Why it is unreachable in the shipped container:
//
//   * `host` carries the EXTERNAL published port (compose `3001:3000`),
//     and nothing listens on that port inside the container.
//   * `next-server` in the production image binds the CONTAINER IP
//     (observed 172.28.0.7:3000), not loopback — so even the correct
//     internal port is refused when it resolves to 127.0.0.1.
//   * Behind a reverse proxy `host` is the PUBLIC hostname, which the
//     container may not be able to resolve or route to at all.
//
// The correct server-side pattern is `apiBaseURL()` -> `ATLAS_HTTP_URL`
// (`lib/api/base.ts`), which every other page in the app already uses.
// For the auth-gate / self-introspection probes specifically, use
// `lib/api/self.server.ts`.
//
// SCOPE NOTE. Reading the Host header is not itself wrong — building an
// ABSOLUTE URL from it and fetching that URL is. This test flags only
// files that do BOTH, so a legitimate Host reader (canonical-URL
// rendering, an OAuth redirect_uri, a Location header) does not trip it.

import { readFileSync, readdirSync, statSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join, relative } from "node:path";
import { describe, expect, test } from "vitest";

const WEB_ROOT = join(dirname(fileURLToPath(import.meta.url)), "..");

// Directories that ship server-rendered code. `e2e/` and `e2e-audit/`
// are Playwright harnesses that legitimately drive the app over its
// public origin — they are clients, not the server.
const SCANNED_DIRS = ["app", "components", "lib"];

const SOURCE_EXT = /\.(ts|tsx)$/;
const SKIPPED = new Set(["node_modules", ".next", "dist", "coverage"]);

function walk(dir: string, out: string[] = []): string[] {
  for (const entry of readdirSync(dir)) {
    if (SKIPPED.has(entry)) continue;
    const full = join(dir, entry);
    if (statSync(full).isDirectory()) {
      walk(full, out);
      continue;
    }
    // Test files are allowed to mention the pattern (this file does).
    if (SOURCE_EXT.test(entry) && !/\.test\.tsx?$/.test(entry)) {
      out.push(full);
    }
  }
  return out;
}

/**
 * stripComments removes block comments and whole-line `//` comments so the
 * detector reads CODE, not prose. Required: the fix's own explanatory
 * headers (this file, `lib/api/self.server.ts`, the five repaired call
 * sites) quote the banned pattern verbatim to document what was removed,
 * and a naive scan flags them.
 *
 * Whole-line only — a trailing `//` strip would mangle every `https://`
 * in a string literal, and a comment that shares a line with a genuine
 * self-fetch does not hide it from the line-agnostic regexes below.
 */
function stripComments(src: string): string {
  return src
    .replace(/\/\*[\s\S]*?\*\//g, "")
    .split("\n")
    .filter((line) => !line.trimStart().startsWith("//"))
    .join("\n");
}

/** Reads the Host header off the incoming request. */
const READS_HOST =
  /\.get\(\s*["'`](?:host|x-forwarded-host)["'`]\s*\)|headers\(\)\s*\)?\s*\.get\(\s*["'`]host["'`]/i;

/** Builds an absolute URL out of a `host`/`proto` pair and fetches it. */
const FETCHES_RECONSTRUCTED_ORIGIN =
  /fetch\(\s*[`"']\s*\$\{\s*(?:proto|protocol|scheme)\s*\}:\/\/\$\{\s*host\s*\}|fetch\(\s*[`"'][^`"']*\$\{\s*host\s*\}/i;

describe("OE-549: no server-side self-fetch via the request Host header", () => {
  const files = SCANNED_DIRS.flatMap((d) => walk(join(WEB_ROOT, d)));

  test("the scan actually reaches the files it claims to cover", () => {
    // A silently-empty walk would make every assertion below vacuous.
    expect(files.length).toBeGreaterThan(200);
    const rel = files.map((f) => relative(WEB_ROOT, f));
    expect(rel).toContain(join("app", "admin", "layout.tsx"));
    expect(rel).toContain(join("app", "audit-log", "layout.tsx"));
    expect(rel).toContain(join("components", "shell", "sidebar.tsx"));
    expect(rel).toContain(join("components", "shell", "user-avatar.tsx"));
    expect(rel).toContain(join("lib", "feature-nav.server.ts"));
    expect(rel).toContain(join("lib", "api", "self.server.ts"));
  });

  test("no source file both reads the Host header and fetches an origin built from it", () => {
    const offenders: string[] = [];
    for (const file of files) {
      const src = stripComments(readFileSync(file, "utf8"));
      if (READS_HOST.test(src) && FETCHES_RECONSTRUCTED_ORIGIN.test(src)) {
        offenders.push(relative(WEB_ROOT, file));
      }
    }
    expect(
      offenders,
      `These files rebuild the app's own origin from the request Host header ` +
        `and fetch it. That address does not resolve inside the web container ` +
        `(external published port; next-server binds the container IP, not ` +
        `loopback). Use apiBaseURL() -> ATLAS_HTTP_URL, or the probes in ` +
        `lib/api/self.server.ts. See OE-549.`,
    ).toEqual([]);
  });

  test("the detector recognises the exact shape that was removed (guards against a dead regex)", () => {
    // Verbatim from the pre-OE-549 web/app/admin/layout.tsx isAdmin().
    const removed = [
      "const h = await headers();",
      'const host = h.get("host") ?? "localhost:3000";',
      'const proto = h.get("x-forwarded-proto") ?? "http";',
      "const res = await fetch(`${proto}://${host}/api/admin/me`, {",
      "  headers: { Cookie: `${ATLAS_JWT_COOKIE}=${bearer}` },",
      "});",
    ].join("\n");

    expect(READS_HOST.test(removed)).toBe(true);
    expect(FETCHES_RECONSTRUCTED_ORIGIN.test(removed)).toBe(true);
  });

  test("the detector does NOT flag a Host read with no self-fetch", () => {
    const benign = [
      "const h = await headers();",
      'const host = h.get("host") ?? "localhost:3000";',
      "return { canonical: `https://${host}${pathname}` };",
    ].join("\n");

    expect(READS_HOST.test(benign)).toBe(true);
    expect(FETCHES_RECONSTRUCTED_ORIGIN.test(benign)).toBe(false);
  });

  test("stripComments hides prose but not code", () => {
    // The banned shape quoted inside a comment must not trip the scan...
    const inProse = [
      "/*",
      ' * const host = h.get("host") ?? "localhost:3000";',
      " * await fetch(`${proto}://${host}/api/admin/me`);",
      " */",
      '// const host = h.get("host");',
      "export const x = 1;",
    ].join("\n");
    const stripped = stripComments(inProse);
    expect(READS_HOST.test(stripped)).toBe(false);
    expect(FETCHES_RECONSTRUCTED_ORIGIN.test(stripped)).toBe(false);

    // ...but the same shape as real code must still trip it.
    const inCode = [
      'const host = h.get("host") ?? "localhost:3000";',
      "await fetch(`${proto}://${host}/api/admin/me`);",
    ].join("\n");
    expect(READS_HOST.test(stripComments(inCode))).toBe(true);
    expect(FETCHES_RECONSTRUCTED_ORIGIN.test(stripComments(inCode))).toBe(true);

    // A trailing comment must not eat the code on its own line.
    const trailing = 'const u = "https://example.com"; // note';
    expect(stripComments(trailing)).toContain("https://example.com");
  });

  test("the detector does NOT flag the correct apiBaseURL() pattern", () => {
    const correct = [
      "const res = await fetch(`${apiBaseURL()}/v1/me`, {",
      "  headers: { Authorization: `Bearer ${bearer}` },",
      '  cache: "no-store",',
      "});",
    ].join("\n");

    expect(FETCHES_RECONSTRUCTED_ORIGIN.test(correct)).toBe(false);
  });
});
