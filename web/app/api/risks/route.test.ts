// Slice 100 + Slice 105 — vitest coverage for web/app/api/risks/route.ts.
//
// The route guarantees:
//   * GET 401 when the bearer cookie is missing.
//   * GET forwards the bearer as `Authorization: Bearer <token>` to upstream.
//   * GET upstream JSON body passes through verbatim on success.
//   * GET upstream error status passes through as the response status.
//   * POST 401 when the bearer cookie is missing (slice 105 ISC-24).
//   * POST forwards bearer + JSON body to `/v1/risks` upstream (ISC-22).
//   * POST upstream non-2xx status propagates verbatim (ISC-23).
//
// Mirrors the slice 098 controls route test shape. All test bearers use
// the neutral `test-bearer-100` token — NO vendor token prefixes
// (`ghp_*`, `sk_*`, `eyJ*`, `AKIA*`) per slice 100 P0-A4.

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

// Lightweight NextRequest stub — the BFF only ever calls `req.json()`,
// so a thin shim with a configurable JSON payload is enough. Avoids
// pulling the real next/server runtime into the unit-test path.
function makeReq(body: unknown): {
  json: () => Promise<unknown>;
} {
  return {
    json: async () => body,
  };
}

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

describe("POST /api/risks", () => {
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

  test("401 when bearer cookie missing (ISC-24)", async () => {
    mockCookieGet.mockReturnValue(undefined);
    const fetchSpy = vi.spyOn(globalThis, "fetch");
    const req = makeReq({ title: "x" }) as unknown as Parameters<
      typeof POST
    >[0];
    const res = await POST(req);
    expect(res.status).toBe(401);
    const body = (await res.json()) as { error?: string };
    expect(body.error).toBe("unauthenticated");
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  test("forwards bearer + JSON body to /v1/risks on 201 success (ISC-22)", async () => {
    mockCookieGet.mockReturnValue({ value: "test-bearer-100" });

    const upstreamRisk = {
      id: "00000000-0000-0000-0000-000000000042",
      title: "new operational risk",
      description: "",
      category: "operational",
      methodology: "nist_800_30",
      inherent_score: { likelihood: 4, impact: 5 },
      treatment: "mitigate",
      treatment_owner: "alice",
      residual_score: {},
      accepter: "",
      instrument_reference: "",
      linked_control_ids: [],
      created_at: "2026-05-16T12:00:00Z",
      updated_at: "2026-05-16T12:00:00Z",
      themes: [],
      severity: 20,
    };

    let capturedURL = "";
    let capturedInit: RequestInit | undefined;
    const fetchSpy = vi
      .spyOn(globalThis, "fetch")
      .mockImplementation(
        async (input: RequestInfo | URL, init?: RequestInit) => {
          capturedURL = typeof input === "string" ? input : input.toString();
          capturedInit = init;
          return new Response(JSON.stringify({ risk: upstreamRisk }), {
            status: 201,
            headers: { "Content-Type": "application/json" },
          });
        },
      );

    const body = {
      title: "new operational risk",
      category: "operational",
      methodology: "nist_800_30",
      treatment: "mitigate",
      treatment_owner: "alice",
      inherent_score: { likelihood: 4, impact: 5 },
    };
    const req = makeReq(body) as unknown as Parameters<typeof POST>[0];
    const res = await POST(req);

    expect(res.status).toBe(201);
    expect(fetchSpy).toHaveBeenCalledOnce();
    expect(capturedURL).toBe("http://atlas:8080/v1/risks");
    expect(capturedInit?.method).toBe("POST");
    const headers = capturedInit?.headers as Record<string, string>;
    expect(headers.Authorization).toBe("Bearer test-bearer-100");
    expect(headers["Content-Type"]).toBe("application/json");
    expect(capturedInit?.body).toBe(JSON.stringify(body));

    const parsed = (await res.json()) as { risk: { title: string } };
    expect(parsed.risk.title).toBe("new operational risk");
  });

  test("propagates upstream 4xx status verbatim (ISC-23)", async () => {
    mockCookieGet.mockReturnValue({ value: "test-bearer-100" });
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(JSON.stringify({ error: "title is required" }), {
        status: 400,
        headers: { "Content-Type": "application/json" },
      }),
    );

    const req = makeReq({}) as unknown as Parameters<typeof POST>[0];
    const res = await POST(req);

    expect(res.status).toBe(400);
    const body = (await res.json()) as { error?: string };
    expect(body.error).toBe("title is required");
  });
});
