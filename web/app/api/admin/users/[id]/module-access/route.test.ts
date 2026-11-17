// #2172 — the module-access BFF forwards the bearer and the body, and
// passes the upstream status through.

import { beforeEach, describe, expect, test, vi } from "vitest";
import { mockNextServer } from "../../../../../../lib/test-utils/next-mocks";

const cookieStore = new Map<string, string>();

vi.mock("next/headers", () => ({
  cookies: async () => ({
    get: (name: string) =>
      cookieStore.has(name) ? { value: cookieStore.get(name) } : undefined,
  }),
}));

vi.mock("next/server", () => mockNextServer());

import { ATLAS_JWT_COOKIE } from "@/lib/auth";
import { PUT } from "./route";

const ID = "11111111-2222-3333-4444-555555555555";
const ctx = (id: string) => ({ params: Promise.resolve({ id }) });
const put = (body: unknown) =>
  new Request("http://x/api", { method: "PUT", body: JSON.stringify(body) });

describe("PUT /api/admin/users/{id}/module-access", () => {
  beforeEach(() => {
    cookieStore.clear();
    vi.restoreAllMocks();
    process.env.ATLAS_HTTP_URL = "http://atlas:8080";
  });

  test("401 without a session", async () => {
    const res = await PUT(put({ modules: null }), ctx(ID));
    expect(res.status).toBe(401);
  });

  test("400 on a non-uuid id, without calling upstream", async () => {
    cookieStore.set(ATLAS_JWT_COOKIE, "b");
    const spy = vi.spyOn(globalThis, "fetch");
    const res = await PUT(put({ modules: null }), ctx("../x"));
    expect(res.status).toBe(400);
    expect(spy).not.toHaveBeenCalled();
  });

  test("forwards bearer and body; passes the upstream answer through", async () => {
    cookieStore.set(ATLAS_JWT_COOKIE, "b");
    const spy = vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(`{"user_id":"${ID}","modules":["policies"]}`, {
        status: 200,
      }),
    );
    const res = await PUT(put({ modules: ["policies"] }), ctx(ID));
    expect(res.status).toBe(200);
    const [url, init] = spy.mock.calls[0] as [string, RequestInit];
    expect(url).toBe(`http://atlas:8080/v1/admin/users/${ID}/module-access`);
    expect(init.method).toBe("PUT");
    expect((init.headers as Record<string, string>).Authorization).toBe(
      "Bearer b",
    );
    expect(init.body).toBe('{"modules":["policies"]}');
    expect(((await res.json()) as { modules: string[] }).modules).toEqual([
      "policies",
    ]);
  });

  test("passes an upstream 400 through", async () => {
    cookieStore.set(ATLAS_JWT_COOKIE, "b");
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response('{"error":"you cannot narrow your own module access"}', {
        status: 400,
      }),
    );
    const res = await PUT(put({ modules: [] }), ctx(ID));
    expect(res.status).toBe(400);
  });
});
