// #2312, #2558 — repo-wide ratchet: a BFF route under a dynamic `[param]`
// segment refuses an unsafe path param before it builds the upstream URL
// or reads the bearer.
//
// encodeURIComponent leaves `..` and `.` intact, so a route that
// interpolates `[id]` into its upstream path lets `/api/x/%2E%2E/verb`
// normalise upstream to `/v1/verb`. It needs the caller's own bearer, so
// it grants no privilege, but several of these routes change state.
//
// Every exported handler in a route file under a dynamic segment that
// reads its params must guard EVERY `[param]` in its path, with one of:
//   * `rejectNonUUID({ param })` for a UUID;
//   * `rejectUnsafeSegment({ param }, SEGMENT.x | CLOSED_SET)` otherwise;
//   * `SET.has(param)`, `LIST.includes(param)` or
//     `Object.hasOwn(MAP, param)`: a closed set the route already checked
//     before #2558.
// The guard comes before `cookies(`, any `/v1` path, `fetch(` or the
// forwarding call. Comments are stripped first, so a guard named only in
// a comment does not count. There is no exemption list (#2558 emptied
// KNOWN_UNGUARDED); a new route guards its params or this test fails.
//
// The second describe block drives every such handler with `..` in each
// param and checks it answers 4xx without a fetch or a cookie read.

import { readFileSync, readdirSync, statSync } from "node:fs";
import { dirname, join, relative } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";
import { mockNextServer } from "../../lib/test-utils/next-mocks";

const cookiesSpy = vi.fn();

vi.mock("next/server", () => mockNextServer());
vi.mock("next/headers", () => ({
  cookies: (...args: unknown[]) => {
    cookiesSpy(...args);
    return Promise.resolve({ get: () => ({ value: "test-bearer-2558" }) });
  },
  headers: () => Promise.resolve(new Headers()),
}));

const API_DIR = dirname(fileURLToPath(import.meta.url));
const ROUTE_FILES = new Set(["route.ts", "route.tsx", "route.js"]);
const HANDLER_RE = /^export (?:async )?function (GET|POST|PUT|PATCH|DELETE)\b/m;

function dynamicRoutes(dir: string): string[] {
  const out: string[] = [];
  for (const name of readdirSync(dir)) {
    const p = join(dir, name);
    if (statSync(p).isDirectory()) {
      out.push(...dynamicRoutes(p));
    } else if (ROUTE_FILES.has(name)) {
      const rel = relative(API_DIR, p);
      if (rel.includes("[")) out.push(rel);
    }
  }
  return out;
}

function routeParams(rel: string): string[] {
  return [...rel.matchAll(/\[([^\]]+)\]/g)].map((m) => m[1]);
}

