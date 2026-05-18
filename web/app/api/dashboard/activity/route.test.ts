// Slice 147 — vitest coverage for web/app/api/dashboard/activity/route.ts.
//
// The route guarantees:
//   * 401 when the bearer cookie is missing.
//   * Forwards the bearer as `Authorization: Bearer <token>` to upstream.
//   * Upstream JSON envelope `{activity, count, next_cursor}` passes through
//     verbatim on success.
//   * Empty-install path: upstream `{activity: [], count: 0, next_cursor: ""}`
//     -> 200 with empty envelope (AC-5: no 500, no placeholder).
//   * Upstream error status passes through as the response status.
//
// Mirrors the slice-147 framework-posture-route test shape.

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

describe("GET /api/dashboard/activity", () => {
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

  test("forwards bearer + returns upstream activity envelope on success", async () => {
    mockCookieGet.mockReturnValue({ value: "test-bearer-147" });

    const fetchSpy = vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(
        JSON.stringify({
          activity: [
            {
              ts: "2026-05-17T12:00:00Z",
              event_type: "evidence_accepted",
              actor: "connector:aws",
              resource_type: "evidence_record",
              resource_id: "ev-abc-123",
              summary: { evidence_kind: "aws.s3.bucket_encryption.v1" },
            },
          ],
          count: 1,
          next_cursor: "",
        }),
        { status: 200, headers: { "Content-Type": "application/json" } },
      ),
    );

    const res = await GET();
    expect(res.status).toBe(200);
    const body = (await res.json()) as {
      activity: Array<{ event_type: string; ts: string }>;
      count: number;
      next_cursor: string;
    };
    expect(body.count).toBe(1);
    expect(body.activity[0]?.event_type).toBe("evidence_accepted");

    expect(fetchSpy).toHaveBeenCalledOnce();
    const calledURL = String(fetchSpy.mock.calls[0]?.[0] ?? "");
    expect(calledURL).toContain("/v1/activity");
    const init = fetchSpy.mock.calls[0]?.[1] as RequestInit | undefined;
    const headers = init?.headers as Record<string, string> | undefined;
    expect(headers?.Authorization).toBe("Bearer test-bearer-147");
  });

  test("AC-5 empty install: upstream empty envelope -> 200 empty envelope", async () => {
    mockCookieGet.mockReturnValue({ value: "test-bearer-147" });
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(
        JSON.stringify({ activity: [], count: 0, next_cursor: "" }),
        {
          status: 200,
          headers: { "Content-Type": "application/json" },
        },
      ),
    );

    const res = await GET();
    expect(res.status).toBe(200);
    const body = (await res.json()) as {
      activity: unknown[];
      count: number;
      next_cursor: string;
    };
    expect(body.activity).toEqual([]);
    expect(body.count).toBe(0);
    expect(body.next_cursor).toBe("");
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
