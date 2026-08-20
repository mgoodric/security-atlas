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
import { COVERAGE_STRENGTH_BAND_TOKENS } from "@/lib/api/coverage-strength-matrix";

function requestFor(search = "") {
  return {
    nextUrl: {
      searchParams: new URLSearchParams(search),
    },
  };
}

describe("GET /api/coverage-strength/matrix", () => {
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
    const res = await GET(requestFor() as never);
    expect(res.status).toBe(401);
    const body = (await res.json()) as { error?: string };
    expect(body.error).toBe("unauthenticated");
  });

  test("forwards bearer, query params, axis, and semantic band tokens", async () => {
    mockCookieGet.mockReturnValue({ value: "test-bearer-473" });

    const fetchSpy = vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(
        JSON.stringify({
          axis: {
            rows: "scf_anchor",
            columns: "framework_current_version",
            cell_value: "max_requirement_contribution",
            aggregation: "max(edge_strength * anchor_coverage)",
          },
          bands: [
            { band: "strong", token: "pass", label: "Strong coverage" },
            { band: "partial", token: "warning", label: "Partial coverage" },
            { band: "weak", token: "critical", label: "Weak coverage" },
            { band: "uncovered", token: "info", label: "Uncovered" },
          ],
          frameworks: [
            {
              framework_version_id: "00000000-0000-0000-0000-000000000001",
              framework_slug: "soc2",
              framework_name: "SOC 2",
              version: "2017",
              status: "current",
            },
          ],
          rows: [
            {
              anchor: {
                id: "00000000-0000-0000-0000-0000000000a1",
                scf_id: "NET-04",
                family: "Network Security",
                name: "Boundary protection",
              },
              cells: [
                {
                  framework_version_id: "00000000-0000-0000-0000-000000000001",
                  coverage_strength: 0.8,
                  confidence_band: "strong",
                  band_token: "pass",
                  requirement_count: 1,
                  contributing: true,
                },
              ],
            },
          ],
          limit: 25,
          offset: 0,
        }),
        { status: 200, headers: { "Content-Type": "application/json" } },
      ),
    );

    const res = await GET(
      requestFor("limit=25&family=Network+Security") as never,
    );
    expect(res.status).toBe(200);
    const body = (await res.json()) as {
      axis: { rows: string; aggregation: string };
      bands: Array<{ band: string; token: string }>;
      rows: Array<{ cells: Array<{ band_token: string }> }>;
    };
    expect(body.axis.rows).toBe("scf_anchor");
    expect(body.axis.aggregation).toBe("max(edge_strength * anchor_coverage)");
    expect(body.bands).toEqual(
      expect.arrayContaining([
        expect.objectContaining({ band: "strong", token: "pass" }),
      ]),
    );
    expect(body.rows[0]?.cells[0]?.band_token).toBe("pass");

    expect(fetchSpy).toHaveBeenCalledOnce();
    const calledURL = String(fetchSpy.mock.calls[0]?.[0] ?? "");
    expect(calledURL).toContain(
      "/v1/coverage-strength/matrix?limit=25&family=Network+Security",
    );
    const init = fetchSpy.mock.calls[0]?.[1] as RequestInit | undefined;
    const headers = init?.headers as Record<string, string> | undefined;
    expect(headers?.Authorization).toBe("Bearer test-bearer-473");
  });

  test("band token helper is semantic status tokens only", () => {
    expect(COVERAGE_STRENGTH_BAND_TOKENS).toEqual({
      strong: "pass",
      partial: "warning",
      weak: "critical",
      uncovered: "info",
    });
  });

  test("propagates upstream error status", async () => {
    mockCookieGet.mockReturnValue({ value: "test-bearer-473" });
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response("server error", { status: 502 }),
    );

    const res = await GET(requestFor() as never);
    expect(res.status).toBe(502);
  });
});
