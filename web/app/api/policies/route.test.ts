// Slice 101 — vitest coverage for web/app/api/policies/route.ts.
//
// The route guarantees:
//   * 401 when the bearer cookie is missing.
//   * Forwards the bearer as `Authorization: Bearer <token>` to upstream.
//   * Upstream JSON body passes through verbatim on success.
//   * Upstream error status passes through as the response status.
//
// Mirrors the slice 100 risks route test shape. All test bearers use
// the neutral `test-bearer-101` token — NO vendor token prefixes
// (`ghp_*`, `sk_*`, `eyJ*`, `AKIA*`) per slice 101 P0-A5.

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

describe("GET /api/policies", () => {
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

  test("forwards bearer + returns upstream policies on success", async () => {
    mockCookieGet.mockReturnValue({ value: "test-bearer-101" });

    const fetchSpy = vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(
        JSON.stringify({
          policies: [
            {
              id: "00000000-0000-0000-0000-000000000001",
              title: "Information Security Policy",
              version: "v3.2",
              body_md: "",
              owner_role: "security_lead",
              approver_role: "cto",
              linked_control_ids: [],
              acknowledgment_required_roles: ["all_staff"],
              status: "published",
              source_attribution: "in_house",
              created_by: "user-1",
              published_at: "2026-01-15T00:00:00Z",
              created_at: "2026-01-01T00:00:00Z",
              updated_at: "2026-04-22T00:00:00Z",
            },
          ],
          count: 1,
        }),
        { status: 200, headers: { "Content-Type": "application/json" } },
      ),
    );

    const res = await GET();
    expect(res.status).toBe(200);
    const body = (await res.json()) as { policies: unknown[] };
    expect(body.policies).toHaveLength(1);

    expect(fetchSpy).toHaveBeenCalledOnce();
    const calledURL = String(fetchSpy.mock.calls[0]?.[0] ?? "");
    expect(calledURL).toContain("/v1/policies");
    // Slice 107: the BFF hard-codes `?include=ack_rate` so the
    // /policies page can render real ack-rate cells in one round trip
    // (anti-criterion P0-A2 — no client-side per-row fan-out).
    expect(calledURL).toContain("include=ack_rate");
    const init = fetchSpy.mock.calls[0]?.[1] as RequestInit | undefined;
    const headers = init?.headers as Record<string, string> | undefined;
    expect(headers?.Authorization).toBe("Bearer test-bearer-101");
  });

  test("propagates upstream error status", async () => {
    mockCookieGet.mockReturnValue({ value: "test-bearer-101" });
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response("server error", { status: 502 }),
    );

    const res = await GET();
    expect(res.status).toBe(502);
  });
});
