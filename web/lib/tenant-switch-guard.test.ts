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
//
// The scan reads code, not comments, the same way
// lib/no-origin-self-fetch.test.ts does.

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
