// Slice 143 — vitest coverage for the /api/admin/tenants BFF route.
//
// The BFF proxies super_admin-gated GET + POST routes to the platform's
// /v1/admin/tenants. The tests cover:
//
//   * GET: 401 without session cookie; 200 with items; upstream error
//     pass-through (5xx).
//   * POST: 400 on invalid body shape (no name, no slug, bad slug
//     regex, bad creator_joins_as); 401 without session; happy path
//     posts the canonical body shape to upstream; upstream 409 / 429
//     pass through with their status + error message.

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
import { GET, POST } from "./route";

function makeRequest(body: unknown): { json: () => Promise<unknown> } {
  return {
    json: async () => body,
  };
}

describe("GET /api/admin/tenants", () => {
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

  test("returns 200 with items list when upstream succeeds", async () => {
    cookieStore.set(SESSION_COOKIE, "test-super-admin-bearer");
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(
        JSON.stringify({
          items: [
            {
              id: "11111111-1111-4111-8111-111111111111",
              name: "Bootstrap",
              slug: null,
              is_bootstrap_tenant: true,
              created_at: "2026-05-22T00:00:00.000Z",
            },
            {
              id: "22222222-2222-4222-8222-222222222222",
              name: "Tenant Two",
              slug: "tenant-two",
              is_bootstrap_tenant: false,
              created_at: "2026-05-22T10:00:00.000Z",
              created_by_user_id: "33333333-3333-4333-8333-333333333333",
            },
          ],
        }),
        { status: 200, headers: { "Content-Type": "application/json" } },
      ),
    );

    const res = await GET();
    expect(res.status).toBe(200);
    const body = (await res.json()) as {
      items: Array<{ id: string; name: string }>;
    };
    expect(body.items).toHaveLength(2);
    expect(body.items[0].id).toBe("11111111-1111-4111-8111-111111111111");
    expect(body.items[1].name).toBe("Tenant Two");
  });

  test("upstream 5xx error propagates with same status", async () => {
    cookieStore.set(SESSION_COOKIE, "test-super-admin-bearer");
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(JSON.stringify({ error: "database down" }), {
        status: 500,
        statusText: "Internal Server Error",
        headers: { "Content-Type": "application/json" },
      }),
    );

    const res = await GET();
    expect(res.status).toBe(500);
    // The apiFetch helper reports `status + statusText`, not the
    // upstream's body — the body's `error` field is not surfaced
    // by this codepath. The status alone is the BFF contract.
    const body = (await res.json()) as { error?: string };
    expect(body.error).toBeTruthy();
  });
});

describe("POST /api/admin/tenants", () => {
  beforeEach(() => {
    cookieStore.clear();
    vi.restoreAllMocks();
    process.env.ATLAS_HTTP_URL = "http://atlas:8080";
  });

  test("returns 401 without session cookie", async () => {
    const fetchSpy = vi.spyOn(globalThis, "fetch");
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const res = await POST(makeRequest({}) as any);
    expect(res.status).toBe(401);
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  test("returns 400 when name is empty", async () => {
    cookieStore.set(SESSION_COOKIE, "bearer");
    const res = await POST(
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      makeRequest({ name: "", slug: "valid-slug" }) as any,
    );
    expect(res.status).toBe(400);
    const body = (await res.json()) as { error: string };
    expect(body.error).toContain("name");
  });

  test("returns 400 when slug is empty", async () => {
    cookieStore.set(SESSION_COOKIE, "bearer");
    const res = await POST(
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      makeRequest({ name: "Acme", slug: "" }) as any,
    );
    expect(res.status).toBe(400);
    const body = (await res.json()) as { error: string };
    expect(body.error).toContain("slug");
  });

  test("returns 400 when slug fails regex (uppercase)", async () => {
    cookieStore.set(SESSION_COOKIE, "bearer");
    const res = await POST(
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      makeRequest({ name: "Acme", slug: "INVALID" }) as any,
    );
    expect(res.status).toBe(400);
    const body = (await res.json()) as { error: string };
    expect(body.error).toMatch(/slug must match/);
  });

  test("returns 400 when slug fails regex (leading hyphen)", async () => {
    cookieStore.set(SESSION_COOKIE, "bearer");
    const res = await POST(
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      makeRequest({ name: "Acme", slug: "-bad" }) as any,
    );
    expect(res.status).toBe(400);
  });

  test("returns 400 when creator_joins_as is not 'admin' or 'none'", async () => {
    cookieStore.set(SESSION_COOKIE, "bearer");
    const res = await POST(
      makeRequest({
        name: "Acme",
        slug: "acme",
        creator_joins_as: "bogus",
        // eslint-disable-next-line @typescript-eslint/no-explicit-any
      }) as any,
    );
    expect(res.status).toBe(400);
    const body = (await res.json()) as { error: string };
    expect(body.error).toContain("creator_joins_as");
  });

  test("happy path: POSTs to upstream + returns 200", async () => {
    cookieStore.set(SESSION_COOKIE, "test-super-admin-bearer");
    const fetchSpy = vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(
        JSON.stringify({
          tenant: {
            id: "44444444-4444-4444-8444-444444444444",
            name: "Acme",
            slug: "acme",
            is_bootstrap_tenant: false,
            created_at: "2026-05-22T11:00:00.000Z",
          },
        }),
        { status: 200, headers: { "Content-Type": "application/json" } },
      ),
    );

    const res = await POST(
      makeRequest({
        name: "Acme",
        slug: "acme",
        creator_joins_as: "none",
        // eslint-disable-next-line @typescript-eslint/no-explicit-any
      }) as any,
    );
    expect(res.status).toBe(200);
    const body = (await res.json()) as { tenant: { name: string } };
    expect(body.tenant.name).toBe("Acme");
    expect(fetchSpy).toHaveBeenCalledTimes(1);
    const call = fetchSpy.mock.calls[0];
    expect(call[0]).toMatch(/\/v1\/admin\/tenants$/);
    expect(call[1]?.method).toBe("POST");
    const callBody = JSON.parse((call[1]?.body as string) ?? "{}") as {
      name: string;
      slug: string;
      creator_joins_as: string;
    };
    expect(callBody.name).toBe("Acme");
    expect(callBody.slug).toBe("acme");
    expect(callBody.creator_joins_as).toBe("none");
  });

  test("upstream 409 (duplicate slug) propagates status + message", async () => {
    cookieStore.set(SESSION_COOKIE, "bearer");
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(JSON.stringify({ error: "slug already in use" }), {
        status: 409,
        headers: { "Content-Type": "application/json" },
      }),
    );

    const res = await POST(makeRequest({ name: "Dup", slug: "dup" }) as never);
    expect(res.status).toBe(409);
    const body = (await res.json()) as { error: string };
    expect(body.error).toBe("slug already in use");
  });

  test("upstream 429 (rate-limited) propagates status + message", async () => {
    cookieStore.set(SESSION_COOKIE, "bearer");
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(
        JSON.stringify({
          error: "rate limit exceeded: max 100 tenants per super_admin per 24h",
        }),
        {
          status: 429,
          headers: {
            "Content-Type": "application/json",
            "Retry-After": "86400",
          },
        },
      ),
    );

    const res = await POST(
      makeRequest({ name: "Rate", slug: "rate" }) as never,
    );
    expect(res.status).toBe(429);
    const body = (await res.json()) as { error: string };
    expect(body.error).toContain("rate limit exceeded");
  });
});
