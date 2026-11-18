// #2156 — help coverage: every authenticated route resolves to a help
// module, is exempt with a reason, or waits on an open ticket.

import { join, resolve } from "node:path";
import { describe, expect, it } from "vitest";

import {
  HELP_EXEMPT,
  HELP_PENDING,
  discoverAuthedRoutes,
  helpCoverageProblems,
  readHelpModules,
} from "./coverage";
import { HELP_MODULES } from "./modules";

const WEB_ROOT = resolve(__dirname, "../..");
const APP_DIR = join(WEB_ROOT, "app");
const FIXTURE_APP_DIR = join(__dirname, "__fixtures__", "app");
const HELP_DIR = join(WEB_ROOT, "help", "modules");

describe("helpCoverageProblems", () => {
  const fixtureRoutes = discoverAuthedRoutes(FIXTURE_APP_DIR, ["(authed)"]);

  it("finds both fixture routes", () => {
    expect(fixtureRoutes).toEqual(["/controls", "/no-help-fixture"]);
  });

  it("fails on the fixture route that has no help", () => {
    expect(helpCoverageProblems(fixtureRoutes, HELP_MODULES, [], [])).toEqual([
      "/no-help-fixture: no help module covers this route; add it to the routes of a file in web/help/modules/",
    ]);
  });

  it("passes the fixture route once a ticket is pending for it", () => {
    expect(
      helpCoverageProblems(
        fixtureRoutes,
        HELP_MODULES,
        [],
        [{ route: "/no-help-fixture", ticket: 1 }],
      ),
    ).toEqual([]);
  });

  it("fails a pending entry whose route has help", () => {
    expect(
      helpCoverageProblems(
        fixtureRoutes,
        HELP_MODULES,
        [{ route: "/no-help-fixture", reason: "fixture" }],
        [{ route: "/controls", ticket: 1 }],
      ),
    ).toEqual([
      "/controls: has help now; remove its stale HELP_PENDING entry in web/lib/help/coverage.ts",
    ]);
  });

  it("fails an entry that names no route, and a route listed twice", () => {
    expect(
      helpCoverageProblems(
        fixtureRoutes,
        HELP_MODULES,
        [
          { route: "/no-help-fixture", reason: "fixture" },
          { route: "/gone", reason: "fixture" },
        ],
        [{ route: "/no-help-fixture", ticket: 1 }],
      ),
    ).toEqual([
      "/no-help-fixture: listed twice in HELP_EXEMPT/HELP_PENDING",
      "/gone: HELP_EXEMPT names a route that does not exist",
    ]);
  });
});

describe("the app's routes", () => {
  const routes = discoverAuthedRoutes(APP_DIR);

  it("all have help, an exemption or a pending ticket", () => {
    expect(routes.length).toBeGreaterThan(50);
    expect(helpCoverageProblems(routes, HELP_MODULES)).toEqual([]);
  });

  it("read the same help from disk as the app bundles", () => {
    const byId = (ms: readonly { id: string }[]) =>
      [...ms].sort((a, b) => (a.id < b.id ? -1 : 1));
    expect(byId(readHelpModules(HELP_DIR))).toEqual(byId(HELP_MODULES));
  });

  // Shrink-only: the routes that had no help when the check landed.
  // Never add one here; write its help instead.
  it("only ever drops pending entries", () => {
    const original = ["/audit", "/audit/[controlId]", "/requirements/[id]"];
    for (const p of HELP_PENDING) expect(original).toContain(p.route);
  });

  it("gives every exemption a reason", () => {
    for (const e of HELP_EXEMPT) expect(e.reason.length).toBeGreaterThan(20);
  });

  // A closed ticket with its entries still here means the help was never
  // written, or was and the entry was left behind. Needs a token to read
  // the private repo's issues; CI passes GH_TOKEN to this job. It skips
  // only outside CI: in CI a missing token fails the request, not skips.
  const token = process.env.GH_TOKEN ?? process.env.GITHUB_TOKEN;
  const tickets = [...new Set(HELP_PENDING.map((p) => p.ticket))];
  it.skipIf(!token && !process.env.CI).each(tickets)(
    "pending ticket #%i is open",
    async (ticket) => {
      const res = await fetch(
        `https://api.github.com/repos/mgoodric/security-atlas/issues/${ticket}`,
        {
          headers: {
            Authorization: `Bearer ${token}`,
            Accept: "application/vnd.github+json",
          },
        },
      );
      expect(res.status).toBe(200);
      const issue = (await res.json()) as { state: string };
      expect(issue.state).toBe("open");
    },
  );
});
