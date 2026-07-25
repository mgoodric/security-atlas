// Slice 660 — vitest coverage for the /api/features/enabled BFF route.
//
// Behavior contract:
//   * No session cookie  -> 401 { modules: {} }, no upstream fetch.
//   * Upstream 200        -> 200 { modules } forwarded.
//   * Upstream non-200    -> 200 { modules: {} } (fail-closed; nav hides).
//   * Upstream network err -> 200 { modules: {} } (fail-closed).
//   * Upstream 401        -> 401 { modules: {} }.
//   * Bearer cookie is forwarded as the Authorization header (never to client).

import { beforeEach, describe, expect, test, vi } from "vitest";

import { mockNextServer } from "../../../../lib/test-utils/next-mocks";

const cookieStore = new Map<string, string>();

vi.mock("next/headers", () => ({
  cookies: async () => ({
    get: (name: string) =>
      cookieStore.has(name) ? { value: cookieStore.get(name) } : undefined,
  }),
}));

vi.mock("next/server", () => mockNextServer());

import { ATLAS_JWT_COOKIE } from "@/lib/auth";
import { GET } from "./route";

describe("GET /api/features/enabled", () => {
  beforeEach(() => {
    cookieStore.clear();
    vi.restoreAllMocks();
    process.env.ATLAS_HTTP_URL = "http://atlas:8080";
  });

  test("401 { modules: {} } when no session cookie; no upstream call", async () => {
    const fetchSpy = vi.spyOn(globalThis, "fetch");
    const res = await GET();
    expect(res.status).toBe(401);
    const body = (await res.json()) as { modules: Record<string, boolean> };
    expect(body.modules).toEqual({});
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  test("forwards upstream 200 modules and the bearer header", async () => {
    cookieStore.set(ATLAS_JWT_COOKIE, "test-bearer");
    const fetchSpy = vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(
        JSON.stringify({
          modules: { "oscal.export": false, "board.reporting": true },
        }),
        { status: 200, headers: { "Content-Type": "application/json" } },
      ),
    );

    const res = await GET();
    expect(res.status).toBe(200);
    const body = (await res.json()) as { modules: Record<string, boolean> };
    expect(body.modules).toEqual({
      "oscal.export": false,
      "board.reporting": true,
    });

    // Bearer forwarded as Authorization header to the platform.
    const call = fetchSpy.mock.calls[0];
    expect(String(call[0])).toContain("/v1/features/enabled");
    const headers = (call[1] as RequestInit).headers as Record<string, string>;
    expect(headers.Authorization).toBe("Bearer test-bearer");
  });

  test("fail-closed { modules: {} } on upstream 500", async () => {
    cookieStore.set(ATLAS_JWT_COOKIE, "test-bearer");
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response("boom", { status: 500 }),
    );
    const res = await GET();
    expect(res.status).toBe(200);
    const body = (await res.json()) as { modules: Record<string, boolean> };
    expect(body.modules).toEqual({});
  });

  test("propagates 401 with empty modules on upstream 401", async () => {
    cookieStore.set(ATLAS_JWT_COOKIE, "stale-bearer");
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response("{}", { status: 401 }),
    );
    const res = await GET();
    expect(res.status).toBe(401);
    const body = (await res.json()) as { modules: Record<string, boolean> };
    expect(body.modules).toEqual({});
  });

  test("fail-closed on a network error", async () => {
    cookieStore.set(ATLAS_JWT_COOKIE, "test-bearer");
    vi.spyOn(globalThis, "fetch").mockRejectedValueOnce(
      new Error("ECONNREFUSED"),
    );
    const res = await GET();
    expect(res.status).toBe(200);
    const body = (await res.json()) as { modules: Record<string, boolean> };
    expect(body.modules).toEqual({});
  });

  test("fail-closed { modules: {} } when upstream body lacks a modules field", async () => {
    cookieStore.set(ATLAS_JWT_COOKIE, "test-bearer");
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(JSON.stringify({ unexpected: true }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );
    const res = await GET();
    expect(res.status).toBe(200);
    const body = (await res.json()) as { modules: Record<string, boolean> };
    expect(body.modules).toEqual({});
  });
});
