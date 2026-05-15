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

vi.mock("next/server", () => {
  class NextResponse extends Response {
    static json(
      body: unknown,
      init?: { status?: number; headers?: Record<string, string> },
    ): NextResponse {
      return new NextResponse(JSON.stringify(body), {
        status: init?.status ?? 200,
        headers: {
          "Content-Type": "application/json",
          ...(init?.headers ?? {}),
        },
      });
    }
  }
  return { NextResponse };
});

import { SESSION_COOKIE } from "../auth";
import { forwardJSON } from "./bff";

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
    cookieStore.set(SESSION_COOKIE, "test-bearer-token");

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
    expect(headers.Authorization).toBe("Bearer test-bearer-token");
    expect(res.status).toBe(200);
    const body = (await res.json()) as { ok: boolean };
    expect(body.ok).toBe(true);
  });

  test("passes upstream non-2xx status through verbatim", async () => {
    cookieStore.set(SESSION_COOKIE, "test-bearer-token");
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
    cookieStore.set(SESSION_COOKIE, "test-bearer-token");

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
});
