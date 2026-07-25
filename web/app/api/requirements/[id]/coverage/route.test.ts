// Slice 482 — vitest coverage for
// web/app/api/requirements/[id]/coverage/route.ts.
//
// The route guarantees:
//   * 401 when the bearer cookie is missing.
//   * Forwards the bearer as `Authorization: Bearer <token>` to upstream.
//   * Hits GET /v1/requirements/{id}/coverage with the id URL-encoded.
//   * Passes the slice 482 additive rollup fields (coverage_strength +
//     confidence_band) through verbatim alongside the existing
//     requirement / anchors / controls fields (P0-482-5 additive).
//   * Propagates the upstream error status.
//
// Neutral `test-bearer-482` token — NO vendor token prefixes (slice 098
// P0-A5).

import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";
import { mockNextServer } from "../../../../../lib/test-utils/next-mocks";

vi.mock("next/server", () => mockNextServer());

const mockCookieGet = vi.fn();

vi.mock("next/headers", () => ({
  cookies: () =>
    Promise.resolve({
      get: mockCookieGet,
    }),
}));

import { GET } from "./route";

function ctxFor(id: string) {
  return { params: Promise.resolve({ id }) };
}

describe("GET /api/requirements/[id]/coverage", () => {
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
    const res = await GET({} as never, ctxFor("soc2:2017:CC6.6") as never);
    expect(res.status).toBe(401);
    const body = (await res.json()) as { error?: string };
    expect(body.error).toBe("unauthenticated");
  });

  test("forwards bearer + passes rollup fields through verbatim", async () => {
    mockCookieGet.mockReturnValue({ value: "test-bearer-482" });

    const fetchSpy = vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(
        JSON.stringify({
          requirement: {
            id: "00000000-0000-0000-0000-000000000001",
            code: "CC6.6",
            title: "Logical access security measures",
          },
          anchors: [
            {
              id: "00000000-0000-0000-0000-0000000000a1",
              scf_id: "NET-04",
              family: "NET",
              name: "Boundary protection",
              relationship_type: "subset_of",
              strength: 0.8,
            },
          ],
          controls: [],
          // Slice 482 additive rollup fields.
          coverage_strength: 0.7,
          confidence_band: "partial",
        }),
        { status: 200, headers: { "Content-Type": "application/json" } },
      ),
    );

    const res = await GET(
      {} as never,
      ctxFor("soc2:2017:CC6.6") as never,
    );
    expect(res.status).toBe(200);
    const body = (await res.json()) as {
      requirement: { code: string };
      anchors: { scf_id: string; strength: number }[];
      coverage_strength: number;
      confidence_band: string;
    };
    // Existing fields survive (additive only — P0-482-5).
    expect(body.requirement.code).toBe("CC6.6");
    expect(body.anchors[0].scf_id).toBe("NET-04");
    expect(body.anchors[0].strength).toBe(0.8);
    // The new rollup fields pass through unchanged.
    expect(body.coverage_strength).toBe(0.7);
    expect(body.confidence_band).toBe("partial");

    expect(fetchSpy).toHaveBeenCalledOnce();
    const calledURL = String(fetchSpy.mock.calls[0]?.[0] ?? "");
    expect(calledURL).toContain(
      "/v1/requirements/soc2%3A2017%3ACC6.6/coverage",
    );
    const init = fetchSpy.mock.calls[0]?.[1] as RequestInit | undefined;
    const headers = init?.headers as Record<string, string> | undefined;
    expect(headers?.Authorization).toBe("Bearer test-bearer-482");
  });

  test("passes an uncovered rollup through verbatim", async () => {
    mockCookieGet.mockReturnValue({ value: "test-bearer-482" });
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(
        JSON.stringify({
          requirement: { id: "x", code: "CC6.6", title: "t" },
          anchors: [],
          controls: [],
          coverage_strength: 0,
          confidence_band: "uncovered",
        }),
        { status: 200, headers: { "Content-Type": "application/json" } },
      ),
    );
    const res = await GET({} as never, ctxFor("x") as never);
    expect(res.status).toBe(200);
    const body = (await res.json()) as {
      coverage_strength: number;
      confidence_band: string;
    };
    expect(body.coverage_strength).toBe(0);
    expect(body.confidence_band).toBe("uncovered");
  });

  test("propagates upstream error status", async () => {
    mockCookieGet.mockReturnValue({ value: "test-bearer-482" });
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response("not found", { status: 404 }),
    );
    const res = await GET({} as never, ctxFor("missing") as never);
    expect(res.status).toBe(404);
  });
});
