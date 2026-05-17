// Slice 102 — vitest coverage for web/app/api/audits/route.ts.
//
// The route guarantees:
//   * 401 when the bearer cookie is missing.
//   * Forwards the bearer as `Authorization: Bearer <token>` to upstream.
//   * Upstream JSON body passes through verbatim on success.
//   * Upstream error status passes through as the response status.
//
// Mirrors the slice 098 controls route test shape. All test bearers
// use the neutral `test-bearer-102` token — NO vendor token prefixes
// (`ghp_*`, `sk_*`, `eyJ*`, `AKIA*`) per P0-A5.

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

describe("GET /api/audits", () => {
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

  test("forwards bearer + returns upstream audit_periods on success", async () => {
    mockCookieGet.mockReturnValue({ value: "test-bearer-102" });

    const fetchSpy = vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(
        JSON.stringify({
          audit_periods: [
            {
              id: "00000000-0000-0000-0000-000000000001",
              name: "test period 01",
              framework_version_id: "00000000-0000-0000-0000-0000000000ff",
              period_start: "2026-01-01T00:00:00Z",
              period_end: "2026-03-31T00:00:00Z",
              status: "open",
              created_by: "test-actor",
              created_at: "2026-01-01T00:00:00Z",
              updated_at: "2026-01-01T00:00:00Z",
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
      audit_periods: unknown[];
      count: number;
    };
    expect(body.audit_periods).toHaveLength(1);
    expect(body.count).toBe(1);

    expect(fetchSpy).toHaveBeenCalledOnce();
    const calledURL = String(fetchSpy.mock.calls[0]?.[0] ?? "");
    expect(calledURL).toContain("/v1/audit-periods");
    // P0-A1: routes to the period index endpoint, NOT the
    // /v1/me/audit-periods endpoint that the /audit/[controlId]
    // workspace uses (which is per-user assignments).
    expect(calledURL).not.toContain("/v1/me/audit-periods");
    const init = fetchSpy.mock.calls[0]?.[1] as RequestInit | undefined;
    const headers = init?.headers as Record<string, string> | undefined;
    expect(headers?.Authorization).toBe("Bearer test-bearer-102");
  });

  test("propagates upstream error status", async () => {
    mockCookieGet.mockReturnValue({ value: "test-bearer-102" });
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response("server error", { status: 502 }),
    );

    const res = await GET();
    expect(res.status).toBe(502);
  });

  test("propagates 403 unchanged (RBAC denial passes through)", async () => {
    mockCookieGet.mockReturnValue({ value: "test-bearer-102" });
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(JSON.stringify({ error: "forbidden" }), {
        status: 403,
        headers: { "Content-Type": "application/json" },
      }),
    );

    const res = await GET();
    expect(res.status).toBe(403);
  });
});
