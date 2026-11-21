// #2715 — repo-wide sentinel: every browser path that changes the tenant
// cookie resets the query cache with forgetTenantCaches() (#2621).
//
// Query keys do not carry the tenant (lib/tenant-cache.ts says why), so
// the reset on each switch path is the only thing keeping one tenant's
// cached data from rendering under another. A new switch path that
// forgets the call leaks silently. This test fails instead.
//
// What it holds:
//
//   1. Only lib/auth/switch-tenant.ts calls the switch BFF route, so a
//      new path has to go through switchTenant().
//   2. A file that calls switchTenant() or onTenantSwitched() calls
//      forgetTenantCaches() at least as many times. onTenantSwitched()
//      counts: a sibling tab's switch replaces this tab's cookie too.
//      The exemptions are named in LISTENERS_WITHOUT_RESET, and an
//      exempt file may not call switchTenant().
//   3. The server code that writes the atlas_jwt cookie is the known
//      list below, each entry naming the client reset that covers it.
//      A new writer fails here until someone names its reset.
//   4. No setQueryData() inside a mutate(vars, { onSuccess }) call.
//      tenantGuardedMutationCache() stops useMutation's own onSuccess for
//      a mutation that started before a switch, but cannot reach a
//      callback passed to mutate(); a cache write there would land in
//      the new tenant's view.
//   5. No setQueryData() after an await or a .then() in the same function
//      unless currentTenantEpoch() is read after the last wait (#2768). A
//      handler that awaits a plain fetch is not a mutation, so the guard
//      in 4 never sees it; the epoch compare is what drops its late write.
//      Files whose QueryClient is per-request on the server are named in
//      SERVER_QUERY_CLIENTS.
//
// The scan reads code, not comments, the same way
// lib/no-origin-self-fetch.test.ts does. It is a text scan, not a type
// check, and these get past it:
//
//   - an aliased import (`import { switchTenant as go }`): rule 2 counts
//     the name switchTenant, so a call through the alias is not counted;
//   - a computed route string (`"/api/auth/" + "switch-tenant"`) slips
//     rule 1, which matches the literal path;
//   - rule 2 counts per file, so a reset in one function covers a switch
//     in another function of the same file;
//   - a raw `Set-Cookie: atlas_jwt=` header escapes rule 3, which matches
//     cookies.set() and the "atlas_jwt" literal used as a set() argument;
//   - web/proxy.ts sits outside SCANNED_DIRS, so no rule reads it;
//   - rule 5 finds the enclosing function by braces, so a write in an
//     expression-bodied callback is judged by the braced function around
//     it, and an epoch read anywhere after the last wait counts, even if
//     it is not compared.

import { readFileSync, readdirSync, statSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join, relative, sep } from "node:path";
import { describe, expect, test } from "vitest";

const WEB_ROOT = join(dirname(fileURLToPath(import.meta.url)), "..");
const SCANNED_DIRS = ["app", "components", "lib"];
const SOURCE_EXT = /\.(ts|tsx)$/;
const SKIPPED = new Set(["node_modules", ".next", "dist", "coverage"]);

const SWITCH_CLIENT = "lib/auth/switch-tenant.ts";
const BROADCAST = "lib/auth/tenant-broadcast.ts";

// onTenantSwitched() listeners that need no reset of their own, because
// the switcher's listener resets the whole cache on the same broadcast.
const LISTENERS_WITHOUT_RESET: Record<string, string> = {
  "lib/auth/use-current-tenant-name.ts":
    "refetches the tenant name into React state, not the query cache",
  "lib/people-cache.ts":
    "forgetPeopleDirectory(), a subset of the switcher's full reset",
};

// Server writers of the atlas_jwt cookie, and what resets the browser
// cache for each.
const COOKIE_WRITERS: Record<string, string> = {
  "app/api/auth/switch-tenant/route.ts":
    "tenant switch: reached only through switchTenant() (rule 1), whose callers reset (rule 2)",
  "app/login/actions.ts":
    "sign-in: <ForgetPreviousSession> on the login page clears the client",
  "app/oauth/callback/route.ts":
    "OIDC sign-in: started from the login page, which clears the client",
};

