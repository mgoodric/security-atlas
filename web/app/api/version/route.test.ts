// Slice 072 — vitest seed coverage for app/api/version/route.ts.
//
// The route guarantees:
//   * No bearer cookie is read or forwarded (P0-A1: public endpoint)
//   * Upstream JSON body and status pass through verbatim on success
//   * 5-minute Cache-Control is set on success responses
//   * Upstream transport failure returns a typed 502 error payload
//
// These behaviors lock in the contract the VersionFooter component +
// useVersion() hook depend on (web/lib/version.ts).
//
// Hard rule (P0-A9, slice 069 lesson): test fixtures use neutral strings
// only — NO vendor token prefixes (ghp_*, sk_*, AKIA*, eyJ*).

import { beforeEach, describe, expect, test, vi } from "vitest";

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

import { GET } from "./route";

describe("GET /api/version", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
    delete process.env.ATLAS_HTTP_URL;
    delete process.env.NEXT_PUBLIC_API_BASE_URL;
    process.env.ATLAS_HTTP_URL = "http://atlas:8080";
  });

  test("proxies upstream /v1/version verbatim on success", async () => {
    const upstreamBody = JSON.stringify({
      version: "v1.5.0",
      commit: "abc1234",
      build_time: "2026-05-15T15:00:00Z",
      go_version: "go1.26.1",
    });

    let capturedURL = "";
    let capturedInit: RequestInit | undefined;
    const fetchSpy = vi
      .spyOn(globalThis, "fetch")
      .mockImplementation(
        async (input: RequestInfo | URL, init?: RequestInit) => {
          capturedURL = typeof input === "string" ? input : input.toString();
          capturedInit = init;
          return new Response(upstreamBody, {
            status: 200,
            headers: { "Content-Type": "application/json" },
          });
        },
      );

    const res = await GET();

    expect(fetchSpy).toHaveBeenCalledOnce();
    expect(capturedURL).toBe("http://atlas:8080/v1/version");
    // Bearer must NOT be forwarded — upstream is public.
    const headers = (capturedInit?.headers ?? {}) as Record<string, string>;
    expect(headers.Authorization).toBeUndefined();
    expect(res.status).toBe(200);
    const body = (await res.json()) as { version: string };
    expect(body.version).toBe("v1.5.0");
  });

  test("sets Cache-Control: public, max-age=300 on success", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(
        '{"version":"v1.5.0","commit":"","build_time":"","go_version":""}',
        {
          status: 200,
          headers: { "Content-Type": "application/json" },
        },
      ),
    );

    const res = await GET();
    expect(res.headers.get("Cache-Control")).toBe("public, max-age=300");
  });

  test("returns Content-Type application/json on success", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(
        '{"version":"v1.5.0","commit":"","build_time":"","go_version":""}',
        {
          status: 200,
          headers: { "Content-Type": "application/json" },
        },
      ),
    );

    const res = await GET();
    expect(res.headers.get("Content-Type")).toBe("application/json");
  });

  test("returns 502 with typed error on upstream transport failure", async () => {
    vi.spyOn(globalThis, "fetch").mockRejectedValueOnce(
      new Error("connect ECONNREFUSED 127.0.0.1:8080"),
    );

    const res = await GET();
    expect(res.status).toBe(502);
    const body = (await res.json()) as { error: string; detail: string };
    expect(body.error).toBe("version_unavailable");
    expect(body.detail).toContain("ECONNREFUSED");
  });

  test("passes upstream non-2xx status through verbatim", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response('{"error":"unavailable"}', {
        status: 503,
        headers: { "Content-Type": "application/json" },
      }),
    );

    const res = await GET();
    expect(res.status).toBe(503);
  });
});
