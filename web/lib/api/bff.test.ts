// Slice 069 — vitest seed coverage for lib/api/bff.ts.
//
// `forwardJSON` is the audit-workspace BFF's shared upstream-proxy
// helper. The behaviors it guarantees (verified here):
//
//   * No session cookie  -> 401 { error: "unauthenticated" }, no upstream call
//   * Session cookie set -> forwards `Authorization: Bearer <cookie>`
//   * Upstream status code passes through verbatim
//   * Upstream JSON body passes through verbatim
//   * GET by default; jsonBody flips to POST + Content-Type: application/json
//
// These are unit tests over `forwardJSON` only — the route handlers that
// call it have their own test in app/api/admin/me/route.test.ts.

import { beforeEach, describe, expect, test, vi } from "vitest";
import { mockNextServer } from "../../lib/test-utils/next-mocks";
import { TEST_BEARER_TOKEN } from "../../lib/test-utils/test-tokens";

// `next/headers` and `next/server` only run inside a Next.js request
// context. We mock both so the helper is testable in plain Node. The
// mocks are hoisted by Vitest before the module under test imports.
const cookieStore = new Map<string, string>();

vi.mock("next/headers", () => ({
  cookies: async () => ({
    get: (name: string) =>
      cookieStore.has(name) ? { value: cookieStore.get(name) } : undefined,
  }),
}));

vi.mock("next/server", () => mockNextServer());

import { ATLAS_JWT_COOKIE } from "../auth";
import {
  forwardJSON,
  forwardMultipart,
  noStore,
  rejectUnsafeSegment,
  SEGMENT,
  UPSTREAM_5XX_BODY,
  upstreamBody,
} from "./bff";

describe("upstreamBody (#2704)", () => {
  test.each([500, 502, 503, 599])("%i body is replaced", (status) => {
    expect(upstreamBody(status, "panic: runtime error")).toBe(
      UPSTREAM_5XX_BODY,
    );
  });
  test.each([200, 201, 400, 404, 409, 429, 499])(
    "%i body passes through",
    (status) => {
      expect(upstreamBody(status, '{"error":"x"}')).toBe('{"error":"x"}');
    },
  );
});

describe("forwardMultipart (#2704)", () => {
  beforeEach(() => {
    cookieStore.clear();
    vi.restoreAllMocks();
    process.env.ATLAS_HTTP_URL = "http://atlas:8080";
  });

  test("relays a 4xx body and replaces a 5xx body", async () => {
    cookieStore.set(ATLAS_JWT_COOKIE, TEST_BEARER_TOKEN);
    const fetchSpy = vi.spyOn(globalThis, "fetch");
    fetchSpy.mockResolvedValueOnce(
      new Response('{"error":"bad sheet"}', { status: 400 }),
    );
    const bad = await forwardMultipart("/v1/imports", new FormData());
    expect(bad.status).toBe(400);
    expect(await bad.json()).toEqual({ error: "bad sheet" });

    fetchSpy.mockResolvedValueOnce(
      new Response("open /var/atlas/x: permission denied", { status: 500 }),
    );
    const broke = await forwardMultipart("/v1/imports", new FormData());
    expect(broke.status).toBe(500);
    expect(await broke.text()).toBe(UPSTREAM_5XX_BODY);
  });
});

