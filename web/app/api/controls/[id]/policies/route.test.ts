// Slice 253 — vitest coverage for the per-control policies BFF proxy.
//
// Pattern mirrors the existing `app/api/tenants/[id]/route.test.ts`
// per-id-with-bearer shape. AC-7 covers four response classes:
//
//   * 401 when the atlas_jwt cookie is absent
//   * 200 upstream → body passed through verbatim
//   * 404 upstream → propagated to the caller (the control-detail page's
//     `classifyControlDetailError` discriminator branches on this)
//   * 5xx upstream → propagated to the caller
//
// No vendor-prefixed tokens in fixtures (P0-A4 — slice 098 / 141 norm).

import { beforeEach, describe, expect, test, vi } from "vitest";

const cookieStore = new Map<string, string>();

vi.mock("next/headers", () => ({
  cookies: async () => ({
    get: (name: string) =>
      cookieStore.has(name) ? { value: cookieStore.get(name) } : undefined,
  }),
}));

vi.mock("next/server", () => {
  class NextRequest extends Request {}
  class NextResponse extends Response {
    static json(
      body: unknown,
      init?: { status?: number; headers?: Record<string, string> },
    ): NextResponse {
      return new NextResponse(body === null ? "null" : JSON.stringify(body), {
        status: init?.status ?? 200,
        headers: {
          "Content-Type": "application/json",
          ...(init?.headers ?? {}),
        },
      });
    }
  }
  return { NextRequest, NextResponse };
});

import { GET } from "./route";

const CONTROL_ID = "33333333-3333-3333-3333-333333330001";

function paramsFor(id: string): { params: Promise<{ id: string }> } {
  return { params: Promise.resolve({ id }) };
}

describe("GET /api/controls/[id]/policies", () => {
  beforeEach(() => {
    cookieStore.clear();
    vi.restoreAllMocks();
    delete process.env.ATLAS_HTTP_URL;
    delete process.env.NEXT_PUBLIC_API_BASE_URL;
    process.env.ATLAS_HTTP_URL = "http://atlas:8080";
  });

  test("returns 401 when atlas_jwt cookie is absent", async () => {
    const req = new Request(
      `http://localhost/api/controls/${CONTROL_ID}/policies`,
    );
    const res = await GET(
      req as unknown as Parameters<typeof GET>[0],
      paramsFor(CONTROL_ID),
    );
    expect(res.status).toBe(401);
    const body = (await res.json()) as { error?: string };
    expect(body.error).toBe("unauthenticated");
  });

  test("forwards bearer to upstream and passes 200 through", async () => {
    cookieStore.set("atlas_jwt", "test-bearer-253");
    const fetchSpy = vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(
        JSON.stringify({
          control_id: CONTROL_ID,
          policies: [
            {
              policy_id: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
              title: "Access Control Policy",
              version: "v3.2",
              status: "approved",
            },
          ],
          count: 1,
        }),
        { status: 200, headers: { "Content-Type": "application/json" } },
      ),
    );
    const req = new Request(
      `http://localhost/api/controls/${CONTROL_ID}/policies`,
    );
    const res = await GET(
      req as unknown as Parameters<typeof GET>[0],
      paramsFor(CONTROL_ID),
    );
    expect(res.status).toBe(200);
    const body = (await res.json()) as {
      policies: unknown[];
      count: number;
    };
    expect(body.count).toBe(1);
    expect(body.policies).toHaveLength(1);

    expect(fetchSpy).toHaveBeenCalledOnce();
    const calledURL = String(fetchSpy.mock.calls[0]?.[0] ?? "");
    expect(calledURL).toContain(`/v1/controls/${CONTROL_ID}/policies`);
    const init = fetchSpy.mock.calls[0]?.[1] as RequestInit | undefined;
    const headers = init?.headers as Record<string, string> | undefined;
    expect(headers?.Authorization).toBe("Bearer test-bearer-253");
  });

  test("propagates upstream 404 (control resolves but is unknown in this tenant)", async () => {
    cookieStore.set("atlas_jwt", "test-bearer-253");
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(JSON.stringify({ error: "control not found" }), {
        status: 404,
        headers: { "Content-Type": "application/json" },
      }),
    );
    const req = new Request(
      `http://localhost/api/controls/${CONTROL_ID}/policies`,
    );
    const res = await GET(
      req as unknown as Parameters<typeof GET>[0],
      paramsFor(CONTROL_ID),
    );
    expect(res.status).toBe(404);
  });

  test("propagates upstream 5xx (downstream-platform failure)", async () => {
    cookieStore.set("atlas_jwt", "test-bearer-253");
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response("server error", { status: 502 }),
    );
    const req = new Request(
      `http://localhost/api/controls/${CONTROL_ID}/policies`,
    );
    const res = await GET(
      req as unknown as Parameters<typeof GET>[0],
      paramsFor(CONTROL_ID),
    );
    expect(res.status).toBe(502);
  });
});
