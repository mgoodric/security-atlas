// Slice 278 — vitest coverage for the /api/admin/demo/status BFF.
//
// The BFF proxies the admin-gated GET /v1/admin/demo/status route.
// Tests cover:
//
//   - 401 without session cookie (no upstream fetch)
//   - 200 + {enabled: true} passthrough when env-var gate set
//   - 200 + {enabled: false} passthrough when env-var gate unset
//   - 403 passthrough when upstream rejects non-admin
//   - 5xx passthrough on transient upstream error

import { beforeEach, describe, expect, test, vi } from "vitest";
import { mockNextServer } from "../../../../../lib/test-utils/next-mocks";

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

describe("GET /api/admin/demo/status", () => {
  beforeEach(() => {
    cookieStore.clear();
    vi.restoreAllMocks();
    process.env.ATLAS_HTTP_URL = "http://atlas:8080";
  });

  test("returns 401 when session cookie is absent", async () => {
    const fetchSpy = vi.spyOn(globalThis, "fetch");
    const res = await GET();
    expect(res.status).toBe(401);
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  test("returns 200 with enabled=true when upstream reports gate set", async () => {
    cookieStore.set(ATLAS_JWT_COOKIE, "test-admin-bearer");
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(JSON.stringify({ enabled: true }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );

    const res = await GET();
    expect(res.status).toBe(200);
    const body = (await res.json()) as { enabled: boolean };
    expect(body.enabled).toBe(true);
  });

  test("returns 200 with enabled=false when upstream reports gate unset", async () => {
    cookieStore.set(ATLAS_JWT_COOKIE, "test-admin-bearer");
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(JSON.stringify({ enabled: false }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );

    const res = await GET();
    expect(res.status).toBe(200);
    const body = (await res.json()) as { enabled: boolean };
    expect(body.enabled).toBe(false);
  });

  test("upstream 403 propagates with same status", async () => {
    cookieStore.set(ATLAS_JWT_COOKIE, "non-admin-bearer");
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(JSON.stringify({ error: "admin role required" }), {
        status: 403,
        statusText: "Forbidden",
        headers: { "Content-Type": "application/json" },
      }),
    );

    const res = await GET();
    expect(res.status).toBe(403);
    const body = (await res.json()) as { error?: string };
    expect(body.error).toBe("admin role required");
  });

  test("upstream 5xx propagates with same status", async () => {
    cookieStore.set(ATLAS_JWT_COOKIE, "bearer");
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(JSON.stringify({ error: "platform unavailable" }), {
        status: 500,
        statusText: "Internal Server Error",
        headers: { "Content-Type": "application/json" },
      }),
    );

    const res = await GET();
    expect(res.status).toBe(500);
    const body = (await res.json()) as { error?: string };
    expect(body.error).toBe("platform unavailable");
  });
});
