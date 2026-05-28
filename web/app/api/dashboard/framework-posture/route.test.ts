// Slice 147 — vitest coverage for web/app/api/dashboard/framework-posture/route.ts.
//
// The route guarantees:
//   * 401 when the bearer cookie is missing.
//   * Forwards the bearer as `Authorization: Bearer <token>` to upstream.
//   * Upstream JSON body passes through verbatim on success.
//   * Upstream error status passes through as the response status.
//   * Empty-install path: upstream `{frameworks: [], count: 0}` -> 200
//     with empty envelope (AC-5: no 500, no placeholder).
//
// Mirrors the slice-100 risks-route test shape. The test bearer is the
// neutral `test-bearer-147` token — NO vendor token prefixes per the
// slice-100 secret-scanning convention.

import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";
import { mockNextServer } from "../../../../lib/test-utils/next-mocks";

vi.mock("next/server", () => mockNextServer());

const mockCookieGet = vi.fn();

vi.mock("next/headers", () => ({
  cookies: () =>
    Promise.resolve({
      get: mockCookieGet,
    }),
}));

import { GET } from "./route";

describe("GET /api/dashboard/framework-posture", () => {
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
    const res = await GET();
    expect(res.status).toBe(401);
    const body = (await res.json()) as { error?: string };
    expect(body.error).toBe("unauthenticated");
  });

  test("forwards bearer + returns upstream posture envelope on success", async () => {
    mockCookieGet.mockReturnValue({ value: "test-bearer-147" });

    const fetchSpy = vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(
        JSON.stringify({
          frameworks: [
            {
              framework_id: "00000000-0000-0000-0000-000000000001",
              framework_version: "2024",
              coverage_pct: 87.5,
              freshness_composite: 92.0,
              trend_delta_90d: 12.5,
            },
          ],
          count: 1,
        }),
        { status: 200, headers: { "Content-Type": "application/json" } },
      ),
    );

    const res = await GET();
    expect(res.status).toBe(200);
    const body = (await res.json()) as {
      frameworks: Array<{ framework_version: string; coverage_pct: number }>;
      count: number;
    };
    expect(body.count).toBe(1);
    expect(body.frameworks[0]?.coverage_pct).toBe(87.5);

    expect(fetchSpy).toHaveBeenCalledOnce();
    const calledURL = String(fetchSpy.mock.calls[0]?.[0] ?? "");
    expect(calledURL).toContain("/v1/frameworks/posture");
    const init = fetchSpy.mock.calls[0]?.[1] as RequestInit | undefined;
    const headers = init?.headers as Record<string, string> | undefined;
    expect(headers?.Authorization).toBe("Bearer test-bearer-147");
  });

  test("AC-5 empty install: upstream {frameworks:[],count:0} -> 200 empty envelope", async () => {
    mockCookieGet.mockReturnValue({ value: "test-bearer-147" });
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(JSON.stringify({ frameworks: [], count: 0 }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );

    const res = await GET();
    expect(res.status).toBe(200);
    const body = (await res.json()) as {
      frameworks: unknown[];
      count: number;
    };
    expect(body.frameworks).toEqual([]);
    expect(body.count).toBe(0);
  });

  test("propagates upstream error status", async () => {
    mockCookieGet.mockReturnValue({ value: "test-bearer-147" });
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response("server error", { status: 502 }),
    );

    const res = await GET();
    expect(res.status).toBe(502);
  });
});
