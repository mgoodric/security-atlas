// #3096 — vitest coverage for the /api/admin/demo/teardown BFF.
//
//   - 401 without session cookie (no upstream fetch)
//   - 200 passthrough on success
//   - 503 teardown_busy keeps its code and Retry-After (#3094)
//   - other 5xx propagates its status, not its body, and no Retry-After

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
import { POST } from "./route";

describe("POST /api/admin/demo/teardown", () => {
  beforeEach(() => {
    cookieStore.clear();
    vi.restoreAllMocks();
    process.env.ATLAS_HTTP_URL = "http://atlas:8080";
  });

  test("returns 401 when session cookie is absent", async () => {
    const fetchSpy = vi.spyOn(globalThis, "fetch");
    const res = await POST();
    expect(res.status).toBe(401);
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  test("returns 200 with the upstream body on success", async () => {
    cookieStore.set(ATLAS_JWT_COOKIE, "bearer");
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(JSON.stringify({ tenant_slug: "demo", status: "deleted" }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );
    const res = await POST();
    expect(res.status).toBe(200);
    expect(await res.json()).toEqual({
      tenant_slug: "demo",
      status: "deleted",
    });
  });

  test("503 teardown_busy keeps its code and Retry-After", async () => {
    cookieStore.set(ATLAS_JWT_COOKIE, "bearer");
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(
        JSON.stringify({ error: "teardown busy", code: "teardown_busy" }),
        {
          status: 503,
          statusText: "Service Unavailable",
          headers: { "Content-Type": "application/json", "Retry-After": "5" },
        },
      ),
    );
    const res = await POST();
    expect(res.status).toBe(503);
    expect(res.headers.get("Retry-After")).toBe("5");
    const body = (await res.json()) as { error?: string; code?: string };
    expect(body.code).toBe("teardown_busy");
    expect(body.error).toBe("503 Service Unavailable");
  });

  test("other 5xx propagates its status, not its body", async () => {
    cookieStore.set(ATLAS_JWT_COOKIE, "bearer");
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(JSON.stringify({ error: "pq: secret detail" }), {
        status: 500,
        statusText: "Internal Server Error",
        headers: { "Content-Type": "application/json" },
      }),
    );
    const res = await POST();
    expect(res.status).toBe(500);
    expect(res.headers.get("Retry-After")).toBeNull();
    const body = (await res.json()) as { error?: string; code?: string };
    expect(body.error).toBe("500 Internal Server Error");
    expect(body.code).toBeUndefined();
  });
});
