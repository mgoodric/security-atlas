// #2757: every BFF site that relays a platform answer with the platform's
// status puts the body through `upstreamBody()`, so a 5xx body never reaches
// the wire (#2704). This scans the route handlers and lib modules and fails
// on a new `new NextResponse(text, { status: upstream.status })` outside
// bff.ts.
//
// A call is any `NextResponse(`, `Response(`, `NextResponse.json(` or
// `Response.json(`. It is a relay when its init's status is `<x>.status`, or
// a variable the enclosing function read from `<x>.status`. Its body must be
// `null`, go through `upstreamBody(`, or be a success-path stream
// (`upstream.body`, an ArrayBuffer) after an `!upstream.ok` branch in the
// same function, which only a non-5xx can get past. `e.status` /
// `err.status` are the catch blocks relaying an APIError message; lib/api
// builds those messages with upstreamErrorMessage, covered by
// upstream-error-sites.test.ts (#2645).

import { readdirSync, readFileSync } from "node:fs";
import path from "node:path";

import { describe, expect, test } from "vitest";

const WEB = path.resolve(__dirname, "..", "..");
const EXEMPT = new Set([path.join("lib", "api", "bff.ts")]);
const CATCH_STATUS = new Set(["e", "err"]);
const STREAMS = new Set(["upstream.body", "bytes", "buf"]);
const CONTROL = new Set(["if", "for", "while", "switch", "catch", "with"]);

// sources lists the non-test `.ts` files under `dir`. Responses are built
// only in route handlers and lib modules; `.tsx` pages build none, and JSX
// text is not something `mask` can read.
function sources(dir: string, out: string[] = []): string[] {
  for (const e of readdirSync(dir, { withFileTypes: true })) {
    const p = path.join(dir, e.name);
    if (e.isDirectory()) {
      if (e.name !== "node_modules" && e.name !== ".next") sources(p, out);
    } else if (/\.ts$/.test(e.name) && !/\.test\.ts$/.test(e.name)) {
      out.push(p);
    }
  }
  return out;
}

// mask blanks comments and the contents of string, template and regex
// literals, keeping offsets and newlines, so brace and paren matching sees
// only code.
export function mask(src: string): string {
  const out = src.split("");
  const blank = (from: number, to: number) => {
    for (let k = from; k < to; k++) if (out[k] !== "\n") out[k] = " ";
  };
  let prev = "";
  for (let i = 0; i < src.length; i++) {
    const c = src[i];
    if (c === "/" && src[i + 1] === "/") {
      const end = src.indexOf("\n", i);
      const stop = end < 0 ? src.length : end;
      blank(i, stop);
      i = stop - 1;
      continue;
    }
    if (c === "/" && src[i + 1] === "*") {
      const end = src.indexOf("*/", i + 2);
      const stop = end < 0 ? src.length : end + 2;
      blank(i, stop);
      i = stop - 1;
      continue;
    }
    const regex =
      c === "/" && (prev === "" || "(,=:[!&|?{};+-*%~^".includes(prev));
    if (c === '"' || c === "'" || c === "`" || regex) {
      let j = i + 1;
      let inClass = false;
      for (; j < src.length; j++) {
        if (src[j] === "\\") {
          j++;
        } else if (regex && src[j] === "[") {
          inClass = true;
        } else if (regex && src[j] === "]") {
          inClass = false;
        } else if (src[j] === c && !inClass) {
          break;
        } else if (!regex && c !== "`" && src[j] === "\n") {
          break;
        }
      }
      blank(i + 1, j);
      i = j;
      prev = "a";
      continue;
    }
    if (!/\s/.test(c)) prev = c;
  }
  return out.join("");
}

// callArgs returns the [start, end) offsets of each top-level argument of
// the call whose `(` is at `open`, in masked source.
function callArgs(code: string, open: number): [number, number][] | null {
  const spans: [number, number][] = [];
  let depth = 0;
  let start = open + 1;
  for (let i = open; i < code.length; i++) {
    const c = code[i];
    if ("([{".includes(c)) depth++;
    else if (")]}".includes(c)) {
      depth--;
      if (depth === 0) {
        spans.push([start, i]);
        return spans;
      }
    } else if (c === "," && depth === 1) {
      spans.push([start, i]);
      start = i + 1;
    }
  }
  return null;
}

// isFunctionBody reports whether the `{` at `at` opens a function body:
// it follows `=>`, or a `)` (plus an optional return type) whose `(` is not
// a control keyword's.
function isFunctionBody(code: string, at: number): boolean {
  const before = code.slice(0, at);
  if (/=>\s*$/.test(before)) return true;
  let i = before.search(/\)\s*(?::[^(){};=]*)?$/);
  if (i < 0) return false;
  let depth = 0;
  for (; i >= 0; i--) {
    if (code[i] === ")") depth++;
    else if (code[i] === "(" && --depth === 0) break;
  }
  if (i < 0) return false;
  const word = /(\w+)\s*$/.exec(code.slice(0, i));
  return !word || !CONTROL.has(word[1]);
}

