// OE-549 — regression coverage for the server-side auth-gate probes
// under a CONTAINERIZED topology, which is the configuration the old
// Host-header self-fetch broke on and neither `next dev` nor CI covered.
//
// What the shipped container actually looks like (observed on the repro
// instance: compose project `security-atlas`, web published `3001:3000`,
// image `security-atlas/web:1.18.0`):
//
//   * The request Host header is `localhost:3001` — the EXTERNAL
//     published port. Nothing listens on 3001 inside the container.
//   * `next-server` binds the CONTAINER IP (`/proc/net/tcp` line
//     `07001CAC:0BB8` = 172.28.0.7:3000), NOT 0.0.0.0 and NOT loopback —
//     so a self-fetch to `localhost:3000` is refused too.
//   * `http://atlas:8080` (ATLAS_HTTP_URL) is reachable and healthy.
//
// The `containerNetwork()` fetch stub below encodes exactly that: the
// atlas base answers, EVERY other origin throws the same
// `TypeError: fetch failed` / `ECONNREFUSED` Node emits. Under the
// pre-OE-549 code the admin + audit-log gates fetched
// `http://localhost:3001/api/admin/me` and that error escaped the server
// component, producing the 500 "This page couldn't load" (Next digest
// 3227098399) on every `/admin/*` page. Under this stub, these tests are
// red on the old code and green on the new.
//
// `next dev` and CI both bind 0.0.0.0 with a matching Host:port, so a
// stub that lets the app origin answer would pass either way — the
// refusal is the whole point of the harness.

import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import {
  emptyAdminMe,
  fetchAdminMe,
  fetchDemoSeedEnabled,
  fetchEnabledModules,
  fetchSelfProfile,
  normalizeAdminMe,
  normalizeEnabledModules,
} from "./self.server";

// The internal service address the compose sets. Stable regardless of the
// published port, the reverse proxy, or the server's bind address.
const ATLAS_BASE = "http://atlas:8080";

// The app's own public origin as the request Host header reports it in
// the repro instance — external port 3001, nothing behind it in-container.
const EXTERNAL_ORIGIN = "http://localhost:3001";

// The app's own INTERNAL port. Still refused, because next-server bound
// the container IP rather than loopback. Both variants of the root cause.
const INTERNAL_ORIGIN = "http://localhost:3000";

/** The exact error Node's undici raises when nothing accepts the connection. */
function econnrefused(): TypeError {
  const err = new TypeError("fetch failed");
  (err as { cause?: unknown }).cause = Object.assign(
    new Error("connect ECONNREFUSED"),
    { code: "ECONNREFUSED" },
  );
  return err;
}

type Route = { status?: number; body?: unknown; text?: string };

/**
 * containerNetwork installs a `fetch` stub modelling the web container's
 * real reachability: only `http://atlas:8080` answers; every other origin
 * (including this app's own, at either the external or the internal port)
 * refuses the connection.
 *
 * Returns the list of requested URLs so a test can assert that NOTHING
 * was ever addressed to the app's own origin.
 */
function containerNetwork(routes: Record<string, Route>): { urls: string[] } {
  const urls: string[] = [];
  vi.spyOn(globalThis, "fetch").mockImplementation(
    async (input: RequestInfo | URL) => {
      const url = String(input);
      urls.push(url);
      if (!url.startsWith(ATLAS_BASE)) {
        // Anything not addressed to atlas is unreachable from inside the
        // container. This is the defect's failure mode, verbatim.
        throw econnrefused();
      }
      const route = routes[url.slice(ATLAS_BASE.length)];
      if (!route) {
        return new Response(JSON.stringify({ error: "not found" }), {
          status: 404,
          headers: { "Content-Type": "application/json" },
        });
      }
      return new Response(
        route.text !== undefined ? route.text : JSON.stringify(route.body),
        {
          status: route.status ?? 200,
          headers: { "Content-Type": "application/json" },
        },
      );
    },
  );
  return { urls };
}

/** Assert no request ever went to the app's own origin. */
function expectNoSelfFetch(urls: string[]): void {
  for (const url of urls) {
    expect(url.startsWith(EXTERNAL_ORIGIN)).toBe(false);
    expect(url.startsWith(INTERNAL_ORIGIN)).toBe(false);
    expect(url.startsWith(ATLAS_BASE)).toBe(true);
  }
}

