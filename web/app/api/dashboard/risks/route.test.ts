// Slice 157 — vitest coverage for web/app/api/dashboard/risks/route.ts.
//
// The route guarantees:
//   * 401 when the bearer cookie is missing.
//   * Forwards the bearer as `Authorization: Bearer <token>` to upstream
//     and hits `/v1/risks?treatment=mitigate&sort=residual,age`
//     (slice 066 AC-3 — the residual,age ranking that slice 157
//     re-points the dashboard onto).
//   * Upstream `{risks, count}` envelope is unpacked: `risks` array is
//     forwarded; `count` is re-derived from `risks.length` so the BFF
//     envelope is consistent even if the upstream omits count.
//   * Empty-install path: upstream `{risks: [], count: 0}` -> 200 with
//     `{risks: [], count: 0}` (P0-148-3: empty list -> empty-state).
//   * Upstream error status passes through as the response status.
//
// Mirrors the slice-147 framework-posture-route + activity-route test
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

describe("GET /api/dashboard/risks", () => {
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

  test("forwards bearer + appends sort=residual,age + returns {risks, count}", async () => {
    mockCookieGet.mockReturnValue({ value: "test-bearer-157" });

    const fetchSpy = vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(
        JSON.stringify({
          risks: [
            {
              id: "11111111-0000-0000-0000-000000000001",
              title: "Vendor SSO outage",
              description: "Identity provider single point of failure",
              category: "vendor",
              methodology: "nist_800_30",
              inherent_score: { likelihood: 4, impact: 5 },
              treatment: "mitigate",
              treatment_owner: "security-team",
              residual_score: { likelihood: 3, impact: 4 },
              accepter: "",
              instrument_reference: "",
              linked_control_ids: [],
              created_at: "2026-01-01T00:00:00Z",
              updated_at: "2026-02-01T00:00:00Z",
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
      risks: Array<{ id: string; treatment: string }>;
      count: number;
    };
    expect(body.count).toBe(1);
    expect(body.risks[0]?.treatment).toBe("mitigate");

    expect(fetchSpy).toHaveBeenCalledOnce();
    const calledURL = String(fetchSpy.mock.calls[0]?.[0] ?? "");
    // The slice-157 contract: BFF must forward both filters together —
    // treatment=mitigate AND the residual,age sort. If either is
    // missing, the dashboard regresses to the slice-040 unsorted shape.
    expect(calledURL).toContain("/v1/risks");
    expect(calledURL).toContain("treatment=mitigate");
    // The sort key passes the comma through literally — the slice-066
    // ParseListSort handler accepts `residual,age` (slice 066 AC-3).
    expect(calledURL).toMatch(/sort=residual(?:,|%2C)age/);
    const init = fetchSpy.mock.calls[0]?.[1] as RequestInit | undefined;
    const headers = init?.headers as Record<string, string> | undefined;
    expect(headers?.Authorization).toBe("Bearer test-bearer-157");
  });

  test("P0-148-3 empty mitigate: upstream {risks:[],count:0} -> 200 empty envelope", async () => {
    mockCookieGet.mockReturnValue({ value: "test-bearer-157" });
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(JSON.stringify({ risks: [], count: 0 }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );

    const res = await GET();
    expect(res.status).toBe(200);
    const body = (await res.json()) as {
      risks: unknown[];
      count: number;
    };
    expect(body.risks).toEqual([]);
    expect(body.count).toBe(0);
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
