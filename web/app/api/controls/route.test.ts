// Slice 098 + 104 — vitest coverage for web/app/api/controls/route.ts.
//
// The route guarantees:
//   * 401 when the bearer cookie is missing.
//   * Forwards the bearer as `Authorization: Bearer <token>` to upstream.
//   * Slice 104: calls `/v1/anchors?include=state` (the joined endpoint)
//     and passes the joined `{anchors:[{...,state}]}` shape through
//     verbatim.
//   * Upstream JSON body passes through verbatim on success.
//   * Upstream error status passes through as the response status.
//
// Mirrors the slice 094 calendar route test shape. All test bearers use
// the neutral `test-bearer-098` token — NO vendor token prefixes
// (`ghp_*`, `sk_*`, `eyJ*`, `AKIA*`) per slice 098 P0-A5.

import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

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

// Slice 224 — the GET handler now reads `req.nextUrl.searchParams`
// to pluck the optional ?scope=<cell_id> query param and forward it
// upstream. The tests construct a small fake whose only contract is
// the `nextUrl.searchParams.get()` call path used by the handler.
type FakeNextRequest = {
  nextUrl: { searchParams: URLSearchParams };
};
function makeReq(url = "/api/controls"): FakeNextRequest {
  const u = new URL(url, "http://test.local");
  return { nextUrl: { searchParams: u.searchParams } };
}

const mockCookieGet = vi.fn();

vi.mock("next/headers", () => ({
  cookies: () =>
    Promise.resolve({
      get: mockCookieGet,
    }),
}));

import { GET } from "./route";

describe("GET /api/controls", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
    mockCookieGet.mockReset();
    delete process.env.ATLAS_HTTP_URL;
    delete process.env.NEXT_PUBLIC_API_BASE_URL;
    process.env.ATLAS_HTTP_URL = "http://atlas:8080";
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  test("401 when bearer cookie missing", async () => {
    mockCookieGet.mockReturnValue(undefined);
    const res = await GET(makeReq() as never);
    expect(res.status).toBe(401);
    const body = (await res.json()) as { error?: string };
    expect(body.error).toBe("unauthenticated");
  });

  test("forwards bearer + returns upstream anchors on success", async () => {
    mockCookieGet.mockReturnValue({ value: "test-bearer-098" });

    const fetchSpy = vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(
        JSON.stringify({
          anchors: [
            {
              id: "00000000-0000-0000-0000-000000000001",
              scf_id: "test-01",
              family: "AAA",
              name: "test anchor one",
              description: "",
              // Slice 104: state is populated when a tenant control
              // satisfies this anchor; null otherwise.
              state: null,
            },
          ],
        }),
        { status: 200, headers: { "Content-Type": "application/json" } },
      ),
    );

    const res = await GET(makeReq() as never);
    expect(res.status).toBe(200);
    const body = (await res.json()) as {
      anchors: { state: unknown | null }[];
    };
    expect(body.anchors).toHaveLength(1);
    // Slice 104: the joined shape passes through verbatim.
    expect(body.anchors[0].state).toBeNull();

    expect(fetchSpy).toHaveBeenCalledOnce();
    const calledURL = String(fetchSpy.mock.calls[0]?.[0] ?? "");
    // Slice 104: we hit the joined endpoint, not the bare /v1/anchors.
    expect(calledURL).toContain("/v1/anchors?include=state");
    const init = fetchSpy.mock.calls[0]?.[1] as RequestInit | undefined;
    const headers = init?.headers as Record<string, string> | undefined;
    expect(headers?.Authorization).toBe("Bearer test-bearer-098");
  });

  test("slice 104: forwards populated state shape verbatim", async () => {
    mockCookieGet.mockReturnValue({ value: "test-bearer-104" });
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(
        JSON.stringify({
          anchors: [
            {
              id: "00000000-0000-0000-0000-000000000002",
              scf_id: "IAC-06",
              family: "IAC",
              name: "Multi-Factor Authentication (MFA)",
              description: "",
              state: {
                result: "fail",
                freshness_status: "fresh",
                last_observed_at: "2026-05-15T09:30:00Z",
                evaluated_at: "2026-05-16T10:00:00Z",
              },
            },
          ],
        }),
        { status: 200, headers: { "Content-Type": "application/json" } },
      ),
    );
    const res = await GET(makeReq() as never);
    expect(res.status).toBe(200);
    const body = (await res.json()) as {
      anchors: { state: { result: string } | null }[];
    };
    expect(body.anchors[0].state?.result).toBe("fail");
  });

  test("propagates upstream error status", async () => {
    mockCookieGet.mockReturnValue({ value: "test-bearer-098" });
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response("server error", { status: 502 }),
    );

    const res = await GET(makeReq() as never);
    expect(res.status).toBe(502);
  });

  // Slice 224 — the BFF forwards an optional ?scope=<cell_id> query
  // param to the upstream as `?scope=<cell_id>`. Verifies the
  // server-side intersection plumbing — the BFF never narrows
  // anchors itself; it forwards the predicate verbatim (P0-224-2).
  test("slice 224: forwards ?scope= upstream when set", async () => {
    mockCookieGet.mockReturnValue({ value: "test-bearer-224" });
    const fetchSpy = vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(JSON.stringify({ anchors: [] }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );

    const cellID = "00000000-0000-0000-0000-000000000aaa";
    const res = await GET(makeReq(`/api/controls?scope=${cellID}`) as never);
    expect(res.status).toBe(200);

    expect(fetchSpy).toHaveBeenCalledOnce();
    const calledURL = String(fetchSpy.mock.calls[0]?.[0] ?? "");
    expect(calledURL).toContain("/v1/anchors?");
    expect(calledURL).toContain("include=state");
    expect(calledURL).toContain(`scope=${cellID}`);
  });

  test("slice 224: omits scope when query param is absent", async () => {
    mockCookieGet.mockReturnValue({ value: "test-bearer-224" });
    const fetchSpy = vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(JSON.stringify({ anchors: [] }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );

    const res = await GET(makeReq("/api/controls") as never);
    expect(res.status).toBe(200);
    const calledURL = String(fetchSpy.mock.calls[0]?.[0] ?? "");
    expect(calledURL).not.toContain("scope=");
  });

  test("slice 224: empty scope value treated as no-filter", async () => {
    mockCookieGet.mockReturnValue({ value: "test-bearer-224" });
    const fetchSpy = vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(JSON.stringify({ anchors: [] }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );

    const res = await GET(makeReq("/api/controls?scope=") as never);
    expect(res.status).toBe(200);
    const calledURL = String(fetchSpy.mock.calls[0]?.[0] ?? "");
    expect(calledURL).not.toContain("scope=");
  });
});