// enclosingFunction returns the offset of the `{` that opens the innermost
// function body around `at`, or 0 at module level.
function enclosingFunction(code: string, at: number): number {
  const open: number[] = [];
  for (let i = 0; i < at; i++) {
    if (code[i] === "{") open.push(i);
    else if (code[i] === "}") open.pop();
  }
  for (let k = open.length - 1; k >= 0; k--) {
    if (isFunctionBody(code, open[k])) return open[k];
  }
  return 0;
}

// statusSource returns the response variable a call's status comes from,
// or null when the status is not an upstream response's.
function statusSource(init: string, scope: string): string | null {
  const m = /\bstatus\b(?:\s*:\s*([\w.]+))?/.exec(init);
  if (!m) return null;
  const value = m[1] ?? "status";
  const direct = /^(\w+)\.status$/.exec(value);
  if (direct) return direct[1];
  if (!/^\w+$/.test(value)) return null;
  const alias = new RegExp(
    `(?:const|let|var)\\s+${value}\\s*=\\s*(\\w+)\\.status\\b`,
  ).exec(scope);
  if (alias) return alias[1];
  if (value === "status") {
    const destructured =
      /(?:const|let|var)\s*\{[^}]*\bstatus\b[^}]*\}\s*=\s*(\w+)/.exec(scope);
    if (destructured) return destructured[1];
  }
  return null;
}

// rawRelays returns the 1-based line of every relay in `src` whose body
// bypasses upstreamBody.
export function rawRelays(src: string): number[] {
  const code = mask(src);
  const bad: number[] = [];
  const re = /\b(?:new\s+)?(?:Next)?Response(?:\.json)?\(/g;
  for (let m = re.exec(code); m; m = re.exec(code)) {
    const spans = callArgs(code, m.index + m[0].length - 1);
    if (!spans || spans.length < 2) continue;
    const fn = enclosingFunction(code, m.index);
    const scope = code.slice(fn, m.index);
    const from = statusSource(code.slice(...spans[1]), scope);
    if (!from || CATCH_STATUS.has(from)) continue;
    const body = code.slice(...spans[0]).trim();
    if (body === "null" || body.includes("upstreamBody(")) continue;
    if (STREAMS.has(body) && scope.includes("!upstream.ok")) continue;
    bad.push(src.slice(0, m.index).split("\n").length);
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

  test("mask leaves every scanned file's braces balanced", () => {
    const unbalanced: string[] = [];
    for (const dir of ["app", "lib"]) {
      for (const file of sources(path.join(WEB, dir))) {
        const code = mask(readFileSync(file, "utf8"));
        let depth = 0;
        for (const c of code) {
          if (c === "{") depth++;
          else if (c === "}") depth--;
        }
        if (depth !== 0) unbalanced.push(path.relative(WEB, file));
      }
    }
    expect(unbalanced).toEqual([]);
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
    [
      "plain Response",
      `return new Response(await r.text(), { status: r.status });`,
    ],
    ["Response.json", `return Response.json(body, { status: r.status });`],
    [
      "status shorthand",
      `async function GET() {
  const status = upstream.status;
  return new NextResponse(text, { status });
}`,
    ],
    [
      "status variable",
      `async function GET() {
  const code = upstream.status;
  return new NextResponse(text, { status: code });
}`,
    ],
    [
      "destructured status",
      `async function GET() {
  const { status } = upstream;
  return new NextResponse(text, { status });
}`,
    ],
    [
      "stream whose ok branch is in another function",
      `function check(upstream: Response) {
  if (!upstream.ok) throw new Error("x");
}
export async function GET(): Promise<Response> {
  return new NextResponse(upstream.body, { status: upstream.status });
}`,
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
      `export async function GET(): Promise<Response> {
  if (!upstream.ok) {
    return new NextResponse(upstreamBody(upstream.status, t), { status: upstream.status });
  }
  return new NextResponse(upstream.body, { status: upstream.status });
}`,
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
    [
      "status parameter",
      `function bad(msg: string, status = 400) {
  return NextResponse.json({ error: msg }, { status });
}`,
    ],
    [
      "commented-out raw relay",
      `// return new NextResponse(text, { status: upstream.status });`,
    ],
  ])("passes a %s", (_n, src) => {
    expect(rawRelays(src)).toEqual([]);
  });
});
