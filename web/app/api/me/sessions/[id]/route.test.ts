// Slice 110 — vitest coverage for the /api/me/sessions/{id} BFF route.
//
// Behavior under test:
//
//   * No bearer cookie -> 401
//   * Bearer + atlas_session present -> upstream DELETE carries BOTH
//     `Authorization: Bearer <bearer>` AND `Cookie: atlas_session=<value>`
//   * Bearer present, atlas_session absent -> upstream DELETE carries
//     ONLY the bearer (no Cookie header)

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
import { DELETE } from "./route";

const ctx = (id: string) => ({ params: Promise.resolve({ id }) });

describe("DELETE /api/me/sessions/{id}", () => {
  beforeEach(() => {
    cookieStore.clear();
    vi.restoreAllMocks();
    process.env.ATLAS_HTTP_URL = "http://atlas:8080";
  });

  test("returns 401 when bearer cookie is absent", async () => {
    const req = new Request("http://localhost/api/me/sessions/sess-abc", {
      method: "DELETE",
    });
    const res = await DELETE(req, ctx("sess-abc"));
    expect(res.status).toBe(401);
  });

  test("forwards both bearer and atlas_session cookie when both present", async () => {
    cookieStore.set(SESSION_COOKIE, "test-bearer-token");
    cookieStore.set(OIDC_SESSION_COOKIE, "test-atlas-session-id");
    const fetchSpy = vi
      .spyOn(globalThis, "fetch")
      .mockResolvedValueOnce(new Response(null, { status: 204 }));
    const req = new Request("http://localhost/api/me/sessions/sess-abc", {
      method: "DELETE",
    });
    const res = await DELETE(req, ctx("sess-abc"));
    expect(res.status).toBe(204);
    const call = fetchSpy.mock.calls[0];
    expect(call[1]?.method).toBe("DELETE");
    const headers = call[1]?.headers as Record<string, string> | undefined;
    expect(headers?.Authorization).toBe("Bearer test-bearer-token");
    expect(headers?.Cookie).toBe("atlas_session=test-atlas-session-id");
    // Confirm the upstream URL targets the right path with id escaped.
    expect(call[0]).toContain("/v1/me/sessions/sess-abc");
  });

  test("omits Cookie header when atlas_session cookie is absent", async () => {
    cookieStore.set(SESSION_COOKIE, "test-bearer-token");
    const fetchSpy = vi
      .spyOn(globalThis, "fetch")
      .mockResolvedValueOnce(new Response(null, { status: 204 }));
    const req = new Request("http://localhost/api/me/sessions/sess-abc", {
      method: "DELETE",
    });
    await DELETE(req, ctx("sess-abc"));
    const call = fetchSpy.mock.calls[0];
    const headers = call[1]?.headers as Record<string, string> | undefined;
    expect(headers?.Authorization).toBe("Bearer test-bearer-token");
    expect(headers?.Cookie).toBeUndefined();
  });
});
