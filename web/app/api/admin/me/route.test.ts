// Slice 069 — vitest seed coverage for the /api/admin/me BFF route.
//
// The route's behavior (per slice 060):
//
//   * No session cookie  -> 401 { is_admin: false }
//   * Upstream 200       -> 200 { is_admin: true }
//   * Upstream 403       -> 200 { is_admin: false } (intentional: a
//                            non-admin caller is authenticated; the page
//                            renders a 403 surface, not the login page)
//   * Upstream 401       -> 401 { is_admin: false } (cookie expired)
//   * Upstream other     -> 502 { is_admin: false, error }
//
// These assertions guard the admin-layout's role-gate; a regression here
// would either leak admin UI to non-admins or bounce admins to the login
// page on a transient upstream blip.

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

import { SESSION_COOKIE } from "@/lib/auth";
import { GET } from "./route";

describe("GET /api/admin/me", () => {
  beforeEach(() => {
    cookieStore.clear();
    vi.restoreAllMocks();
    process.env.ATLAS_HTTP_URL = "http://atlas:8080";
  });

  test("returns 401 { is_admin: false } when session cookie is absent", async () => {
    const fetchSpy = vi.spyOn(globalThis, "fetch");
    const res = await GET();
    expect(res.status).toBe(401);
    const body = (await res.json()) as { is_admin: boolean };
    expect(body.is_admin).toBe(false);
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  test("returns 200 { is_admin: true } when upstream returns 200", async () => {
    cookieStore.set(SESSION_COOKIE, "test-admin-bearer");
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response("[]", { status: 200 }),
    );

    const res = await GET();
    expect(res.status).toBe(200);
    const body = (await res.json()) as { is_admin: boolean };
    expect(body.is_admin).toBe(true);
  });

  test("returns 200 { is_admin: false } when upstream returns 403", async () => {
    cookieStore.set(SESSION_COOKIE, "test-viewer-bearer");
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response('{"error":"forbidden"}', { status: 403 }),
    );

    const res = await GET();
    expect(res.status).toBe(200);
    const body = (await res.json()) as { is_admin: boolean };
    expect(body.is_admin).toBe(false);
  });

  test("returns 401 { is_admin: false } when upstream returns 401", async () => {
    cookieStore.set(SESSION_COOKIE, "test-expired-bearer");
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response("", { status: 401 }),
    );

    const res = await GET();
    expect(res.status).toBe(401);
    const body = (await res.json()) as { is_admin: boolean };
    expect(body.is_admin).toBe(false);
  });

  test("returns 502 { is_admin: false, error } on unexpected upstream status", async () => {
    cookieStore.set(SESSION_COOKIE, "test-bearer");
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response("", { status: 500 }),
    );

    const res = await GET();
    expect(res.status).toBe(502);
    const body = (await res.json()) as { is_admin: boolean; error?: string };
    expect(body.is_admin).toBe(false);
    expect(body.error).toContain("upstream 500");
  });
});
