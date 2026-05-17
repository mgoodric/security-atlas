// Slice 094 — vitest coverage for web/app/api/calendar/route.ts.
//
// The route guarantees:
//   * 401 when the bearer cookie is missing.
//   * Forwards query params (from / to / types) to /v1/calendar.
//   * Upstream JSON body passes through verbatim on success.
//   * Upstream error status passes through as the response status.
//
// Test fixtures use neutral strings only — NO vendor token prefixes.

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

const mockCookieGet = vi.fn();

vi.mock("next/headers", () => ({
  cookies: () =>
    Promise.resolve({
      get: mockCookieGet,
    }),
}));

import { GET } from "./route";

describe("GET /api/calendar", () => {
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
    const res = await GET(new Request("http://localhost/api/calendar"));
    expect(res.status).toBe(401);
    const body = await res.json();
    expect(body.error).toBe("unauthenticated");
  });

  test("forwards query params and returns upstream JSON on success", async () => {
    mockCookieGet.mockReturnValue({ value: "test-bearer-094" });

    const fetchSpy = vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(
        JSON.stringify({
          events: [],
          count: 0,
          from: "2026-05-16T00:00:00Z",
          to: "2026-08-14T00:00:00Z",
          truncated: false,
        }),
        { status: 200, headers: { "Content-Type": "application/json" } },
      ),
    );

    const res = await GET(
      new Request(
        "http://localhost/api/calendar?from=2026-06-01&to=2026-06-30&types=audit,policy",
      ),
    );
    expect(res.status).toBe(200);
    const body = await res.json();
    expect(body.count).toBe(0);

    // Confirm we forwarded all three query params to the upstream URL.
    expect(fetchSpy).toHaveBeenCalledOnce();
    const calledURL = String(fetchSpy.mock.calls[0]?.[0] ?? "");
    expect(calledURL).toContain("from=2026-06-01");
    expect(calledURL).toContain("to=2026-06-30");
    expect(calledURL).toContain("types=audit%2Cpolicy");
    // Bearer header propagated.
    const init = fetchSpy.mock.calls[0]?.[1] as RequestInit | undefined;
    const headers = init?.headers as Record<string, string> | undefined;
    expect(headers?.Authorization).toBe("Bearer test-bearer-094");
  });

  test("propagates upstream error status", async () => {
    mockCookieGet.mockReturnValue({ value: "test-bearer-094" });
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response("server error", { status: 502 }),
    );

    const res = await GET(new Request("http://localhost/api/calendar"));
    expect(res.status).toBe(502);
  });
});
