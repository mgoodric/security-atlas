// Slice 151 — vitest coverage for web/app/api/controls-list/route.ts.
//
// The route guarantees:
//   * 401 when the bearer cookie is missing.
//   * Forwards the bearer as `Authorization: Bearer <token>` to upstream.
//   * Calls `/v1/controls` (slice 151 backend) and passes the
//     `{controls: [...], count: N}` envelope through verbatim.
//   * Upstream error status propagates to the response.
//
// Mirrors the slice 098 controls route test shape. All test bearers use
// the neutral `test-bearer-151` token — NO vendor token prefixes
// (`ghp_*`, `sk_*`, `eyJ*`, `AKIA*`) per slice 151 P0-RISK-3.

import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";
import { mockNextServer } from "../../../lib/test-utils/next-mocks";

vi.mock("next/server", () => mockNextServer());

const mockCookieGet = vi.fn();

vi.mock("next/headers", () => ({
  cookies: () =>
    Promise.resolve({
      get: mockCookieGet,
    }),
}));

import { GET } from "./route";

describe("GET /api/controls-list", () => {
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

  test("forwards bearer + returns controls envelope on success", async () => {
    mockCookieGet.mockReturnValue({ value: "test-bearer-151" });

    const upstreamControls = [
      {
        id: "00000000-0000-0000-0000-000000000010",
        title: "Quarterly access review",
        control_family: "Identity & Access Mgmt",
        scf_id: "IAC-06",
        lifecycle_state: "active",
        bundle_id: "ctrl-iac-06",
      },
      {
        id: "00000000-0000-0000-0000-000000000011",
        title: "MFA enforced for all users",
        control_family: "Identity & Access Mgmt",
        scf_id: "IAC-06",
        lifecycle_state: "active",
        bundle_id: "ctrl-iac-06-mfa",
      },
    ];

    const fetchSpy = vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(JSON.stringify({ controls: upstreamControls, count: 2 }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );

    const res = await GET();
    expect(res.status).toBe(200);
    const body = (await res.json()) as {
      controls: typeof upstreamControls;
      count: number;
    };
    expect(body.controls).toHaveLength(2);
    expect(body.count).toBe(2);
    expect(body.controls[0].title).toBe("Quarterly access review");

    expect(fetchSpy).toHaveBeenCalledOnce();
    const calledURL = String(fetchSpy.mock.calls[0]?.[0] ?? "");
    expect(calledURL).toContain("/v1/controls");
    const init = fetchSpy.mock.calls[0]?.[1] as RequestInit | undefined;
    const headers = init?.headers as Record<string, string> | undefined;
    expect(headers?.Authorization).toBe("Bearer test-bearer-151");
  });

  test("propagates upstream error status", async () => {
    mockCookieGet.mockReturnValue({ value: "test-bearer-151" });
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response("server error", { status: 503 }),
    );

    const res = await GET();
    expect(res.status).toBe(503);
  });

  test("returns empty array when upstream returns zero controls", async () => {
    mockCookieGet.mockReturnValue({ value: "test-bearer-151" });
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(JSON.stringify({ controls: [], count: 0 }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );

    const res = await GET();
    expect(res.status).toBe(200);
    const body = (await res.json()) as { controls: unknown[]; count: number };
    expect(body.controls).toEqual([]);
    expect(body.count).toBe(0);
  });
});
