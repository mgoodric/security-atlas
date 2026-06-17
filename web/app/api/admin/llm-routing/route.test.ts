// Slice 499 — vitest coverage for the /api/admin/llm-routing BFF route.
//
// The BFF proxies the tenant-admin GET/PUT/DELETE routes to the platform's
// /v1/admin/llm-routing. Tests cover: 401 without session; GET/PUT/DELETE
// happy-path proxying with the bearer; upstream status pass-through; and that
// the route never injects a tenant_id (the platform derives tenant from the
// credential). The provider key is platform-side write-only — the BFF only
// proxies bytes.

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
import { DELETE, GET, PUT } from "./route";

function putRequest(body: unknown): Request {
  return { json: async () => body } as unknown as Request;
}

describe("/api/admin/llm-routing BFF", () => {
  beforeEach(() => {
    cookieStore.clear();
    vi.restoreAllMocks();
    process.env.ATLAS_HTTP_URL = "http://atlas:8080";
  });

  test("GET returns 401 without a session cookie", async () => {
    const fetchSpy = vi.spyOn(globalThis, "fetch");
    const res = await GET();
    expect(res.status).toBe(401);
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  test("GET proxies the masked config and sets no-store", async () => {
    cookieStore.set(ATLAS_JWT_COOKIE, "test-admin-bearer");
    const spy = vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(
        JSON.stringify({
          provider: "anthropic",
          is_cloud: true,
          has_api_key: true,
        }),
        { status: 200 },
      ),
    );
    const res = await GET();
    expect(res.status).toBe(200);
    const [url, init] = spy.mock.calls[0];
    expect(String(url)).toBe("http://atlas:8080/v1/admin/llm-routing");
    expect((init as RequestInit).method ?? "GET").toBe("GET");
    expect((init as RequestInit).headers).toMatchObject({
      Authorization: "Bearer test-admin-bearer",
    });
    expect(res.headers.get("Cache-Control")).toBe("no-store");
    const body = (await res.json()) as { provider?: string };
    expect(body.provider).toBe("anthropic");
  });

  test("PUT forwards the body and bearer to upstream", async () => {
    cookieStore.set(ATLAS_JWT_COOKIE, "test-admin-bearer");
    const spy = vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(
        JSON.stringify({
          provider: "openai",
          is_cloud: true,
          has_api_key: true,
        }),
        { status: 200 },
      ),
    );
    const res = await PUT(
      putRequest({ provider: "openai", api_key: "fake-test-key-000" }),
    );
    expect(res.status).toBe(200);
    const [url, init] = spy.mock.calls[0];
    expect(String(url)).toBe("http://atlas:8080/v1/admin/llm-routing");
    expect((init as RequestInit).method).toBe("PUT");
    // The body is proxied verbatim; tenant_id is NOT injected by the BFF.
    const sent = JSON.parse(String((init as RequestInit).body));
    expect(sent.provider).toBe("openai");
    expect(sent.tenant_id).toBeUndefined();
  });

  test("PUT 401 without a session", async () => {
    const fetchSpy = vi.spyOn(globalThis, "fetch");
    const res = await PUT(putRequest({ provider: "openai" }));
    expect(res.status).toBe(401);
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  test("PUT passes upstream error status through (e.g. 403 non-admin)", async () => {
    cookieStore.set(ATLAS_JWT_COOKIE, "test-viewer-bearer");
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(JSON.stringify({ error: "tenant-admin role required" }), {
        status: 403,
      }),
    );
    const res = await PUT(putRequest({ provider: "anthropic", api_key: "x" }));
    expect(res.status).toBe(403);
  });

  test("DELETE proxies clear to upstream", async () => {
    cookieStore.set(ATLAS_JWT_COOKIE, "test-admin-bearer");
    const spy = vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(JSON.stringify({ provider: "local-ollama" }), {
        status: 200,
      }),
    );
    const res = await DELETE();
    expect(res.status).toBe(200);
    const [url, init] = spy.mock.calls[0];
    expect(String(url)).toBe("http://atlas:8080/v1/admin/llm-routing");
    expect((init as RequestInit).method).toBe("DELETE");
  });
});
