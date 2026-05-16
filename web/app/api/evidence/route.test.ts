// Slice 099 — vitest coverage for web/app/api/evidence/route.ts.
//
// The route guarantees:
//   * 401 when the bearer cookie is missing.
//   * 400 when control_id query param is missing (the upstream
//     `GET /v1/evidence` handler requires it — see
//     `internal/api/controldetail/handler.go` Evidence handler).
//   * Forwards the bearer as `Authorization: Bearer <token>` to upstream.
//   * Upstream JSON body passes through verbatim on success.
//   * Upstream error status passes through as the response status.
//
// Mirrors the slice 098 controls + slice 102 audits route test shape.
// All test bearers use the neutral `test-bearer-099` token — NO vendor
// token prefixes (`ghp_*`, `sk_*`, `eyJ*`, `AKIA*`) per P0-A4.

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

function makeRequest(url: string): Request {
  return new Request(url);
}

describe("GET /api/evidence", () => {
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
      makeRequest(
        "http://localhost/api/evidence?control_id=00000000-0000-0000-0000-000000000001",
      ),
    );
    expect(res.status).toBe(401);
    const body = (await res.json()) as { error?: string };
    expect(body.error).toBe("unauthenticated");
  });

  test("400 when control_id query param missing", async () => {
    mockCookieGet.mockReturnValue({ value: "test-bearer-099" });
    const res = await GET(makeRequest("http://localhost/api/evidence"));
    expect(res.status).toBe(400);
    const body = (await res.json()) as { error?: string };
    expect(body.error).toMatch(/control_id/);
  });

  test("forwards bearer + control_id + returns upstream evidence on success", async () => {
    mockCookieGet.mockReturnValue({ value: "test-bearer-099" });

    const fetchSpy = vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(
        JSON.stringify({
          control_id: "00000000-0000-0000-0000-000000000001",
          evidence: [
            {
              evidence_id: "11111111-1111-1111-1111-111111111111",
              evidence_kind: "aws.s3.encryption_status.v1",
              observed_at: "2026-05-16T09:42:00Z",
              source: { actor_type: "connector", actor_id: "aws-connector" },
              content_hash:
                "7a4f0123456789abcdef0123456789abcdef0123456789abcdef0123456789b2c1",
              scope_cell: "22222222-2222-2222-2222-222222222222",
            },
          ],
          count: 1,
          next_cursor: "",
        }),
        { status: 200, headers: { "Content-Type": "application/json" } },
      ),
    );

    const res = await GET(
      makeRequest(
        "http://localhost/api/evidence?control_id=00000000-0000-0000-0000-000000000001",
      ),
    );
    expect(res.status).toBe(200);
    const body = (await res.json()) as {
      evidence: unknown[];
      count: number;
    };
    expect(body.evidence).toHaveLength(1);
    expect(body.count).toBe(1);

    expect(fetchSpy).toHaveBeenCalledOnce();
    const calledURL = String(fetchSpy.mock.calls[0]?.[0] ?? "");
    expect(calledURL).toContain("/v1/evidence?control_id=");
    expect(calledURL).toContain("00000000-0000-0000-0000-000000000001");
    const init = fetchSpy.mock.calls[0]?.[1] as RequestInit | undefined;
    const headers = init?.headers as Record<string, string> | undefined;
    expect(headers?.Authorization).toBe("Bearer test-bearer-099");
  });

  test("forwards optional since + cursor + limit params when present", async () => {
    mockCookieGet.mockReturnValue({ value: "test-bearer-099" });

    const fetchSpy = vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(
        JSON.stringify({
          control_id: "00000000-0000-0000-0000-000000000001",
          evidence: [],
          count: 0,
          next_cursor: "",
        }),
        { status: 200, headers: { "Content-Type": "application/json" } },
      ),
    );

    await GET(
      makeRequest(
        "http://localhost/api/evidence?control_id=00000000-0000-0000-0000-000000000001&since=2026-05-01T00:00:00Z&cursor=abc&limit=25",
      ),
    );

    const calledURL = String(fetchSpy.mock.calls[0]?.[0] ?? "");
    expect(calledURL).toContain("since=");
    expect(calledURL).toContain("cursor=abc");
    expect(calledURL).toContain("limit=25");
  });

  test("ignores unrelated query params (does NOT forward arbitrary keys)", async () => {
    mockCookieGet.mockReturnValue({ value: "test-bearer-099" });

    const fetchSpy = vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(
        JSON.stringify({
          control_id: "00000000-0000-0000-0000-000000000001",
          evidence: [],
          count: 0,
          next_cursor: "",
        }),
        { status: 200, headers: { "Content-Type": "application/json" } },
      ),
    );

    await GET(
      makeRequest(
        "http://localhost/api/evidence?control_id=00000000-0000-0000-0000-000000000001&tenant_id=evil&debug=1",
      ),
    );

    const calledURL = String(fetchSpy.mock.calls[0]?.[0] ?? "");
    // Anti-injection guard: client-supplied tenant_id MUST NOT reach
    // upstream. The platform derives tenant from the bearer.
    expect(calledURL).not.toContain("tenant_id");
    expect(calledURL).not.toContain("debug");
  });

  test("propagates upstream error status", async () => {
    mockCookieGet.mockReturnValue({ value: "test-bearer-099" });
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response("server error", { status: 502 }),
    );

    const res = await GET(
      makeRequest(
        "http://localhost/api/evidence?control_id=00000000-0000-0000-0000-000000000001",
      ),
    );
    expect(res.status).toBe(502);
  });

  test("propagates 403 unchanged (RBAC denial passes through)", async () => {
    mockCookieGet.mockReturnValue({ value: "test-bearer-099" });
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(JSON.stringify({ error: "forbidden" }), {
        status: 403,
        headers: { "Content-Type": "application/json" },
      }),
    );

    const res = await GET(
      makeRequest(
        "http://localhost/api/evidence?control_id=00000000-0000-0000-0000-000000000001",
      ),
    );
    expect(res.status).toBe(403);
  });
});