describe("OE-549: server-side probes under a containerized topology", () => {
  beforeEach(() => {
    // Exactly what deploy/docker/docker-compose.yml sets on the web
    // service. NEXT_PUBLIC_API_BASE_URL is deliberately left unset so
    // apiBaseURL() resolves through the ATLAS_HTTP_URL server branch.
    process.env.ATLAS_HTTP_URL = ATLAS_BASE;
    delete process.env.NEXT_PUBLIC_API_BASE_URL;
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  test("admin gate resolves is_admin=true with the web port remapped (3001 external / 3000 internal)", async () => {
    const { urls } = containerNetwork({
      "/v1/me": { body: { is_admin: true, roles: ["admin"] } },
    });

    const me = await fetchAdminMe("admin-bearer");

    expect(me).toEqual({ is_admin: true, roles: ["admin"] });
    expect(urls).toEqual([`${ATLAS_BASE}/v1/me`]);
    expectNoSelfFetch(urls);
  });

  test("admin gate resolves is_admin=false for a non-admin — the gated state, not a crash", async () => {
    const { urls } = containerNetwork({
      "/v1/me": { body: { is_admin: false, roles: ["control_owner"] } },
    });

    const me = await fetchAdminMe("viewer-bearer");

    expect(me).toEqual({ is_admin: false, roles: ["control_owner"] });
    expectNoSelfFetch(urls);
  });

  test("audit-log gate resolves the widened role list (auditor / grc_engineer) in-container", async () => {
    const { urls } = containerNetwork({
      "/v1/me": { body: { is_admin: false, roles: ["auditor"] } },
    });

    const me = await fetchAdminMe("auditor-bearer");

    expect(me.is_admin).toBe(false);
    expect(me.roles).toEqual(["auditor"]);
    expectNoSelfFetch(urls);
  });

  test("demo-seed probe resolves in-container", async () => {
    const { urls } = containerNetwork({
      "/v1/admin/demo/status": { body: { enabled: true } },
    });

    await expect(fetchDemoSeedEnabled("admin-bearer")).resolves.toBe(true);
    expect(urls).toEqual([`${ATLAS_BASE}/v1/admin/demo/status`]);
    expectNoSelfFetch(urls);
  });

  test("enabled-modules probe resolves the real flags in-container (not the fail-closed empty map)", async () => {
    const { urls } = containerNetwork({
      "/v1/features/enabled": {
        body: { modules: { "oscal.export": true, "board.reporting": false } },
      },
    });

    await expect(fetchEnabledModules("any-bearer")).resolves.toEqual({
      "oscal.export": true,
      "board.reporting": false,
    });
    expectNoSelfFetch(urls);
  });

  test("topbar profile probe resolves in-container", async () => {
    const { urls } = containerNetwork({
      "/v1/me": { body: { display_name: "Ada Lovelace", email: "a@l.dev" } },
    });

    const profile = await fetchSelfProfile("any-bearer");

    expect(profile).toMatchObject({ display_name: "Ada Lovelace" });
    expectNoSelfFetch(urls);
  });

  test("no probe ever addresses the app's own origin, at either port", async () => {
    const { urls } = containerNetwork({
      "/v1/me": { body: { is_admin: true, roles: ["admin"] } },
      "/v1/admin/demo/status": { body: { enabled: false } },
      "/v1/features/enabled": { body: { modules: {} } },
    });

    await Promise.all([
      fetchAdminMe("b"),
      fetchSelfProfile("b"),
      fetchEnabledModules("b"),
      fetchDemoSeedEnabled("b"),
    ]);

    expect(urls.length).toBe(4);
    expectNoSelfFetch(urls);
  });

  test("behind a reverse proxy the probe base is unchanged (Host/x-forwarded-proto are irrelevant)", async () => {
    // A reverse-proxied deployment reports Host `atlas.example.com` and
    // `x-forwarded-proto: https`. The probes read neither, so the address
    // they use is identical to the bare-compose case above.
    const { urls } = containerNetwork({
      "/v1/me": { body: { is_admin: true, roles: [] } },
    });

    await expect(fetchAdminMe("b")).resolves.toEqual({
      is_admin: true,
      roles: [],
    });
    expect(urls).toEqual([`${ATLAS_BASE}/v1/me`]);
  });
});

describe("OE-549: fail-closed posture on every failure mode", () => {
  beforeEach(() => {
    process.env.ATLAS_HTTP_URL = ATLAS_BASE;
    delete process.env.NEXT_PUBLIC_API_BASE_URL;
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  test("transport failure denies instead of throwing (this is what used to 500 the page)", async () => {
    vi.spyOn(globalThis, "fetch").mockRejectedValue(econnrefused());

    // The load-bearing assertion: `resolves`, not `rejects`. The
    // pre-OE-549 admin + audit-log gates let this error escape the server
    // component, which is what rendered "This page couldn't load".
    await expect(fetchAdminMe("b")).resolves.toEqual(emptyAdminMe());
    await expect(fetchSelfProfile("b")).resolves.toBeNull();
    await expect(fetchEnabledModules("b")).resolves.toEqual({});
    await expect(fetchDemoSeedEnabled("b")).resolves.toBe(false);
  });

  test("upstream 403 denies (non-admin reaching an admin-gated upstream)", async () => {
    containerNetwork({
      "/v1/me": { status: 403, body: { error: "forbidden" } },
      "/v1/admin/demo/status": { status: 403, body: { error: "forbidden" } },
    });

    await expect(fetchAdminMe("b")).resolves.toEqual(emptyAdminMe());
    await expect(fetchDemoSeedEnabled("b")).resolves.toBe(false);
  });

  test("upstream 401 / 5xx deny", async () => {
    containerNetwork({ "/v1/me": { status: 401, body: {} } });
    await expect(fetchAdminMe("b")).resolves.toEqual(emptyAdminMe());

    vi.restoreAllMocks();
    containerNetwork({ "/v1/me": { status: 503, body: {} } });
    await expect(fetchAdminMe("b")).resolves.toEqual(emptyAdminMe());
  });

  test("non-JSON 200 body denies", async () => {
    containerNetwork({ "/v1/me": { text: "<html>gateway</html>" } });
    await expect(fetchAdminMe("b")).resolves.toEqual(emptyAdminMe());
  });

  test("normalizeAdminMe never admits on a soft-truthy is_admin", () => {
    // Slice 130 P0-A3 semantics, preserved verbatim by the extraction.
    expect(normalizeAdminMe({ is_admin: "true" }).is_admin).toBe(false);
    expect(normalizeAdminMe({ is_admin: 1 }).is_admin).toBe(false);
    expect(normalizeAdminMe({ is_admin: true }).is_admin).toBe(true);
  });

  test("normalizeAdminMe collapses a missing / non-array / dirty roles field", () => {
    expect(normalizeAdminMe({ is_admin: false }).roles).toEqual([]);
    expect(normalizeAdminMe({ is_admin: false, roles: null }).roles).toEqual(
      [],
    );
    expect(
      normalizeAdminMe({ is_admin: false, roles: "auditor" }).roles,
    ).toEqual([]);
    expect(
      normalizeAdminMe({ is_admin: false, roles: ["auditor", 7, null] }).roles,
    ).toEqual(["auditor"]);
    expect(normalizeAdminMe(null)).toEqual(emptyAdminMe());
    expect(normalizeAdminMe("nope")).toEqual(emptyAdminMe());
  });

  test("normalizeEnabledModules collapses anything that is not a modules object", () => {
    expect(normalizeEnabledModules(null)).toEqual({});
    expect(normalizeEnabledModules({})).toEqual({});
    expect(normalizeEnabledModules({ modules: null })).toEqual({});
    expect(normalizeEnabledModules({ modules: "on" })).toEqual({});
    expect(normalizeEnabledModules({ modules: { a: true } })).toEqual({
      a: true,
    });
  });

  test("demo-seed probe admits only on a literal enabled:true", async () => {
    containerNetwork({ "/v1/admin/demo/status": { body: { enabled: "yes" } } });
    await expect(fetchDemoSeedEnabled("b")).resolves.toBe(false);

    vi.restoreAllMocks();
    containerNetwork({ "/v1/admin/demo/status": { body: {} } });
    await expect(fetchDemoSeedEnabled("b")).resolves.toBe(false);
  });

  test("probes forward the bearer as an Authorization header and never cache", async () => {
    const seen: RequestInit[] = [];
    vi.spyOn(globalThis, "fetch").mockImplementation(
      async (_input: RequestInfo | URL, init?: RequestInit) => {
        seen.push(init ?? {});
        return new Response(JSON.stringify({ is_admin: true, roles: [] }), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        });
      },
    );

    await fetchAdminMe("secret-bearer");

    expect(seen[0].headers).toEqual({
      Authorization: "Bearer secret-bearer",
    });
    // Invariant #6: the answer is bearer-dependent, so it must never
    // enter the Next.js data cache.
    expect(seen[0].cache).toBe("no-store");
  });
});