// stripComments drops block comments and `//` line comments. A `//`
// after `:` is kept, so a URL literal (`http://web`) survives.
function stripComments(src: string): string {
  return src
    .replace(/\/\*[\s\S]*?\*\//g, "")
    .replace(/(^|[^:\\])\/\/.*$/gm, "$1");
}

// handlers splits a route file into its exported handler bodies.
function handlers(src: string): { name: string; body: string }[] {
  const out: { name: string; body: string }[] = [];
  const parts = src.split(/(?=^export (?:async )?function )/m);
  for (const part of parts) {
    const m = HANDLER_RE.exec(part);
    if (m) out.push({ name: m[1], body: part });
  }
  return out;
}

function escape(s: string): string {
  return s.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

// guardIndex is where `param` is first guarded in `body`, or -1.
function guardIndex(body: string, param: string): number {
  const p = escape(param);
  const res = [
    new RegExp(`rejectNonUUID\\(\\s*\\{[^}]*\\b${p}\\b[^}]*\\}`),
    new RegExp(`rejectUnsafeSegment\\(\\s*\\{[^}]*\\b${p}\\b[^}]*\\}`),
    new RegExp(`\\.(?:has|includes)\\(\\s*${p}\\b`),
    new RegExp(`Object\\.hasOwn\\(\\s*[A-Za-z_]+,\\s*${p}\\b`),
  ];
  const hits = res
    .map((re) => re.exec(body)?.index ?? -1)
    .filter((i) => i >= 0);
  return hits.length ? Math.min(...hits) : -1;
}

// upstreamIndex is where `body` first reads the bearer or builds or
// calls upstream, or -1.
function upstreamIndex(body: string): number {
  const hits = [
    /\bcookies\(/,
    /\/v1\b/,
    /\bfetch\(/,
    /\bforward[A-Za-z]*\(/,
    /\bproxy\(/,
    /\bupstreamPath\(/,
  ]
    .map((re) => re.exec(body)?.index ?? -1)
    .filter((i) => i >= 0);
  return hits.length ? Math.min(...hits) : -1;
}

// findings lists every unguarded or late-guarded param in one route file.
function findings(rel: string, raw: string): string[] {
  const params = routeParams(rel);
  const out: string[] = [];
  for (const h of handlers(stripComments(raw))) {
    if (!/\bparams\b/.test(h.body)) continue;
    const up = upstreamIndex(h.body);
    for (const p of params) {
      const g = guardIndex(h.body, p);
      if (g < 0) out.push(`${rel} ${h.name}: [${p}] not guarded`);
      else if (up >= 0 && up < g)
        out.push(`${rel} ${h.name}: [${p}] guarded after upstream`);
    }
  }
  return out;
}

describe("BFF path-param guard ratchet (#2312, #2558)", () => {
  const routes = dynamicRoutes(API_DIR);

  test("finds the dynamic BFF routes", () => {
    expect(routes.length).toBeGreaterThan(100);
  });

  test("every param of every dynamic BFF route is guarded first", () => {
    const all = routes.flatMap((r) =>
      findings(r, readFileSync(join(API_DIR, r), "utf8")),
    );
    expect(all).toEqual([]);
  });

  test("a guard inside a comment does not count", () => {
    const src = `export async function GET(_r: Request, ctx: { params: Promise<{ id: string }> }) {
  const { id } = await ctx.params;
  // rejectNonUUID({ id });
  /* rejectNonUUID({ id }) */
  return forwardJSON(\`/v1/x/\${id}\`);
}`;
    expect(findings("x/[id]/route.ts", src)).toEqual([
      "x/[id]/route.ts GET: [id] not guarded",
    ]);
  });

  test("every param on a multi-param route must be guarded", () => {
    const src = `export async function DELETE(_r: Request, ctx: { params: Promise<{ id: string; childId: string }> }) {
  const { id, childId } = await ctx.params;
  const bad = rejectNonUUID({ id });
  if (bad) return bad;
  return forwardJSON(\`/v1/x/\${id}/c/\${childId}\`);
}`;
    expect(findings("x/[id]/c/[childId]/route.tsx", src)).toEqual([
      "x/[id]/c/[childId]/route.tsx DELETE: [childId] not guarded",
    ]);
  });

  test("a guard after the bearer read does not count", () => {
    const src = `export async function POST(_r: Request, ctx: { params: Promise<{ key: string }> }) {
  const jar = await cookies();
  const { key } = await ctx.params;
  const bad = rejectUnsafeSegment({ key }, SEGMENT.slug);
  if (bad) return bad;
  return forwardJSON(\`/v1/x/\${key}\`);
}`;
    expect(findings("x/[key]/route.js", src)).toEqual([
      "x/[key]/route.js POST: [key] guarded after upstream",
    ]);
  });

  test("route.tsx and route.js files are scanned", () => {
    expect(ROUTE_FILES.has("route.tsx")).toBe(true);
    expect(ROUTE_FILES.has("route.js")).toBe(true);
  });
});

// A value every guard admits for a param that is not under test: a
// lowercase UUID passes rejectNonUUID and every SEGMENT allowlist. A
// closed-set param may still refuse it; that refusal is a 4xx before any
// fetch too, which is all this block checks.
const VALID = "00000000-0000-0000-0000-000000002558";

describe("`..` in any BFF path param is refused before upstream (#2558)", () => {
  const routes = dynamicRoutes(API_DIR).filter((r) => !r.endsWith(".js"));
  let fetchSpy: ReturnType<typeof vi.spyOn>;

  beforeEach(() => {
    cookiesSpy.mockReset();
    fetchSpy = vi
      .spyOn(globalThis, "fetch")
      .mockResolvedValue(new Response("{}", { status: 200 }));
    process.env.ATLAS_HTTP_URL = "http://atlas:8080";
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  for (const rel of routes) {
    const params = routeParams(rel);
    for (const p of params) {
      test(`${rel} [${p}]=..`, async () => {
        const mod = (await import(
          pathToFileURL(join(API_DIR, rel)).href
        )) as Record<string, unknown>;
        const values = Object.fromEntries(
          params.map((q) => [q, q === p ? ".." : VALID]),
        );
        let called = 0;
        for (const verb of ["GET", "POST", "PUT", "PATCH", "DELETE"]) {
          const h = mod[verb];
          if (typeof h !== "function") continue;
          called++;
          const res = (await h(
            new Request(`http://web/api/${rel}`, { method: verb }),
            { params: Promise.resolve(values) },
          )) as Response;
          expect([400, 404], `${verb} status`).toContain(res.status);
        }
        expect(called).toBeGreaterThan(0);
        expect(fetchSpy).not.toHaveBeenCalled();
        expect(cookiesSpy).not.toHaveBeenCalled();
      });
    }
  }
});
