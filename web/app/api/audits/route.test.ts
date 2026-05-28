// Slice 102 — vitest coverage for web/app/api/audits/route.ts (GET).
// Slice 149 — adds POST coverage for the period-create wire path.
//
// The route guarantees:
//   * GET 401 when the bearer cookie is missing.
//   * GET forwards the bearer as `Authorization: Bearer <token>` to upstream.
//   * GET upstream JSON body passes through verbatim on success.
//   * GET upstream error status passes through as the response status.
//   * POST 401 when the bearer cookie is missing (slice 149).
//   * POST forwards bearer + JSON body to `/v1/audit-periods` upstream (slice 149).
//   * POST upstream non-2xx status propagates verbatim (slice 149).
//
// Mirrors the slice 098/105 route test shape. All test bearers use the
// neutral `test-bearer-102` / `test-bearer-149` tokens — NO vendor
// token prefixes (`ghp_*`, `sk_*`, `eyJ*`, `AKIA*`) per P0-A5.

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

import { GET, POST } from "./route";

// Lightweight NextRequest stub — the BFF only calls `req.json()`, so a
// thin shim with a configurable JSON payload is enough. Avoids pulling
// the real next/server runtime into the unit-test path.
function makeReq(body: unknown): { json: () => Promise<unknown> } {
  return {
    json: async () => body,
  };
}

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

describe("POST /api/audits (slice 149)", () => {
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
    const fetchSpy = vi.spyOn(globalThis, "fetch");
    const req = makeReq({ name: "x" }) as unknown as Parameters<typeof POST>[0];
    const res = await POST(req);
    expect(res.status).toBe(401);
    const body = (await res.json()) as { error?: string };
    expect(body.error).toBe("unauthenticated");
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  test("forwards bearer + JSON body to /v1/audit-periods on success", async () => {
    mockCookieGet.mockReturnValue({ value: "test-bearer-149" });

    const upstreamPeriod = {
      id: "00000000-0000-0000-0000-000000000149",
      name: "Q3 2026 SOC 2",
      framework_version_id: "00000000-0000-0000-0000-0000000000ff",
      period_start: "2026-07-01T00:00:00Z",
      period_end: "2026-09-30T00:00:00Z",
      status: "open",
      created_by: "test-actor",
      created_at: "2026-05-18T00:00:00Z",
      updated_at: "2026-05-18T00:00:00Z",
    };

    let capturedURL = "";
    let capturedInit: RequestInit | undefined;
    const fetchSpy = vi
      .spyOn(globalThis, "fetch")
      .mockImplementation(
        async (input: RequestInfo | URL, init?: RequestInit) => {
          capturedURL = typeof input === "string" ? input : input.toString();
          capturedInit = init;
          return new Response(JSON.stringify(upstreamPeriod), {
            status: 201,
            headers: { "Content-Type": "application/json" },
          });
        },
      );

    const body = {
      name: "Q3 2026 SOC 2",
      framework_version_id: "00000000-0000-0000-0000-0000000000ff",
      period_start: "2026-07-01T00:00:00Z",
      period_end: "2026-09-30T00:00:00Z",
    };
    const req = makeReq(body) as unknown as Parameters<typeof POST>[0];
    const res = await POST(req);

    expect(res.status).toBe(201);
    expect(fetchSpy).toHaveBeenCalledOnce();
    expect(capturedURL).toBe("http://atlas:8080/v1/audit-periods");
    expect(capturedInit?.method).toBe("POST");
    const headers = capturedInit?.headers as Record<string, string>;
    expect(headers.Authorization).toBe("Bearer test-bearer-149");
    expect(headers["Content-Type"]).toBe("application/json");
    expect(capturedInit?.body).toBe(JSON.stringify(body));
  });

  test("propagates upstream 4xx status verbatim", async () => {
    mockCookieGet.mockReturnValue({ value: "test-bearer-149" });
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(JSON.stringify({ error: "name must be non-empty" }), {
        status: 400,
        headers: { "Content-Type": "application/json" },
      }),
    );

    const req = makeReq({}) as unknown as Parameters<typeof POST>[0];
    const res = await POST(req);

    expect(res.status).toBe(400);
    const body = (await res.json()) as { error?: string };
    expect(body.error).toBe("name must be non-empty");
  });
});
