// #2760 — vitest coverage for DELETE /api/admin/sso.
//
// The BFF proxies DELETE to the platform's /v1/admin/sso with the session
// bearer and no body (the platform derives the tenant from the credential),
// and passes the upstream counts and status (404 unset, 409 last admin)
// through.

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
import { DELETE } from "./route";

describe("DELETE /api/admin/sso", () => {
  beforeEach(() => {
    cookieStore.clear();
    vi.restoreAllMocks();
    process.env.ATLAS_HTTP_URL = "http://atlas:8080";
  });

  test("returns 401 without a session cookie", async () => {
    const fetchSpy = vi.spyOn(globalThis, "fetch");
    const res = await DELETE();
    expect(res.status).toBe(401);
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  test("proxies the DELETE with the bearer and returns the counts", async () => {
    cookieStore.set(ATLAS_JWT_COOKIE, "test-admin-bearer");
    const counts = {
      config_id: "cfg-1",
      mapping_count: 2,
      revoked_role_count: 5,
      affected_user_count: 3,
    };
    const spy = vi
      .spyOn(globalThis, "fetch")
      .mockResolvedValueOnce(
        new Response(JSON.stringify(counts), { status: 200 }),
      );
    const res = await DELETE();
    expect(res.status).toBe(200);
    const [url, init] = spy.mock.calls[0];
    expect(String(url)).toBe("http://atlas:8080/v1/admin/sso");
    expect((init as RequestInit).method).toBe("DELETE");
    expect((init as RequestInit).body).toBeUndefined();
    expect((init as RequestInit).headers).toMatchObject({
      Authorization: "Bearer test-admin-bearer",
    });
    expect(res.headers.get("Cache-Control")).toBe("no-store");
    expect(await res.json()).toEqual(counts);
  });

  test("passes a 409 last-admin refusal through", async () => {
    cookieStore.set(ATLAS_JWT_COOKIE, "test-admin-bearer");
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(JSON.stringify({ error: "last admin" }), { status: 409 }),
    );
    const res = await DELETE();
    expect(res.status).toBe(409);
    expect(await res.json()).toEqual({ error: "last admin" });
  });
});
