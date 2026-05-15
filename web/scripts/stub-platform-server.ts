// Slice 057 — hermetic stub of the Go platform's HTTP API for the
// README-screenshot capture run.
//
// Why this exists
// ---------------
// The capture spec (`capture-readme-screenshots.spec.ts`) renders the
// real Next.js app — same React components, same shadcn/ui primitives,
// same Tailwind output that ship to production. The only thing it
// replaces is the network call between the Next BFF and the Go platform.
// This file serves that network surface from static JSON fixtures under
// `fixtures/readme-demo/`.
//
// Why not page.route()?
// ---------------------
// `page.route('**/api/**', ...)` only intercepts CLIENT-side fetches.
// The audit-workspace and board-pack pages run server-side fetches
// inside React Server Components (Next 16 RSC) — those happen inside
// the Node process and never traverse the browser network stack. A
// real HTTP stub on a real port catches BOTH client and server fetches
// cleanly. ATLAS_HTTP_URL (server-side default) + the BFF passthrough
// pattern means routing both through one stub is the natural seam.
//
// Constraints honored
// -------------------
// * stdlib only — no new dependency
// * neutral fixture data (P0-A2): no maintainer references, no real
//   tenant names, no vendor-prefixed tokens
// * deterministic: every request returns the same payload for the same
//   path; no time-dependent fields except where the fixtures encode
//   absolute ISO timestamps (which are static).

import { createServer } from "node:http";
import { readFile } from "node:fs/promises";
import { join, resolve } from "node:path";

import type { IncomingMessage, ServerResponse } from "node:http";

const FIXTURE_DIR = resolve(__dirname, "../../fixtures/readme-demo");

// Map every platform endpoint the captured pages touch to a fixture file.
// Keys are full request paths (with leading slash). Wildcards are handled
// by `matchPath` below.
const ROUTE_MAP: Record<string, string> = {
  // Identity / session
  "/v1/me": "me.json",
  "/v1/me/audit-period": "audit-period.json",

  // Dashboard endpoints (slice 040)
  "/v1/controls/drift": "dashboard-drift.json",
  "/v1/evidence/freshness": "dashboard-freshness.json",
  "/v1/risks": "dashboard-risks.json",
  "/v1/exceptions/expiring": "dashboard-upcoming.json",

  // Anchors / catalog (slice 005, sidebar counts)
  "/v1/anchors": "anchors-list.json",

  // Controls list (sidebar / browser)
  "/v1/controls": "controls-list.json",

  // Board packs (slice 043)
  "/v1/board-packs/00000000-0000-0000-0000-000000000501": "board-pack.json",
};

// Some endpoints have a parameter in the path. Match by suffix to
// catch the more specific routes first.
const PREFIX_MAP: Array<[string, string]> = [
  // Control detail page sub-endpoints (slice 041) — order matters,
  // most-specific first.
  ["/v1/controls/acme-soc2-ac-1/coverage", "control-detail.json"],
  ["/v1/controls/acme-soc2-ac-1/state", "control-state.json"],
  ["/v1/controls/acme-soc2-ac-1/effectiveness", "control-effectiveness.json"],
  [
    "/v1/controls/acme-soc2-ac-1/effective-scope",
    "control-effective-scope.json",
  ],
  // Audit workspace (slice 042)
  ["/v1/audit/controls/acme-soc2-ac-1", "audit-control.json"],
  // SCF anchor requirements (slice 005 — sidebar / catalog)
  ["/v1/anchors/scf-iac-06/requirements", "control-detail.json"],
];

function matchPath(pathname: string): string | undefined {
  if (ROUTE_MAP[pathname]) return ROUTE_MAP[pathname];
  for (const [prefix, fixture] of PREFIX_MAP) {
    if (pathname.startsWith(prefix)) return fixture;
  }
  return undefined;
}

async function loadFixture(name: string): Promise<string> {
  return readFile(join(FIXTURE_DIR, name), "utf8");
}

async function handle(
  req: IncomingMessage,
  res: ServerResponse,
): Promise<void> {
  const url = new URL(req.url ?? "/", "http://localhost");
  const pathname = url.pathname;

  // Health
  if (pathname === "/health") {
    res.writeHead(200, { "content-type": "application/json" });
    res.end('{"status":"ok"}');
    return;
  }

  const fixture = matchPath(pathname);
  if (!fixture) {
    // Unknown path: respond with a benign empty envelope. This keeps the
    // captured UI from breaking on incidental side-fetches we haven't
    // explicitly stubbed (e.g. analytics pings).
    res.writeHead(200, { "content-type": "application/json" });
    res.end("{}");
    return;
  }

  try {
    const body = await loadFixture(fixture);
    res.writeHead(200, { "content-type": "application/json" });
    res.end(body);
  } catch (err) {
    res.writeHead(500, { "content-type": "application/json" });
    res.end(JSON.stringify({ error: (err as Error).message }));
  }
}

export function startStubServer(port: number): {
  close: () => Promise<void>;
} {
  const server = createServer((req, res) => {
    handle(req, res).catch((err) => {
      res.writeHead(500, { "content-type": "application/json" });
      res.end(JSON.stringify({ error: (err as Error).message }));
    });
  });
  server.listen(port);
  return {
    close: () =>
      new Promise<void>((resolveFn, rejectFn) => {
        server.close((err) => (err ? rejectFn(err) : resolveFn()));
      }),
  };
}
