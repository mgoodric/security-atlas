// Slice 157 — vitest coverage for web/app/api/dashboard/upcoming/route.ts.
//
// The route guarantees:
//   * 401 when the bearer cookie is missing.
//   * Forwards the bearer as `Authorization: Bearer <token>` to upstream
//     and hits `/v1/upcoming` (slice 066 AC-4 unified rollup).
//   * Upstream JSON envelope `{upcoming, count, next_cursor}` passes
//     through verbatim on success.
//   * Empty-install path: upstream `{upcoming: [], count: 0,
//     next_cursor: ""}` -> 200 with empty envelope (P0-148-3: empty
//     /v1/upcoming renders empty-state, NOT a fabricated row).
//   * Upstream error status passes through as the response status.
//
// Mirrors the slice-147 activity-route + framework-posture-route test
// shape. Test bearer is the neutral `test-bearer-157` token — no vendor
// token prefixes per the slice-100 secret-scanning convention.

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

describe("GET /api/dashboard/upcoming", () => {
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

  test("forwards bearer + returns upstream upcoming envelope on success", async () => {
    mockCookieGet.mockReturnValue({ value: "test-bearer-157" });

    const fetchSpy = vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(
        JSON.stringify({
          upcoming: [
            {
              due_date: "2026-06-01T00:00:00Z",
              category: "exception",
              title: "Exception abc12345 expires",
              resource_type: "exception",
              resource_id: "abc12345-0000-0000-0000-000000000001",
            },
            {
              due_date: "2026-06-15T00:00:00Z",
              category: "audit_period",
              title: "Q2 SOC 2 audit period closes",
              resource_type: "audit_period",
              resource_id: "def67890-0000-0000-0000-000000000002",
            },
          ],
          count: 2,
          next_cursor: "",
        }),
        { status: 200, headers: { "Content-Type": "application/json" } },
      ),
    );

    const res = await GET();
    expect(res.status).toBe(200);
    const body = (await res.json()) as {
      upcoming: Array<{ category: string; title: string }>;
      count: number;
      next_cursor: string;
    };
    expect(body.count).toBe(2);
    expect(body.upcoming[0]?.category).toBe("exception");
    expect(body.upcoming[1]?.category).toBe("audit_period");

    expect(fetchSpy).toHaveBeenCalledOnce();
    const calledURL = String(fetchSpy.mock.calls[0]?.[0] ?? "");
    expect(calledURL).toContain("/v1/upcoming");
    const init = fetchSpy.mock.calls[0]?.[1] as RequestInit | undefined;
    const headers = init?.headers as Record<string, string> | undefined;
    expect(headers?.Authorization).toBe("Bearer test-bearer-157");
  });

  test("P0-148-3 empty rollup: upstream {upcoming:[],count:0,next_cursor:''} -> 200 empty envelope", async () => {
    mockCookieGet.mockReturnValue({ value: "test-bearer-157" });
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(
        JSON.stringify({ upcoming: [], count: 0, next_cursor: "" }),
        { status: 200, headers: { "Content-Type": "application/json" } },
      ),
    );

    const res = await GET();
    expect(res.status).toBe(200);
    const body = (await res.json()) as {
      upcoming: unknown[];
      count: number;
      next_cursor: string;
    };
    expect(body.upcoming).toEqual([]);
    expect(body.count).toBe(0);
    expect(body.next_cursor).toBe("");
  });

  test("propagates upstream error status", async () => {
    mockCookieGet.mockReturnValue({ value: "test-bearer-157" });
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response("server error", { status: 502 }),
    );

    const res = await GET();
    expect(res.status).toBe(502);
  });
});
