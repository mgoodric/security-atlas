// Slice 223 — vitest coverage for web/app/api/search/route.ts (GET).
//
// Closes AC-10 (BFF route handler unit coverage: cookie → upstream
// forwarding, error paths) and pins P0-223-1 by asserting the BFF
// always forwards via Authorization header (RLS enforces tenancy
// upstream; the BFF never reads or forwards a tenant_id).
//
// The route guarantees:
//   * 401 when the bearer cookie is missing.
//   * Forwards the bearer as `Authorization: Bearer <token>` to upstream.
//   * Passes through the query string (`q`, `types`, `limit`) to
//     `/v1/search` verbatim.
//   * Upstream JSON body passes through on success.
//   * Upstream error status passes through as the response status.
//   * 400 from upstream (q < 2 chars, unknown type, etc.) propagates.
//
// Mirrors the slice 102 (`audits/route.test.ts`) shape. All test
// bearers use the neutral `test-bearer-223` token — NO vendor token
// prefixes per slice CLAUDE.md secret-scanning guidance.

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

// Minimal NextRequest stub — the BFF only reads `req.url` (to
// extract the search params); a string URL satisfies it.
function makeReq(url: string): { url: string } {
  return { url };
}

describe("GET /api/search", () => {
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
    const req = makeReq(
      "http://localhost:3000/api/search?q=iam",
    ) as unknown as Parameters<typeof GET>[0];
    const res = await GET(req);
    expect(res.status).toBe(401);
    const body = (await res.json()) as { error?: string };
    expect(body.error).toBe("unauthenticated");
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  test("forwards bearer + query string to /v1/search on success", async () => {
    mockCookieGet.mockReturnValue({ value: "test-bearer-223" });

    let capturedURL = "";
    let capturedInit: RequestInit | undefined;
    const fetchSpy = vi
      .spyOn(globalThis, "fetch")
      .mockImplementation(
        async (input: RequestInfo | URL, init?: RequestInit) => {
          capturedURL = typeof input === "string" ? input : input.toString();
          capturedInit = init;
          return new Response(
            JSON.stringify({
              hits: [
                {
                  id: "00000000-0000-0000-0000-000000000001",
                  type: "controls",
                  title: "Encryption at rest",
                  snippet: "Encryption at rest — prod object stores",
                  relevance_score: 1.0,
                },
              ],
              count: 1,
              partial_types: [],
            }),
            {
              status: 200,
              headers: { "Content-Type": "application/json" },
            },
          );
        },
      );

    const req = makeReq(
      "http://localhost:3000/api/search?q=iam&types=controls&limit=10",
    ) as unknown as Parameters<typeof GET>[0];
    const res = await GET(req);

    expect(res.status).toBe(200);
    const body = (await res.json()) as {
      hits: unknown[];
      count: number;
    };
    expect(body.count).toBe(1);
    expect(body.hits).toHaveLength(1);

    expect(fetchSpy).toHaveBeenCalledOnce();
    // P0-223-1: route forwards via /v1/search, NOT a per-primitive
    // endpoint. The query string passes through unchanged so the
    // upstream is the single source of validation truth.
    expect(capturedURL).toContain("/v1/search?");
    expect(capturedURL).toContain("q=iam");
    expect(capturedURL).toContain("types=controls");
    expect(capturedURL).toContain("limit=10");
    const headers = capturedInit?.headers as Record<string, string>;
    expect(headers.Authorization).toBe("Bearer test-bearer-223");
  });

  test("forwards types=anchors through verbatim (slice 661)", async () => {
    // Slice 661 added the `anchors` result type upstream. The BFF is a
    // thin proxy with NO type whitelist of its own — it forwards the
    // `types` param verbatim and lets the upstream own validation. This
    // pins that `anchors` is NOT stripped at the BFF layer.
    mockCookieGet.mockReturnValue({ value: "test-bearer-661" });
    let capturedURL = "";
    vi.spyOn(globalThis, "fetch").mockImplementation(async (input) => {
      capturedURL = typeof input === "string" ? input : input.toString();
      return new Response(
        JSON.stringify({
          hits: [
            {
              id: "00000000-0000-0000-0000-0000000000a1",
              type: "anchors",
              title: "CRY-04 — Encryption At Rest",
              snippet: "CRY-04 — Encryption At Rest",
              relevance_score: 1.0,
            },
          ],
          count: 1,
          partial_types: [],
        }),
        { status: 200, headers: { "Content-Type": "application/json" } },
      );
    });

    const req = makeReq(
      "http://localhost:3000/api/search?q=CRY-04&types=anchors&limit=10",
    ) as unknown as Parameters<typeof GET>[0];
    const res = await GET(req);
    expect(res.status).toBe(200);
    const body = (await res.json()) as {
      hits: { type: string }[];
    };
    expect(body.hits[0]?.type).toBe("anchors");
    expect(capturedURL).toContain("types=anchors");
  });

  test("propagates upstream 400 (q too short) verbatim", async () => {
    mockCookieGet.mockReturnValue({ value: "test-bearer-223" });
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(
        JSON.stringify({ error: "q must be at least 2 characters" }),
        { status: 400, headers: { "Content-Type": "application/json" } },
      ),
    );

    const req = makeReq(
      "http://localhost:3000/api/search?q=a",
    ) as unknown as Parameters<typeof GET>[0];
    const res = await GET(req);
    expect(res.status).toBe(400);
    const body = (await res.json()) as { error?: string };
    expect(body.error).toBe("q must be at least 2 characters");
  });

  test("propagates upstream 403 unchanged (RBAC denial passes through)", async () => {
    mockCookieGet.mockReturnValue({ value: "test-bearer-223" });
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(JSON.stringify({ error: "forbidden" }), {
        status: 403,
        headers: { "Content-Type": "application/json" },
      }),
    );

    const req = makeReq(
      "http://localhost:3000/api/search?q=iam",
    ) as unknown as Parameters<typeof GET>[0];
    const res = await GET(req);
    expect(res.status).toBe(403);
  });

  test("propagates upstream 5xx verbatim", async () => {
    mockCookieGet.mockReturnValue({ value: "test-bearer-223" });
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response("internal error", { status: 502 }),
    );

    const req = makeReq(
      "http://localhost:3000/api/search?q=iam",
    ) as unknown as Parameters<typeof GET>[0];
    const res = await GET(req);
    expect(res.status).toBe(502);
  });

  test("forwards an empty query string when the caller omits search params", async () => {
    // The BFF is a thin proxy — upstream validates q ≥ 2 chars and
    // returns 400. We assert the BFF does NOT short-circuit the
    // validation; let the upstream own the contract.
    mockCookieGet.mockReturnValue({ value: "test-bearer-223" });
    const fetchSpy = vi
      .spyOn(globalThis, "fetch")
      .mockResolvedValueOnce(
        new Response(
          JSON.stringify({ error: "q must be at least 2 characters" }),
          { status: 400, headers: { "Content-Type": "application/json" } },
        ),
      );

    const req = makeReq(
      "http://localhost:3000/api/search",
    ) as unknown as Parameters<typeof GET>[0];
    const res = await GET(req);
    expect(res.status).toBe(400);

    const calledURL = String(fetchSpy.mock.calls[0]?.[0] ?? "");
    // The BFF should call /v1/search with no query — upstream returns
    // the 400. The contract is "thin proxy; upstream validates".
    expect(calledURL).toContain("/v1/search");
  });
});
