// Slice 144 — vitest seed coverage for the /api/tenants/[id] BFF route.
//
// The route's behavior:
//
//   * No atlas_jwt cookie  -> 401 { error }
//   * Upstream 200         -> 200 with upstream JSON body passed through
//   * Upstream 409         -> 409 with upstream JSON body passed through
//   * Upstream 403         -> 403 with upstream JSON body passed through

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

// Stub the slice 192 oauth callback module so importing
// `@/app/oauth/callback/route` doesn't drag in Next.js-server-only
// pieces during the vitest environment. We only need the cookie name.
vi.mock("@/app/oauth/callback/route", () => ({
  ATLAS_JWT_COOKIE: "atlas_jwt",
}));

import { PATCH } from "./route";

const TENANT_ID = "11111111-2222-3333-4444-555555555555";

function paramsFor(id: string): { params: Promise<{ id: string }> } {
  return { params: Promise.resolve({ id }) };
}

describe("PATCH /api/tenants/[id]", () => {
  beforeEach(() => {
    cookieStore.clear();
    vi.restoreAllMocks();
    process.env.ATLAS_HTTP_URL = "http://atlas:8080";
  });

  test("returns 401 when atlas_jwt cookie is absent", async () => {
    const req = new Request(`http://localhost/api/tenants/${TENANT_ID}`, {
      method: "PATCH",
      body: JSON.stringify({ name: "Renamed" }),
    });
    const res = await PATCH(
      req as unknown as Parameters<typeof PATCH>[0],
      paramsFor(TENANT_ID),
    );
    expect(res.status).toBe(401);
  });

  test("forwards body to upstream and passes 200 through", async () => {
    cookieStore.set("atlas_jwt", "test-jwt");
    const fetchSpy = vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(JSON.stringify({ tenant: { name: "Renamed" } }), {
        status: 200,
      }),
    );
    const req = new Request(`http://localhost/api/tenants/${TENANT_ID}`, {
      method: "PATCH",
      body: JSON.stringify({ name: "Renamed" }),
    });
    const res = await PATCH(
      req as unknown as Parameters<typeof PATCH>[0],
      paramsFor(TENANT_ID),
    );
    expect(res.status).toBe(200);
    expect(fetchSpy).toHaveBeenCalledTimes(1);
    const url = (fetchSpy.mock.calls[0]?.[0] as string) ?? "";
    expect(url).toContain(`/v1/tenants/${TENANT_ID}`);
    const init = fetchSpy.mock.calls[0]?.[1] as RequestInit;
    expect(init.method).toBe("PATCH");
    expect((init.headers as Record<string, string>).Authorization).toBe(
      "Bearer test-jwt",
    );
  });

  test("passes upstream 409 through (duplicate name)", async () => {
    cookieStore.set("atlas_jwt", "test-jwt");
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(
        JSON.stringify({ error: "another tenant already uses this name" }),
        { status: 409 },
      ),
    );
    const req = new Request(`http://localhost/api/tenants/${TENANT_ID}`, {
      method: "PATCH",
      body: JSON.stringify({ name: "Dupe" }),
    });
    const res = await PATCH(
      req as unknown as Parameters<typeof PATCH>[0],
      paramsFor(TENANT_ID),
    );
    expect(res.status).toBe(409);
  });

  test("passes upstream 403 through (non-admin)", async () => {
    cookieStore.set("atlas_jwt", "test-jwt");
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(JSON.stringify({ error: "admin or super_admin required" }), {
        status: 403,
      }),
    );
    const req = new Request(`http://localhost/api/tenants/${TENANT_ID}`, {
      method: "PATCH",
      body: JSON.stringify({ name: "ShouldFail" }),
    });
    const res = await PATCH(
      req as unknown as Parameters<typeof PATCH>[0],
      paramsFor(TENANT_ID),
    );
    expect(res.status).toBe(403);
  });
});