describe("forwardJSON", () => {
  beforeEach(() => {
    cookieStore.clear();
    vi.restoreAllMocks();
    delete process.env.ATLAS_HTTP_URL;
    delete process.env.NEXT_PUBLIC_API_BASE_URL;
    process.env.ATLAS_HTTP_URL = "http://atlas:8080";
  });

  test("returns 401 with { error } when session cookie is absent", async () => {
    const fetchSpy = vi.spyOn(globalThis, "fetch");
    const res = await forwardJSON("/v1/audit/notes");
    expect(res.status).toBe(401);
    const body = (await res.json()) as { error?: string };
    expect(body.error).toBe("unauthenticated");
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  test("forwards Authorization: Bearer <cookie> to upstream on GET", async () => {
    cookieStore.set(ATLAS_JWT_COOKIE, TEST_BEARER_TOKEN);

    let capturedURL = "";
    let capturedInit: RequestInit | undefined;
    const fetchSpy = vi
      .spyOn(globalThis, "fetch")
      .mockImplementation(
        async (input: RequestInfo | URL, init?: RequestInit) => {
          capturedURL = typeof input === "string" ? input : input.toString();
          capturedInit = init;
          return new Response(JSON.stringify({ ok: true }), {
            status: 200,
            headers: { "Content-Type": "application/json" },
          });
        },
      );

    const res = await forwardJSON("/v1/audit/notes");

    expect(fetchSpy).toHaveBeenCalledOnce();
    expect(capturedURL).toBe("http://atlas:8080/v1/audit/notes");
    expect(capturedInit?.method).toBe("GET");
    const headers = capturedInit?.headers as Record<string, string>;
    expect(headers.Authorization).toBe(`Bearer ${TEST_BEARER_TOKEN}`);
    expect(res.status).toBe(200);
    const body = (await res.json()) as { ok: boolean };
    expect(body.ok).toBe(true);
  });

  test("passes Retry-After through on a 429 (#2618)", async () => {
    cookieStore.set(ATLAS_JWT_COOKIE, TEST_BEARER_TOKEN);
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response('{"error":"too many evaluations; retry later"}', {
        status: 429,
        headers: { "Content-Type": "application/json", "Retry-After": "9" },
      }),
    );

    const res = await forwardJSON("/v1/controls/x/evaluate", {
      method: "POST",
    });
    expect(res.status).toBe(429);
    expect(res.headers.get("Retry-After")).toBe("9");
  });

  test("passes upstream non-2xx status through verbatim", async () => {
    cookieStore.set(ATLAS_JWT_COOKIE, TEST_BEARER_TOKEN);
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response('{"error":"forbidden"}', {
        status: 403,
        headers: { "Content-Type": "application/json" },
      }),
    );

    const res = await forwardJSON("/v1/audit/notes");
    expect(res.status).toBe(403);
    const body = (await res.json()) as { error: string };
    expect(body.error).toBe("forbidden");
  });

  // #2704 — a 5xx body is the server's own failure; it never reaches the
  // wire, whatever a handler wrote there. The status and Retry-After do.
  test("replaces an upstream 5xx body with a generic one", async () => {
    cookieStore.set(ATLAS_JWT_COOKIE, TEST_BEARER_TOKEN);
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response('{"error":"pq: relation \\"secret_table\\" missing"}', {
        status: 503,
        headers: { "Content-Type": "application/json", "Retry-After": "5" },
      }),
    );

    const res = await forwardJSON("/v1/audit/notes");
    expect(res.status).toBe(503);
    expect(res.headers.get("Retry-After")).toBe("5");
    const text = await res.text();
    expect(text).toBe(UPSTREAM_5XX_BODY);
    expect(text).not.toContain("secret_table");
  });

  test("sends POST with Content-Type and serialized body when jsonBody set", async () => {
    cookieStore.set(ATLAS_JWT_COOKIE, TEST_BEARER_TOKEN);

    let capturedInit: RequestInit | undefined;
    vi.spyOn(globalThis, "fetch").mockImplementation(
      async (_input: RequestInfo | URL, init?: RequestInit) => {
        capturedInit = init;
        return new Response('{"created":true}', {
          status: 201,
          headers: { "Content-Type": "application/json" },
        });
      },
    );

    const res = await forwardJSON("/v1/audit/notes", {
      method: "POST",
      jsonBody: { body: "hello" },
    });

    expect(capturedInit?.method).toBe("POST");
    const headers = capturedInit?.headers as Record<string, string>;
    expect(headers["Content-Type"]).toBe("application/json");
    expect(capturedInit?.body).toBe(JSON.stringify({ body: "hello" }));
    expect(res.status).toBe(201);
  });

  // Slice 746 AC-4 — DEFAULT-PATH REGRESSION SENTINEL.
  //
  // `forwardJSON` must NOT set Cache-Control on its own. The saved-views
  // no-store fix is opt-in (the route wraps the response in `noStore`);
  // every other cache-friendly BFF GET route relies on `forwardJSON`
  // leaving caching to the browser/Next defaults. If a future change
  // pushed `no-store` into `forwardJSON` unconditionally (the P0
  // anti-criterion), this test fails.
  test("forwardJSON does NOT set Cache-Control on its response (default path unchanged)", async () => {
    cookieStore.set(ATLAS_JWT_COOKIE, TEST_BEARER_TOKEN);
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response('{"ok":true}', {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );

    const res = await forwardJSON("/v1/audit/notes");
    expect(res.headers.get("Cache-Control")).toBeNull();
    expect(res.headers.get("Pragma")).toBeNull();
  });
});

