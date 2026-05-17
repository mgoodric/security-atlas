// Slice 110 — vitest coverage for the /api/me/sessions BFF route.
//
// Behavior under test:
//
//   * No `sa_session_token` (bearer) cookie -> 401 { error }
//   * Bearer present, `atlas_session` present -> upstream fetch carries
//     BOTH `Authorization: Bearer <bearer>` AND
//     `Cookie: atlas_session=<value>` headers.
//   * Bearer present, `atlas_session` absent -> upstream fetch carries
//     ONLY `Authorization: Bearer <bearer>` (no Cookie header).
//
// Both GET (list sessions) and DELETE (revoke other sessions) share the
// same forwarding shape per slice 110 AC-2 + AC-4.

import { beforeEach, describe, expect, test, vi } from "vitest";

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
      return new NextResponse(body === null ? "null" : JSON.stringify(body), {
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

import { SESSION_COOKIE, OIDC_SESSION_COOKIE } from "@/lib/auth";
import { GET, DELETE } from "./route";

describe("GET /api/me/sessions", () => {
  beforeEach(() => {
    cookieStore.clear();
    vi.restoreAllMocks();
    process.env.ATLAS_HTTP_URL = "http://atlas:8080";
  });

  test("returns 401 when bearer cookie is absent", async () => {
    const res = await GET();
    expect(res.status).toBe(401);
    const body = (await res.json()) as { error: string };
    expect(body.error).toBeDefined();
  });

  test("forwards both bearer and atlas_session cookie when both present", async () => {
    cookieStore.set(SESSION_COOKIE, "test-bearer-token");
    cookieStore.set(OIDC_SESSION_COOKIE, "test-atlas-session-id");
    const fetchSpy = vi
      .spyOn(globalThis, "fetch")
      .mockResolvedValueOnce(
        new Response('{"sessions":[],"count":0}', { status: 200 }),
      );
    const res = await GET();
    expect(res.status).toBe(200);
    expect(fetchSpy).toHaveBeenCalledTimes(1);
    const call = fetchSpy.mock.calls[0];
    const headers = call[1]?.headers as Record<string, string> | undefined;
    expect(headers).toBeDefined();
    expect(headers?.Authorization).toBe("Bearer test-bearer-token");
    expect(headers?.Cookie).toBe("atlas_session=test-atlas-session-id");
  });

  test("omits Cookie header when atlas_session cookie is absent", async () => {
    cookieStore.set(SESSION_COOKIE, "test-bearer-token");
    const fetchSpy = vi
      .spyOn(globalThis, "fetch")
      .mockResolvedValueOnce(
        new Response('{"sessions":[],"count":0}', { status: 200 }),
      );
    await GET();
    const call = fetchSpy.mock.calls[0];
    const headers = call[1]?.headers as Record<string, string> | undefined;
    expect(headers?.Authorization).toBe("Bearer test-bearer-token");
    expect(headers?.Cookie).toBeUndefined();
  });

  test("drops malformed atlas_session cookie (header injection guard)", async () => {
    cookieStore.set(SESSION_COOKIE, "test-bearer-token");
    // Contains semicolon — should be silently dropped, no Cookie header forwarded.
    cookieStore.set(OIDC_SESSION_COOKIE, "evil;injection=1");
    const fetchSpy = vi
      .spyOn(globalThis, "fetch")
      .mockResolvedValueOnce(
        new Response('{"sessions":[],"count":0}', { status: 200 }),
      );
    await GET();
    const call = fetchSpy.mock.calls[0];
    const headers = call[1]?.headers as Record<string, string> | undefined;
    expect(headers?.Cookie).toBeUndefined();
  });
});

describe("DELETE /api/me/sessions (revoke all-others)", () => {
  beforeEach(() => {
    cookieStore.clear();
    vi.restoreAllMocks();
    process.env.ATLAS_HTTP_URL = "http://atlas:8080";
  });

  test("returns 401 when bearer cookie is absent", async () => {
    const res = await DELETE();
    expect(res.status).toBe(401);
  });

  test("forwards both bearer and atlas_session cookie when both present", async () => {
    cookieStore.set(SESSION_COOKIE, "test-bearer-token");
    cookieStore.set(OIDC_SESSION_COOKIE, "test-atlas-session-id");
    const fetchSpy = vi
      .spyOn(globalThis, "fetch")
      .mockResolvedValueOnce(
        new Response('{"revoked_count":3}', { status: 200 }),
      );
    const res = await DELETE();
    expect(res.status).toBe(200);
    const call = fetchSpy.mock.calls[0];
    expect(call[1]?.method).toBe("DELETE");
    const headers = call[1]?.headers as Record<string, string> | undefined;
    expect(headers?.Authorization).toBe("Bearer test-bearer-token");
    expect(headers?.Cookie).toBe("atlas_session=test-atlas-session-id");
  });

  test("omits Cookie header when atlas_session cookie is absent", async () => {
    cookieStore.set(SESSION_COOKIE, "test-bearer-token");
    const fetchSpy = vi
      .spyOn(globalThis, "fetch")
      .mockResolvedValueOnce(
        new Response('{"revoked_count":0}', { status: 200 }),
      );
    await DELETE();
    const call = fetchSpy.mock.calls[0];
    const headers = call[1]?.headers as Record<string, string> | undefined;
    expect(headers?.Authorization).toBe("Bearer test-bearer-token");
    expect(headers?.Cookie).toBeUndefined();
  });
});
