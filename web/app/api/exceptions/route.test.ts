// Slice 177 — vitest coverage for web/app/api/exceptions/route.ts.
//
// The route guarantees:
//   * 401 when the bearer cookie is missing.
//   * Forwards the bearer as `Authorization: Bearer <token>` to upstream.
//   * Forwards the WHITELISTED query params (status, control_id) — and
//     ONLY those (`tenant_id` / `debug` / arbitrary keys are dropped).
//   * Upstream JSON body passes through verbatim on success.
//   * Upstream error status passes through as the response status.
//
// Mirrors the slice 099 evidence + slice 101 policies route test shape.
// All test bearers use neutral `test-bearer-177` tokens — NO vendor
// token prefixes (`ghp_*`, `sk_*`, `eyJ*`, `AKIA*`).

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

function makeRequest(url: string): Request {
  return new Request(url);
}

describe("GET /api/exceptions", () => {
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
    const res = await GET(makeRequest("http://localhost/api/exceptions"));
    expect(res.status).toBe(401);
    const body = (await res.json()) as { error?: string };
    expect(body.error).toBe("unauthenticated");
  });

  test("forwards bearer + returns upstream body on 200", async () => {
    mockCookieGet.mockReturnValue({ value: "test-bearer-177" });

    const upstreamPayload = {
      exceptions: [
        {
          id: "11111111-1111-1111-1111-111111111111",
          control_id: "22222222-2222-2222-2222-222222222222",
          scope_cell_predicate: {},
          justification: "vendor patch pending",
          compensating_controls: [],
          requested_by: "alice",
          requested_at: "2026-05-01T00:00:00Z",
          expires_at: "2026-08-01T00:00:00Z",
          status: "active",
          created_at: "2026-05-01T00:00:00Z",
          updated_at: "2026-05-01T00:00:00Z",
        },
      ],
      count: 1,
    };

    const fetchSpy = vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(JSON.stringify(upstreamPayload), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );

    const res = await GET(makeRequest("http://localhost/api/exceptions"));
    expect(res.status).toBe(200);

    // Bearer was forwarded as Authorization header.
    const init = fetchSpy.mock.calls[0]?.[1] as RequestInit | undefined;
    const auth = (init?.headers as Record<string, string> | undefined)
      ?.Authorization;
    expect(auth).toBe("Bearer test-bearer-177");

    // Upstream URL is the bare /v1/exceptions when no query string given.
    const calledURL = String(fetchSpy.mock.calls[0]?.[0] ?? "");
    expect(calledURL).toBe("http://atlas:8080/v1/exceptions");

    // Body passes through verbatim.
    const body = (await res.json()) as typeof upstreamPayload;
    expect(body).toEqual(upstreamPayload);
  });

  test("forwards status filter param", async () => {
    mockCookieGet.mockReturnValue({ value: "test-bearer-177" });

    const fetchSpy = vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(JSON.stringify({ exceptions: [], count: 0 }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );

    const res = await GET(
      makeRequest("http://localhost/api/exceptions?status=active"),
    );
    expect(res.status).toBe(200);

    const calledURL = String(fetchSpy.mock.calls[0]?.[0] ?? "");
    expect(calledURL).toBe("http://atlas:8080/v1/exceptions?status=active");
  });

  test("forwards control_id filter param", async () => {
    mockCookieGet.mockReturnValue({ value: "test-bearer-177" });

    const fetchSpy = vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(JSON.stringify({ exceptions: [], count: 0 }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );

    const cid = "00000000-0000-0000-0000-000000000001";
    const res = await GET(
      makeRequest(`http://localhost/api/exceptions?control_id=${cid}`),
    );
    expect(res.status).toBe(200);

    const calledURL = String(fetchSpy.mock.calls[0]?.[0] ?? "");
    expect(calledURL).toBe(`http://atlas:8080/v1/exceptions?control_id=${cid}`);
  });

  test("drops non-whitelisted params (tenant_id, debug)", async () => {
    mockCookieGet.mockReturnValue({ value: "test-bearer-177" });

    const fetchSpy = vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(JSON.stringify({ exceptions: [], count: 0 }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );

    const res = await GET(
      makeRequest(
        "http://localhost/api/exceptions?status=active&tenant_id=other-tenant&debug=1",
      ),
    );
    expect(res.status).toBe(200);

    // Only status survived; tenant_id and debug were dropped.
    const calledURL = String(fetchSpy.mock.calls[0]?.[0] ?? "");
    expect(calledURL).toBe("http://atlas:8080/v1/exceptions?status=active");
    expect(calledURL).not.toContain("tenant_id");
    expect(calledURL).not.toContain("debug");
  });

  test("upstream error status passes through", async () => {
    mockCookieGet.mockReturnValue({ value: "test-bearer-177" });

    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(JSON.stringify({ error: "upstream boom" }), {
        status: 502,
        headers: { "Content-Type": "application/json" },
      }),
    );

    const res = await GET(makeRequest("http://localhost/api/exceptions"));
    expect(res.status).toBe(502);
    const body = (await res.json()) as { error?: string };
    expect(body.error).toBe("upstream boom");
  });

  test("cross-tenant isolation: BFF does not consult or echo caller tenant_id", async () => {
    // Defense-in-depth assertion: even if a caller injects ?tenant_id=,
    // it is dropped before upstream. Cross-tenant isolation is enforced
    // by RLS at the DB layer (the bearer carries the only tenant
    // claim), but the BFF must never become a passthrough that ferries
    // a forged tenant context.
    mockCookieGet.mockReturnValue({ value: "test-bearer-177" });

    const fetchSpy = vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(JSON.stringify({ exceptions: [], count: 0 }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );

    await GET(
      makeRequest("http://localhost/api/exceptions?tenant_id=evil-tenant-9999"),
    );
    const calledURL = String(fetchSpy.mock.calls[0]?.[0] ?? "");
    expect(calledURL).toBe("http://atlas:8080/v1/exceptions");
    expect(calledURL).not.toContain("evil-tenant-9999");
  });
});
