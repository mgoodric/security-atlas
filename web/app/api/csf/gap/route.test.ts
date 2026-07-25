// Slice 515 — vitest coverage for web/app/api/csf/gap/route.ts.
//
// The route guarantees:
//   * 401 when the bearer cookie is missing.
//   * 400 when framework_version is absent.
//   * forwards framework_version to /v1/csf/gap and returns the upstream JSON.
//   * upstream error status passes through.
//
// Test fixtures use neutral strings only — NO vendor token prefixes.

import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";
import { mockNextServer } from "../../../../lib/test-utils/next-mocks";
import { TEST_BEARER_DEFAULT } from "../../../../lib/test-utils/test-tokens";

vi.mock("next/server", () => mockNextServer());

const mockCookieGet = vi.fn();

vi.mock("next/headers", () => ({
  cookies: () =>
    Promise.resolve({
      get: mockCookieGet,
    }),
}));

import { GET } from "./route";

const FV = "11111111-1111-1111-1111-111111111111";

describe("GET /api/csf/gap", () => {
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
    const res = await GET(
      new Request(`http://localhost/api/csf/gap?framework_version=${FV}`),
    );
    expect(res.status).toBe(401);
    const body = await res.json();
    expect(body.error).toBe("unauthenticated");
  });

  test("400 when framework_version missing", async () => {
    mockCookieGet.mockReturnValue({ value: TEST_BEARER_DEFAULT });
    const res = await GET(new Request("http://localhost/api/csf/gap"));
    expect(res.status).toBe(400);
  });

  test("forwards framework_version and returns upstream JSON on success", async () => {
    mockCookieGet.mockReturnValue({ value: TEST_BEARER_DEFAULT });

    const upstream = {
      framework_version_id: FV,
      gap: [
        {
          subcategory_code: "GV.OC-01",
          subcategory_title: "GV.OC-01 subcategory",
          requirement_id: "22222222-2222-2222-2222-222222222222",
          current_outcome: "partial",
          target_outcome: "fully",
          gap_delta: 2,
          met: false,
        },
      ],
      gap_count: 1,
      tier_rating: null,
    };

    const fetchSpy = vi
      .spyOn(globalThis, "fetch")
      .mockResolvedValueOnce(
        new Response(JSON.stringify(upstream), { status: 200 }),
      );

    const res = await GET(
      new Request(`http://localhost/api/csf/gap?framework_version=${FV}`),
    );
    expect(res.status).toBe(200);
    const body = await res.json();
    expect(body.gap_count).toBe(1);
    expect(body.gap[0].subcategory_code).toBe("GV.OC-01");
    expect(body.gap[0].gap_delta).toBe(2);

    // The upstream call carried the framework_version + the bearer.
    expect(fetchSpy).toHaveBeenCalledTimes(1);
    const calledURL = fetchSpy.mock.calls[0][0] as string;
    expect(calledURL).toContain("/v1/csf/gap");
    expect(calledURL).toContain(`framework_version=${FV}`);
  });

  test("upstream error status passes through", async () => {
    mockCookieGet.mockReturnValue({ value: TEST_BEARER_DEFAULT });
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response("boom", { status: 503, statusText: "Service Unavailable" }),
    );
    const res = await GET(
      new Request(`http://localhost/api/csf/gap?framework_version=${FV}`),
    );
    expect(res.status).toBe(503);
  });
});
