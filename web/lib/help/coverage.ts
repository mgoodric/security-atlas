// #2156 — every authenticated route has a help module.
//
// The routes come from the filesystem, the same walk the route sweep
// (`e2e/route-sweep.spec.ts`) visits: every directory under an
// authenticated tree of `web/app` that holds a `page.tsx`. A route is
// covered when `helpModuleForPath` resolves it, the lookup the **?**
// button uses. A dynamic segment stays in its `[name]` form, which a
// parent route's module covers like any other child path.
//
// The check fails on a route with no help unless the route is listed
// below, in one of two lists:
//
//   * HELP_EXEMPT — routes that never render a page of their own. Each
//     entry gives its reason, and a test proves the reason still holds.
//   * HELP_PENDING — routes whose help is still to be written. Each entry
//     names the open ticket that writes it. The list only shrinks: an
//     entry whose route gains help is stale and fails, and so does an
//     entry whose ticket is closed (`coverage.test.ts`).
//
// Login and error pages sit outside the authenticated trees (`/login`,
// the root not-found), so the walk never reaches them and they need no
// entry.
//
// No `.md` imports here, directly or through `modules.ts`: the Playwright
// sweep has no raw-Markdown loader, so it reads the help files with
// `readHelpModules` and parses them with the app's own parser.

import fs from "node:fs";
import path from "node:path";

import {
  helpModuleForPath,
  parseHelpModule,
  type HelpModule,
} from "./module-parse";

/** The authenticated trees under `web/app`. `(authed)` is the route
 *  group; `/admin`, `/audit` and `/audit-log` live outside it because
 *  their paths are literal, but render the same shell. */
export const AUTHED_ROOTS = ["(authed)", "admin", "audit", "audit-log"];

const isGroup = (name: string) => /^\(.*\)$/.test(name);

/** Every route pattern under the authenticated trees of `appDir`, sorted.
 *  Route groups add no segment; `_private` folders are not routes. */
export function discoverAuthedRoutes(
  appDir: string,
  roots: readonly string[] = AUTHED_ROOTS,
): string[] {
  const patterns: string[] = [];
  const walk = (dir: string, segments: string[]) => {
    const entries = fs.readdirSync(dir, { withFileTypes: true });
    if (entries.some((e) => e.isFile() && e.name === "page.tsx")) {
      patterns.push("/" + segments.join("/"));
    }
    for (const e of entries) {
      if (!e.isDirectory() || e.name.startsWith("_")) continue;
      walk(path.join(dir, e.name), [
        ...segments,
        ...(isGroup(e.name) ? [] : [e.name]),
      ]);
    }
  };
  for (const root of roots) {
    walk(path.join(appDir, root), isGroup(root) ? [] : [root]);
  }
  return patterns.sort();
}

/** Every help file in `helpDir` (`web/help/modules`), parsed. */
export function readHelpModules(helpDir: string): HelpModule[] {
  return fs
    .readdirSync(helpDir)
    .filter((f) => f.endsWith(".md"))
    .sort()
    .map((f) =>
      parseHelpModule(fs.readFileSync(path.join(helpDir, f), "utf8")),
    );
}

export type HelpExemption = { route: string; reason: string };
export type HelpPending = { route: string; ticket: number };

export const HELP_EXEMPT: readonly HelpExemption[] = [
  {
    route: "/audit-log",
    reason:
      "redirect-only: it sends the request on to /activity, whose help covers it (route-sweep.spec.ts asserts the redirect)",
  },
];

/** Shrink-only. Remove an entry in the PR that writes its help; never add
 *  one (`coverage.test.ts` fails on a route not in the original list). */
export const HELP_PENDING: readonly HelpPending[] = [
  { route: "/audit", ticket: 2592 },
  { route: "/audit/[controlId]", ticket: 2592 },
  { route: "/requirements/[id]", ticket: 2592 },
];

/** Every way the routes and the help disagree, one line each; empty when
 *  every route is covered, exempt or pending, and no list entry is stale. */
export function helpCoverageProblems(
  routes: readonly string[],
  modules: readonly HelpModule[],
  exempt: readonly HelpExemption[] = HELP_EXEMPT,
  pending: readonly HelpPending[] = HELP_PENDING,
): string[] {
  const problems: string[] = [];
  const known = new Set(routes);
  const covered = (r: string) => helpModuleForPath(r, modules) !== undefined;
  const listed = new Map<string, string>();
  for (const e of exempt) listed.set(e.route, "HELP_EXEMPT");
  for (const p of pending) {
    if (listed.has(p.route)) {
      problems.push(`${p.route}: listed twice in HELP_EXEMPT/HELP_PENDING`);
    }
    listed.set(p.route, "HELP_PENDING");
  }

  for (const r of routes) {
    if (!covered(r) && !listed.has(r)) {
      problems.push(
        `${r}: no help module covers this route; add it to the routes of a file in web/help/modules/`,
      );
    }
  }
  for (const [r, list] of listed) {
    if (!known.has(r)) {
      problems.push(`${r}: ${list} names a route that does not exist`);
    } else if (covered(r)) {
      problems.push(
        `${r}: has help now; remove its stale ${list} entry in web/lib/help/coverage.ts`,
      );
    }
  }
  return problems;
}