describe("noStore (slice 746)", () => {
  test("adds Cache-Control: no-store + Pragma while preserving status, body, headers", async () => {
    const wrapped = noStore(
      new Response('{"views":[]}', {
        status: 200,
        statusText: "OK",
        headers: { "Content-Type": "application/json", "X-Trace": "abc" },
      }),
    );

    expect(wrapped.headers.get("Cache-Control")).toBe("no-store");
    expect(wrapped.headers.get("Pragma")).toBe("no-cache");
    expect(wrapped.status).toBe(200);
    // Existing headers survive; body passes through verbatim.
    expect(wrapped.headers.get("Content-Type")).toBe("application/json");
    expect(wrapped.headers.get("X-Trace")).toBe("abc");
    const body = (await wrapped.json()) as { views: unknown[] };
    expect(body.views).toEqual([]);
  });

  test("preserves a non-2xx status (does not coerce to 200)", () => {
    const wrapped = noStore(
      new Response('{"error":"forbidden"}', { status: 403 }),
    );
    expect(wrapped.status).toBe(403);
    expect(wrapped.headers.get("Cache-Control")).toBe("no-store");
  });

  test("re-wraps a null-body 204 without throwing (saved-views DELETE path)", () => {
    // The DELETE upstream returns 204; undici rejects a 204 Response with a
    // body, so the wrapper must pass null for null-body statuses.
    const wrapped = noStore(new Response(null, { status: 204 }));
    expect(wrapped.status).toBe(204);
    expect(wrapped.headers.get("Cache-Control")).toBe("no-store");
    expect(wrapped.body).toBeNull();
  });
});

describe("rejectUnsafeSegment (#2558)", () => {
  const UUID = "00000000-0000-0000-0000-000000002558";

  test.each([
    ["slug", "risk.themes"],
    ["slug", "evidence_freshness_pct"],
    ["slugOrUUID", "third-party"],
    ["slugOrUUID", UUID],
    ["token", "aB3_-xYz"],
    ["credentialID", `key_${UUID}`],
    ["credentialID", UUID],
    ["code", "IAC-06.1"],
    ["code", "hipaa:2013:164.308(a)(1)(ii)(A)"],
  ] as const)("SEGMENT.%s admits %s", (allow, v) => {
    expect(rejectUnsafeSegment({ p: v }, SEGMENT[allow])).toBeNull();
  });

  const unsafe = ["", ".", "..", "a/b", "a\\b", "a%2Fb", "%2E%2E"];
  for (const allow of Object.keys(SEGMENT) as (keyof typeof SEGMENT)[]) {
    test.each(unsafe)(`SEGMENT.${allow} refuses %j`, async (v) => {
      const res = rejectUnsafeSegment({ p: v }, SEGMENT[allow]);
      expect(res?.status).toBe(400);
      expect(await res?.json()).toEqual({
        error: "p is not a valid path segment",
      });
    });
  }

  test("a closed set admits its members and names them on refusal", async () => {
    const kinds: ReadonlySet<string> = new Set(["current", "target"]);
    expect(rejectUnsafeSegment({ kind: "target" }, kinds)).toBeNull();
    const res = rejectUnsafeSegment({ kind: ".." }, kinds);
    expect(res?.status).toBe(400);
    expect(await res?.json()).toEqual({
      error: "kind must be one of current, target",
    });
  });

  test("checks every param it is given", () => {
    const res = rejectUnsafeSegment({ a: "ok", b: ".." }, SEGMENT.slug);
    expect(res?.status).toBe(400);
  });
});
