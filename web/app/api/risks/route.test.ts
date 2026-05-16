// Slice 100 — vitest coverage for web/app/api/risks/route.ts.
//
// The route guarantees:
//   * 401 when the bearer cookie is missing.
//   * Forwards the bearer as `Authorization: Bearer <token>` to upstream.
//   * Upstream JSON body passes through verbatim on success.
//   * Upstream error status passes through as the response status.
//
// Mirrors the slice 098 controls route test shape. All test bearers use
// the neutral `test-bearer-100` token — NO vendor token prefixes
// (`ghp_*`, `sk_*`, `eyJ*`, `AKIA*`) per slice 100 P0-A4.

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

describe("GET /api/risks", () => {
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

  test("forwards bearer + returns upstream risks on success", async () => {
    mockCookieGet.mockReturnValue({ value: "test-bearer-100" });

    const fetchSpy = vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(
        JSON.stringify({
          risks: [
            {
              id: "00000000-0000-0000-0000-000000000001",
              title: "test risk one",
              description: "",
              category: "operational",
              methodology: "nist_800_30",
              inherent_score: { likelihood: 4, impact: 5 },
              treatment: "mitigate",
              treatment_owner: "alpha",
              residual_score: { likelihood: 3, impact: 4 },
              accepter: "",
              instrument_reference: "",
              linked_control_ids: [],
              created_at: "2026-01-01T00:00:00Z",
              updated_at: "2026-01-01T00:00:00Z",
              themes: [],
              severity: 20,
            },
          ],
          count: 1,
        }),
        { status: 200, headers: { "Content-Type": "application/json" } },
      ),
    );

    const res = await GET();
    expect(res.status).toBe(200);
    const body = (await res.json()) as { risks: unknown[] };
    expect(body.risks).toHaveLength(1);

    expect(fetchSpy).toHaveBeenCalledOnce();
    const calledURL = String(fetchSpy.mock.calls[0]?.[0] ?? "");
    expect(calledURL).toContain("/v1/risks");
    const init = fetchSpy.mock.calls[0]?.[1] as RequestInit | undefined;
    const headers = init?.headers as Record<string, string> | undefined;
    expect(headers?.Authorization).toBe("Bearer test-bearer-100");
  });

  test("propagates upstream error status", async () => {
    mockCookieGet.mockReturnValue({ value: "test-bearer-100" });
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response("server error", { status: 502 }),
    );

    const res = await GET();
    expect(res.status).toBe(502);
  });
});