function walk(dir: string, out: string[] = []): string[] {
  for (const entry of readdirSync(dir)) {
    if (SKIPPED.has(entry)) continue;
    const full = join(dir, entry);
    if (statSync(full).isDirectory()) {
      walk(full, out);
      continue;
    }
    if (SOURCE_EXT.test(entry) && !/\.test\.tsx?$/.test(entry)) {
      out.push(full);
    }
  }
  return out;
}

function stripComments(src: string): string {
  return src
    .replace(/\/\*[\s\S]*?\*\//g, "")
    .split("\n")
    .filter((line) => !line.trimStart().startsWith("//"))
    .join("\n");
}

function count(src: string, re: RegExp): number {
  return src.match(re)?.length ?? 0;
}

// A call, not a declaration: `function switchTenant(` is excluded.
const SWITCH_CALL = /(?<!function\s)\b(?:switchTenant|onTenantSwitched)\(/g;
const RESET_CALL = /(?<!function\s)\bforgetTenantCaches\(/g;
const SWITCH_ROUTE = /["'`]\/api\/auth\/switch-tenant["'`]/;
const WRITES_JWT_COOKIE =
  /\.set\(\s*ATLAS_JWT_COOKIE\b|\.set\(\s*\{\s*name:\s*ATLAS_JWT_COOKIE\b|["'`]atlas_jwt["'`]\s*,\s*[^)]/;

/** Number of switch calls in src not matched by a reset call. */
function missingResets(src: string): number {
  const code = stripComments(src);
  return Math.max(0, count(code, SWITCH_CALL) - count(code, RESET_CALL));
}

const MUTATE_CALL = /\.mutate(?:Async)?\(/g;

/**
 * The text of each mutate()/mutateAsync() call in src: from the opening
 * paren to its match. Parens inside string literals are not special-cased;
 * an unbalanced one ends the scan at the end of the file, which can only
 * widen a call and so over-flag, never under-flag.
 */
function mutateCalls(src: string): string[] {
  const calls: string[] = [];
  for (const m of src.matchAll(MUTATE_CALL)) {
    const open = m.index + m[0].length - 1;
    let depth = 0;
    let end = src.length;
    for (let i = open; i < src.length; i++) {
      if (src[i] === "(") depth++;
      else if (src[i] === ")" && --depth === 0) {
        end = i + 1;
        break;
      }
    }
    calls.push(src.slice(open, end));
  }
  return calls;
}

/** A mutate() call that writes the cache from its own onSuccess. */
function writesFromMutateCallback(src: string): boolean {
  return mutateCalls(stripComments(src)).some(
    (call) => /\bonSuccess\b/.test(call) && /\bsetQueryData\b/.test(call),
  );
}

// Files that call setQueryData on a server-side QueryClient built per
// request, which no tenant switch can reach mid-flight.
const SERVER_QUERY_CLIENTS: Record<string, string> = {
  "app/(authed)/dashboard/dashboard-prefetch.ts":
    "seeds a per-request server QueryClient for hydration",
};

// Keywords whose `(...) {` opens a block, not a function body.
const BLOCK_KEYWORDS = new Set(["if", "for", "while", "switch", "catch"]);

function matchingClose(src: string, open: number): number {
  let depth = 0;
  for (let i = open; i < src.length; i++) {
    if (src[i] === "{") depth++;
    else if (src[i] === "}" && --depth === 0) return i;
  }
  return src.length;
}

/** Whether the `{` at src[open] opens a function body. */
function opensFunction(src: string, open: number): boolean {
  let before = src.slice(0, open).trimEnd();
  if (before.endsWith("=>")) return true;
  // A return type annotation: `): Promise<void> {`.
  const annotated = /\)\s*:[^(){};=]*$/.exec(before);
  if (annotated) before = before.slice(0, annotated.index + 1);
  if (!before.endsWith(")")) return false;
  let depth = 0;
  for (let i = before.length - 1; i >= 0; i--) {
    if (before[i] === ")") depth++;
    else if (before[i] === "(" && --depth === 0) {
      const word = /(\w+)\s*$/.exec(before.slice(0, i))?.[1] ?? "";
      return !BLOCK_KEYWORDS.has(word);
    }
  }
  return false;
}

/** The `{` of the innermost function body around src[at], or -1. */
function enclosingFunction(src: string, at: number): number {
  let depth = 0;
  for (let i = at - 1; i >= 0; i--) {
    if (src[i] === "}") depth++;
    else if (src[i] === "{") {
      if (depth > 0) depth--;
      else if (opensFunction(src, i)) return i;
    }
  }
  return -1;
}

/** src[from, to) with the bodies of nested functions cut out. */
function ownCode(src: string, from: number, to: number): string {
  let out = "";
  for (let i = from; i < to; i++) {
    if (src[i] === "{" && i > from && opensFunction(src, i)) {
      const close = matchingClose(src, i);
      if (close < to) {
        i = close;
        continue;
      }
    }
    out += src[i];
  }
  return out;
}

const WAIT = /\bawait\b|\.then\(/g;
const THEN_CALLBACK =
  /\.then\(\s*(?:async\s*)?(?:\([^()]*\)|\w+)(?:\s*:[^=]*)?\s*=>\s*$/;

/**
 * Each setQueryData() in src that runs after its function has waited on
 * something (an await, or being a .then() callback) without reading
 * currentTenantEpoch() after the last wait: how many there are.
 */
function unguardedLateWrites(src: string): number {
  const code = stripComments(src);
  let found = 0;
  for (const m of code.matchAll(/\bsetQueryData\b/g)) {
    const open = enclosingFunction(code, m.index);
    const own = ownCode(code, open + 1, m.index);
    const waits = [...own.matchAll(WAIT)];
    const isThen = open >= 0 && THEN_CALLBACK.test(code.slice(0, open));
    if (waits.length === 0 && !isThen) continue;
    const lastWait = waits.at(-1);
    const afterWait = lastWait ? own.slice(lastWait.index) : own;
    if (/\bcurrentTenantEpoch\(/.test(afterWait)) continue;
    found++;
  }
  return found;
}

describe("#2715: every tenant-cookie path resets the browser query cache", () => {
  const files = SCANNED_DIRS.flatMap((d) => walk(join(WEB_ROOT, d)));
  const rel = (f: string) => relative(WEB_ROOT, f).split(sep).join("/");
  const code = new Map(
    files.map((f) => [rel(f), stripComments(readFileSync(f, "utf8"))]),
  );

  test("the scan reaches the switch paths it guards", () => {
    expect(files.length).toBeGreaterThan(200);
    for (const p of [
      SWITCH_CLIENT,
      BROADCAST,
      "components/auth/tenant-switcher.tsx",
      "app/oauth/select-tenant/page.tsx",
      ...Object.keys(COOKIE_WRITERS),
    ]) {
      expect(code.has(p), p).toBe(true);
    }
  });

  test("only switch-tenant.ts calls the switch BFF route", () => {
    const callers = [...code]
      .filter(([, src]) => SWITCH_ROUTE.test(src))
      .map(([p]) => p);
    expect(callers).toEqual([SWITCH_CLIENT]);
  });

  test("each switchTenant()/onTenantSwitched() call is matched by forgetTenantCaches()", () => {
    const offenders = [...code]
      .filter(([p]) => p !== SWITCH_CLIENT && p !== BROADCAST)
      .filter(([p]) => !(p in LISTENERS_WITHOUT_RESET))
      .filter(([, src]) => missingResets(src) > 0)
      .map(([p]) => p);
    expect(
      offenders,
      "These files switch the tenant (or react to a sibling tab's switch) " +
        "without calling forgetTenantCaches(getQueryClient()) after it. " +
        "Cached queries are not keyed by tenant, so the old tenant's data " +
        "would render under the new one. See lib/tenant-cache.ts (#2621).",
    ).toEqual([]);
    // The two paths that exist today really are counted.
    expect(
      count(code.get("components/auth/tenant-switcher.tsx")!, SWITCH_CALL),
    ).toBe(2);
    expect(
      count(code.get("app/oauth/select-tenant/page.tsx")!, SWITCH_CALL),
    ).toBe(1);
  });

  test("an exempt listener only listens, and is still a listener", () => {
    for (const p of Object.keys(LISTENERS_WITHOUT_RESET)) {
      const src = code.get(p);
      expect(src, p).toBeDefined();
      expect(src, p).toMatch(/\bonTenantSwitched\(/);
      expect(src, p).not.toMatch(/(?<!function\s)\bswitchTenant\(/);
    }
  });

  test("every server writer of the atlas_jwt cookie is on the known list", () => {
    const writers = [...code]
      .filter(([, src]) => WRITES_JWT_COOKIE.test(src))
      .map(([p]) => p)
      .sort();
    expect(
      writers,
      "A file now writes the atlas_jwt cookie. If it can change the tenant, " +
        "make sure the browser calls forgetTenantCaches() after it, then add " +
        "it to COOKIE_WRITERS with the reset that covers it.",
    ).toEqual(Object.keys(COOKIE_WRITERS).sort());
  });

  test("no mutate(vars, { onSuccess }) callback writes the query cache", () => {
    // The scan does find mutate() calls with callbacks today.
    expect(
      [...code.values()].some((src) =>
        mutateCalls(src).some((c) => /\bonSuccess\b/.test(c)),
      ),
    ).toBe(true);
    const offenders = [...code]
      .filter(([, src]) => writesFromMutateCallback(src))
      .map(([p]) => p);
    expect(
      offenders,
      "These files call setQueryData from a callback passed to mutate(). " +
        "A mutation in flight across a tenant switch still runs that " +
        "callback (tenantGuardedMutationCache cannot reach it), writing the " +
        "old tenant's data. Move the write into useMutation's onSuccess.",
    ).toEqual([]);
  });

  test("the mutate-callback detector sees a write and nothing else", () => {
    const bad = [
      "save.mutate(days, {",
      "  onSuccess: (d) => {",
      '    qc.setQueryData(["risks", "review-cadence"], d);',
      "  },",
      "});",
    ].join("\n");
    expect(writesFromMutateCallback(bad)).toBe(true);
    expect(
      writesFromMutateCallback(
        "await save.mutateAsync(x, { onSuccess: () => qc.setQueryData(k, (p) => p) });",
      ),
    ).toBe(true);

    const invalidates =
      "save.mutate(days, { onSuccess: () => qc.invalidateQueries({ queryKey: k }) });";
    expect(writesFromMutateCallback(invalidates)).toBe(false);

    // A write in useMutation's own onSuccess is the guarded form.
    const guarded = [
      "useMutation({ mutationFn: save, onSuccess: (d) => qc.setQueryData(k, d) });",
      "save.mutate(days);",
    ].join("\n");
    expect(writesFromMutateCallback(guarded)).toBe(false);
  });

  test("no setQueryData() after a wait skips the tenant-epoch check", () => {
    const offenders = [...code]
      .filter(([p]) => !(p in SERVER_QUERY_CLIENTS))
      .filter(([, src]) => unguardedLateWrites(src) > 0)
      .map(([p]) => p);
    expect(
      offenders,
      "These files call setQueryData after an await or in a .then() " +
        "without comparing currentTenantEpoch() after the wait. A tenant " +
        "switch during the request would write the old tenant's response " +
        "into the new tenant's cache. See lib/tenant-cache.ts (#2768).",
    ).toEqual([]);
    // The handlers #2768 fixed are still found and still guarded.
    for (const p of [
      "app/(authed)/exceptions/[id]/[action]/page.tsx",
      "app/(authed)/questionnaires/[id]/page.tsx",
      "app/(authed)/controls/checklist/page.tsx",
    ]) {
      const src = code.get(p);
      expect(src, p).toMatch(/\bcurrentTenantEpoch\(/);
      expect(
        unguardedLateWrites(src!.replace(/\bcurrentTenantEpoch\(/g, "x(")),
        p,
      ).toBeGreaterThan(0);
    }
    for (const p of Object.keys(SERVER_QUERY_CLIENTS)) {
      expect(code.get(p), p).toMatch(/\bsetQueryData\b/);
    }
  });

  test("the late-write detector sees an unchecked write and nothing else", () => {
    const awaited = [
      "async function submit() {",
      "  const resp = await fetch(url);",
      '  qc.setQueryData(["exceptions", id], resp);',
      "}",
    ].join("\n");
    expect(unguardedLateWrites(awaited)).toBe(1);

    const inTry = [
      "async function draftAll(): Promise<void> {",
      "  try {",
      "    const res = await fetch(url, { method: 'POST' });",
      "    if (!res.ok) { return; }",
      "    queryClient.setQueryData(k, await res.json());",
      "  } finally {}",
      "}",
    ].join("\n");
    expect(unguardedLateWrites(inTry)).toBe(1);

    const inThen = [
      "onSuccess: () => {",
      "  void fetch(url).then((r) => r.json()).then((data: T | null) => {",
      "    if (data) { qc.setQueryData(k, data); }",
      "  });",
      "},",
    ].join("\n");
    expect(unguardedLateWrites(inThen)).toBe(1);

    const checked = [
      "async function submit() {",
      "  const epoch = currentTenantEpoch(qc);",
      "  const resp = await fetch(url);",
      "  if (currentTenantEpoch(qc) !== epoch) return;",
      "  qc.setQueryData(k, resp);",
      "}",
    ].join("\n");
    expect(unguardedLateWrites(checked)).toBe(0);

    // Read only before the wait: the switch can land after it.
    const checkedTooEarly = [
      "async function submit() {",
      "  const epoch = currentTenantEpoch(qc);",
      "  const resp = await fetch(url);",
      "  qc.setQueryData(k, resp);",
      "}",
    ].join("\n");
    expect(unguardedLateWrites(checkedTooEarly)).toBe(1);

    // useMutation's onSuccess, and a write before any await: guarded by
    // tenantGuardedMutationCache() or not late at all.
    const mutation = [
      "const m = useMutation({",
      "  mutationFn: () => save(),",
      "  onSuccess: async (next) => {",
      "    qc.setQueryData(k, next);",
      "    await qc.invalidateQueries({ queryKey: k });",
      "  },",
      "});",
    ].join("\n");
    expect(unguardedLateWrites(mutation)).toBe(0);

    // An await in a sibling function does not count against this one.
    const sibling = [
      "function Page() {",
      "  async function load() { await fetch(url); }",
      "  const m = useMutation({ onSuccess: (d) => qc.setQueryData(k, d) });",
      "}",
    ].join("\n");
    expect(unguardedLateWrites(sibling)).toBe(0);

    // An await inside an if block of the same function still counts.
    const nestedAwait = [
      "async function f() {",
      "  if (x) { await fetch(url); }",
      "  qc.setQueryData(k, v);",
      "}",
    ].join("\n");
    expect(unguardedLateWrites(nestedAwait)).toBe(1);
  });

  test("the detector counts a switch with no reset (guards against a dead regex)", () => {
    const forgot = [
      "const result = await switchTenant(id);",
      "if (result.ok) router.refresh();",
    ].join("\n");
    expect(missingResets(forgot)).toBe(1);

    const listener = "const unsub = onTenantSwitched(() => router.refresh());";
    expect(missingResets(listener)).toBe(1);

    const covered = [
      "const result = await switchTenant(id);",
      "forgetTenantCaches(getQueryClient());",
    ].join("\n");
    expect(missingResets(covered)).toBe(0);

    // A reset that is only mentioned in a comment does not count.
    const inProse = [
      "const result = await switchTenant(id);",
      "// forgetTenantCaches(getQueryClient());",
    ].join("\n");
    expect(missingResets(inProse)).toBe(1);

    expect(missingResets("export async function switchTenant(id) {}")).toBe(0);
  });

  test("the cookie detector recognises both write shapes", () => {
    expect(WRITES_JWT_COOKIE.test("jar.set(ATLAS_JWT_COOKIE, token, {")).toBe(
      true,
    );
    expect(
      WRITES_JWT_COOKIE.test("resp.cookies.set({\n  name: ATLAS_JWT_COOKIE,"),
    ).toBe(true);
    expect(WRITES_JWT_COOKIE.test('jar.set("atlas_jwt", token)')).toBe(true);
    expect(WRITES_JWT_COOKIE.test("jar.get(ATLAS_JWT_COOKIE)?.value")).toBe(
      false,
    );
  });
});
