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
import { forwardJSON, noStore } from "./bff";

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
