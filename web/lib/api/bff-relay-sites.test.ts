// #2757: every BFF site that relays a platform answer with the platform's
// status puts the body through `upstreamBody()`, so a 5xx body never reaches
// the wire (#2704). This scans the route handlers and lib modules and fails
// on a new `new NextResponse(text, { status: upstream.status })` outside
// bff.ts.
//
// A call is a relay when its init sets `status: <x>.status`. Its body must
// be `null`, go through `upstreamBody(`, or be a success-path stream
// (`upstream.body`, an ArrayBuffer) after an `if (!upstream.ok)` branch,
// which only a non-5xx can reach. `e.status` / `err.status` are the catch
// blocks relaying an APIError message; lib/api builds those messages with
// upstreamErrorMessage, covered by upstream-error-sites.test.ts (#2645).

import { readdirSync, readFileSync } from "node:fs";
import path from "node:path";

import { describe, expect, test } from "vitest";

const WEB = path.resolve(__dirname, "..", "..");
const EXEMPT = new Set([path.join("lib", "api", "bff.ts")]);
const CATCH_STATUS = new Set(["e", "err"]);
const STREAMS = new Set(["upstream.body", "bytes", "buf"]);

function sources(dir: string, out: string[] = []): string[] {
  for (const e of readdirSync(dir, { withFileTypes: true })) {
    const p = path.join(dir, e.name);
    if (e.isDirectory()) {
      if (e.name !== "node_modules" && e.name !== ".next") sources(p, out);
    } else if (/\.tsx?$/.test(e.name) && !/\.test\.tsx?$/.test(e.name)) {
      out.push(p);
    }
  }
  return out;
}

// callArgs returns the top-level arguments of the call whose `(` is at
// `open`, skipping string and template literals.
function callArgs(src: string, open: number): string[] | null {
  const args: string[] = [];
  let depth = 0;
  let start = open + 1;
  let quote: string | null = null;
  for (let i = open; i < src.length; i++) {
    const c = src[i];
    if (quote) {
      if (c === "\\") i++;
      else if (c === quote) quote = null;
      continue;
    }
    if (c === '"' || c === "'" || c === "`") quote = c;
    else if ("([{".includes(c)) depth++;
    else if (")]}".includes(c)) {
      depth--;
      if (depth === 0) {
        args.push(src.slice(start, i));
        return args;
      }
    } else if (c === "," && depth === 1) {
      args.push(src.slice(start, i));
      start = i + 1;
    }
  }
  return null;
}

// rawRelays returns the 1-based line of every relay in `src` whose body
// bypasses upstreamBody.
export function rawRelays(src: string): number[] {
  const bad: number[] = [];
  const re = /NextResponse(?:\.json)?\(/g;
  for (let m = re.exec(src); m; m = re.exec(src)) {
    const args = callArgs(src, m.index + m[0].length - 1);
    if (!args || args.length < 2) continue;
    const status = /status:\s*(\w+)\.status\b/.exec(args[1]);
    if (!status || CATCH_STATUS.has(status[1])) continue;
    const body = args[0].trim();
    if (body === "null" || body.includes("upstreamBody(")) continue;
    const before = src.slice(0, m.index);
    if (STREAMS.has(body) && before.includes("!upstream.ok")) continue;
    bad.push(before.split("\n").length);
  }
  return bad;
}

describe("BFF relays mask 5xx bodies (#2757)", () => {
  test("no route handler or lib module relays a raw upstream body", () => {
    const offenders: string[] = [];
    for (const dir of ["app", "lib"]) {
      for (const file of sources(path.join(WEB, dir))) {
        const rel = path.relative(WEB, file);
        if (EXEMPT.has(rel)) continue;
        for (const line of rawRelays(readFileSync(file, "utf8"))) {
          offenders.push(`${rel}:${line}`);
        }
      }
    }
    expect(offenders).toEqual([]);
  });

  test.each([
    [
      "raw text",
      `const text = await upstream.text();
return new NextResponse(text, { status: upstream.status });`,
    ],
    [
      "parsed JSON",
      `return NextResponse.json(text ? JSON.parse(text) : null, {
  status: upstream.status,
});`,
    ],
    [
      "stream with no ok branch",
      `return new NextResponse(upstream.body, { status: upstream.status });`,
    ],
    [
      "inline read",
      `return new NextResponse(await res.text(), { status: res.status });`,
    ],
  ])("flags a %s relay", (_n, src) => {
    expect(rawRelays(src)).toHaveLength(1);
  });

  test.each([
    [
      "masked text",
      `return new NextResponse(upstreamBody(upstream.status, text), {
  status: upstream.status,
});`,
    ],
    [
      "masked JSON",
      `return NextResponse.json(
  text ? JSON.parse(upstreamBody(upstream.status, text)) : null,
  { status: upstream.status },
);`,
    ],
    [
      "stream after the ok branch",
      `if (!upstream.ok) {
  return new NextResponse(upstreamBody(upstream.status, t), { status: upstream.status });
}
return new NextResponse(upstream.body, { status: upstream.status });`,
    ],
    [
      "null body",
      `return new NextResponse(null, { status: upstream.status });`,
    ],
    [
      "APIError catch",
      `return NextResponse.json({ error: e.message }, { status: e.status ?? 500 });`,
    ],
    ["fixed status", `return new NextResponse(text, { status: 200 });`],
  ])("passes a %s", (_n, src) => {
    expect(rawRelays(src)).toEqual([]);
  });
});
