// Slice 069 + Slice 130 — vitest coverage for the /api/admin/me BFF route.
//
// Slice 060's original behavior (preserved, now via /v1/me upstream):
//
//   * No session cookie  -> 401 { is_admin: false, roles: [] }
//   * Upstream 200       -> 200 { is_admin, roles } from upstream
//   * Upstream 401       -> 401 { is_admin: false, roles: [] }
//   * Upstream 403       -> 200 { is_admin: false, roles: [] }
//   * Upstream 5xx       -> 502 { is_admin: false, roles: [], error }
//
// Slice 130 additions (the four cases in decisions log D6):
//
//   1. Upstream 200 with { is_admin: true, roles: ["admin","auditor"] }
//      -> the full role list flows through additively.
//   2. Upstream 200 with { is_admin: false, roles: ["auditor"] }
//      -> non-admin auditor case (the slice's whole point).
//   3. Upstream 200 with { is_admin: true } (legacy shape, no `roles`)
//      -> `roles` defaults to [] (fail-closed P0-A3).
//   4. Upstream error paths default `roles: []` in every response
//      -> already covered by the slice-060 cases above, asserted here too.
//
// Regression sentinels — without these tests, a future refactor that
// flipped the `roles ?? []` default to "permissive" would not fail.

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

describe("GET /api/admin/me", () => {
  beforeEach(() => {
    cookieStore.clear();
    vi.restoreAllMocks();
    process.env.ATLAS_HTTP_URL = "http://atlas:8080";
  });

  test("returns 401 { is_admin: false, roles: [] } when session cookie is absent", async () => {
    const fetchSpy = vi.spyOn(globalThis, "fetch");
    const res = await GET();
    expect(res.status).toBe(401);
    const body = (await res.json()) as { is_admin: boolean; roles: string[] };
    expect(body.is_admin).toBe(false);
    expect(body.roles).toEqual([]);
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  test("slice 130 D6 case 1: upstream 200 { is_admin: true, roles: [admin, auditor] } flows through", async () => {
    cookieStore.set(ATLAS_JWT_COOKIE, "test-admin-bearer");
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(
        JSON.stringify({
          user_id: "u-1",
          tenant_id: "t-1",
          is_admin: true,
          roles: ["admin", "auditor"],
        }),
        {
          status: 200,
          headers: { "Content-Type": "application/json" },
        },
      ),
    );

    const res = await GET();
    expect(res.status).toBe(200);
    const body = (await res.json()) as { is_admin: boolean; roles: string[] };
    expect(body.is_admin).toBe(true);
    expect(body.roles).toEqual(["admin", "auditor"]);
  });

  test("slice 130 D6 case 2: upstream 200 { is_admin: false, roles: [auditor] } — non-admin auditor flows through", async () => {
    cookieStore.set(ATLAS_JWT_COOKIE, "test-auditor-bearer");
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(
        JSON.stringify({
          user_id: "u-2",
          tenant_id: "t-1",
          is_admin: false,
          roles: ["auditor"],
        }),
        {
          status: 200,
          headers: { "Content-Type": "application/json" },
        },
      ),
    );

    const res = await GET();
    expect(res.status).toBe(200);
    const body = (await res.json()) as { is_admin: boolean; roles: string[] };
    expect(body.is_admin).toBe(false);
    expect(body.roles).toEqual(["auditor"]);
  });

  test("slice 130 D6 case 3: upstream 200 with no `roles` field defaults roles to [] (P0-A3 fail-closed)", async () => {
    cookieStore.set(ATLAS_JWT_COOKIE, "test-legacy-bearer");
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(
        JSON.stringify({
          user_id: "u-3",
          tenant_id: "t-1",
          is_admin: true,
        }),
        {
          status: 200,
          headers: { "Content-Type": "application/json" },
        },
      ),
    );

    const res = await GET();
    expect(res.status).toBe(200);
    const body = (await res.json()) as { is_admin: boolean; roles: string[] };
    expect(body.is_admin).toBe(true);
    expect(body.roles).toEqual([]);
  });

  test("upstream 200 with non-JSON body degrades closed to { is_admin: false, roles: [] }", async () => {
    cookieStore.set(ATLAS_JWT_COOKIE, "test-bearer");
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response("not-json", { status: 200 }),
    );

    const res = await GET();
    expect(res.status).toBe(200);
    const body = (await res.json()) as { is_admin: boolean; roles: string[] };
    expect(body.is_admin).toBe(false);
    expect(body.roles).toEqual([]);
  });

  test("upstream 200 with roles containing non-string entries filters them out", async () => {
    cookieStore.set(ATLAS_JWT_COOKIE, "test-bearer");
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(
        JSON.stringify({
          is_admin: false,
          roles: ["auditor", 42, null, "viewer"],
        }),
        {
          status: 200,
          headers: { "Content-Type": "application/json" },
        },
      ),
    );

    const res = await GET();
    expect(res.status).toBe(200);
    const body = (await res.json()) as { is_admin: boolean; roles: string[] };
    expect(body.roles).toEqual(["auditor", "viewer"]);
  });

  test("returns 200 { is_admin: false, roles: [] } when upstream returns 403", async () => {
    cookieStore.set(ATLAS_JWT_COOKIE, "test-viewer-bearer");
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response('{"error":"forbidden"}', { status: 403 }),
    );

    const res = await GET();
    expect(res.status).toBe(200);
    const body = (await res.json()) as { is_admin: boolean; roles: string[] };
    expect(body.is_admin).toBe(false);
    expect(body.roles).toEqual([]);
  });

  test("returns 401 { is_admin: false, roles: [] } when upstream returns 401", async () => {
    cookieStore.set(ATLAS_JWT_COOKIE, "test-expired-bearer");
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response("", { status: 401 }),
    );

    const res = await GET();
    expect(res.status).toBe(401);
    const body = (await res.json()) as { is_admin: boolean; roles: string[] };
    expect(body.is_admin).toBe(false);
    expect(body.roles).toEqual([]);
  });

  test("returns 502 { is_admin: false, roles: [], error } on unexpected upstream status", async () => {
    cookieStore.set(ATLAS_JWT_COOKIE, "test-bearer");
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response("", { status: 500 }),
    );

    const res = await GET();
    expect(res.status).toBe(502);
    const body = (await res.json()) as {
      is_admin: boolean;
      roles: string[];
      error?: string;
    };
    expect(body.is_admin).toBe(false);
    expect(body.roles).toEqual([]);
    expect(body.error).toContain("upstream 500");
  });

  test("BFF upstream URL is /v1/me (slice 130 D2 — not /v1/admin/credentials)", async () => {
    cookieStore.set(ATLAS_JWT_COOKIE, "test-bearer");
    const fetchSpy = vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(JSON.stringify({ is_admin: false, roles: [] }), {
        status: 200,
      }),
    );

    await GET();
    expect(fetchSpy).toHaveBeenCalledOnce();
    const calledURL = (fetchSpy.mock.calls[0]?.[0] ?? "") as string;
    expect(calledURL).toContain("/v1/me");
    expect(calledURL).not.toContain("/v1/admin/credentials");
  });

  test("BFF forwards bearer-only (P0-A1 — never atlas_session)", async () => {
    cookieStore.set(ATLAS_JWT_COOKIE, "test-bearer");
    const fetchSpy = vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(JSON.stringify({ is_admin: false, roles: [] }), {
        status: 200,
      }),
    );

    await GET();
    const init = fetchSpy.mock.calls[0]?.[1] as RequestInit | undefined;
    const headers = (init?.headers ?? {}) as Record<string, string>;
    expect(headers.Authorization).toBe("Bearer test-bearer");
    // No Cookie header — narrow-scope forwarding only (slice-110 P0-A2).
    expect(headers.Cookie).toBeUndefined();
  });
});
